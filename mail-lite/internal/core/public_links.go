package core

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// unrestrictedPlatformSegment is intentionally opaque in public URLs. The
// empty platform policy remains visible in the admin UI as "全部平台", while
// public links avoid exposing that rule in a plainly named path segment.
const unrestrictedPlatformSegment = "p0"

// Public codes are stable random identifiers independent of internal account IDs.
// Resolution still requires an explicitly imported recipient and platform policy.
func publicLinkURLs(base string, l Link, platformName string) (string, string) {
	platform := unrestrictedPlatformSegment
	if l.PlatformID != "" {
		label := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, platformName)
		runes := []rune(strings.Trim(label, "-"))
		if len(runes) > 40 {
			runes = runes[:40]
		}
		if len(runes) == 0 {
			runes = []rune("platform")
		}
		platform = string(runes) + "~" + l.PlatformID
	}
	account, recipient, prefix := l.AccountCode, recipientLocalPart(l.Recipient), "/show/"
	if account == "" {
		// Older primaries may not have supplied public codes to a replica yet.
		account, recipient, prefix = l.AccountID, l.Recipient, "/m/"
	}
	path := url.PathEscape(platform) + "/" + url.PathEscape(account) + "/" + url.PathEscape(recipient)
	return base + prefix + path, base + "/api/mailboxes/" + path + "/messages"
}

func recipientLocalPart(recipient string) string {
	i := strings.LastIndexByte(recipient, '@')
	if i < 0 {
		return recipient
	}
	return recipient[:i]
}

func recipientFromSegment(segment string) string {
	if strings.Contains(segment, "@") {
		return address(segment)
	}
	if address(segment+"@example.invalid") == "" {
		return ""
	}
	return strings.ToLower(segment)
}

func recipientPredicate(recipient string) string {
	if strings.Contains(recipient, "@") {
		return "recipient=? COLLATE NOCASE"
	}
	return "substr(recipient,1,instr(recipient,'@')-1)=? COLLATE NOCASE"
}

func (a *App) platformNames(ctx context.Context) (map[string]string, error) {
	records, e := a.store.All(ctx, "platform")
	if e != nil {
		return nil, e
	}
	names := map[string]string{}
	for _, record := range records {
		var p Platform
		if e = decode(record.Data, &p); e != nil {
			return nil, e
		}
		names[p.ID] = p.Name
	}
	return names, nil
}

func platformFromSegment(segment string) (string, bool) {
	if segment == unrestrictedPlatformSegment || segment == "all" {
		return "", true
	}
	// The label is descriptive and can survive a platform rename. The ID is
	// authoritative, and must match the policy already stored on the link.
	i := strings.LastIndexByte(segment, '~')
	if i < 1 || i == len(segment)-1 {
		return "", false
	}
	return segment[i+1:], true
}

func readableLinkParts(raw string) (platform, account, recipient string, ok bool) {
	u, e := url.Parse(raw)
	if e != nil {
		return
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	var values []string
	if len(parts) == 4 && (parts[0] == "m" || parts[0] == "show") {
		values = parts[1:]
	} else if len(parts) == 6 && parts[0] == "api" && parts[1] == "mailboxes" && parts[5] == "messages" {
		values = parts[2:5]
	} else {
		return
	}
	for i := range values {
		values[i], e = url.PathUnescape(values[i])
		if e != nil {
			return
		}
	}
	platform, ok = platformFromSegment(values[0])
	account, recipient = values[1], recipientFromSegment(values[2])
	ok = ok && account != "" && recipient != ""
	return
}

func (a *App) requestLink(ctx context.Context, r *http.Request) (Link, error) {
	return a.requestLinkWith(ctx, a.store.db, r)
}

func (a *App) publicRecordWith(ctx context.Context, q mailQuery, where string, args ...any) (Record, error) {
	return a.store.scan(q.QueryRowContext(ctx, "SELECT "+columns+" FROM records WHERE "+where+" LIMIT 1", args...))
}

func (a *App) requestLinkWith(ctx context.Context, q mailQuery, r *http.Request) (Link, error) {
	var l Link
	var record Record
	var e error
	canonical := false
	requestedPlatform := ""
	if token := r.PathValue("token"); token != "" {
		if len(token) < 32 || len(token) > 128 {
			return l, ErrNotFound
		}
		record, e = a.publicRecordWith(ctx, q, "kind='link' AND token_hash=?", digest(token))
	} else {
		canonical = true
		platform, ok := platformFromSegment(r.PathValue("platform"))
		recipient := recipientFromSegment(r.PathValue("recipient"))
		if !ok || recipient == "" || r.PathValue("account") == "" {
			return l, ErrNotFound
		}
		requestedPlatform = platform
		account, err := a.publicRecordWith(ctx, q, "kind='account' AND (public_code=? OR id=?)", r.PathValue("account"), r.PathValue("account"))
		if err != nil {
			return l, err
		}
		if requestedPlatform != "" {
			// A platform segment is an explicit filter selected by the caller.
			// Unknown platform identifiers must not turn into an upstream error.
			if _, e = a.publicRecordWith(ctx, q, "kind='platform' AND id=?", requestedPlatform); e != nil {
				if errors.Is(e, ErrNotFound) {
					return l, ErrNotFound
				}
				return l, e
			}
		}
		where := "kind='link' AND account_id=? AND " + recipientPredicate(recipient) + " AND (platform_id='' OR platform_id=?)"
		rows, err := q.QueryContext(ctx, "SELECT "+columns+" FROM records WHERE "+where+" ORDER BY id LIMIT 3", account.ID, recipient, requestedPlatform)
		if err != nil {
			return l, err
		}
		matches := []Record{}
		for rows.Next() {
			v, err := a.store.scan(rows)
			if err != nil {
				rows.Close()
				return l, err
			}
			matches = append(matches, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return l, err
		}
		total := len(matches)
		if total == 0 || total > 2 {
			return l, ErrNotFound
		}
		record = matches[0]
		for _, match := range matches[1:] {
			// Removing a domain must never select another mailbox with the same
			// local part. Full-address legacy links remain available in that case.
			if !strings.EqualFold(match.Recipient, record.Recipient) {
				return l, ErrNotFound
			}
			if match.PlatformID == "" {
				record = match
			}
		}
	}
	if e != nil {
		return l, e
	}
	e = decode(record.Data, &l)
	if canonical && requestedPlatform != "" && l.PlatformID == "" {
		// readPublicLink uses PlatformID to apply the requested filter. The
		// stored record remains unrestricted, so all variants share one link.
		l.PlatformID = requestedPlatform
	}
	return l, e
}

func (a *App) publicRequestLookup(ctx context.Context, r *http.Request, limit int) (Link, []Message, error) {
	if !a.isReady() {
		return a.publicLookup(ctx, "", limit)
	}
	l, e := a.requestLink(ctx, r)
	if e != nil {
		return l, nil, e
	}
	q := r.URL.Query()
	if q.Get("ref") != "" || q.Get("id") != "" || q.Get("message_id") != "" {
		return a.readPublicMessage(ctx, l, q)
	}
	return a.readPublicLink(ctx, l, limit)
}
