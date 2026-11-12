package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Counts are local to this node and independent of replicated link configuration.
type linkAccess struct {
	PageCount    uint64 `json:"page_count"`
	JSONCount    uint64 `json:"json_count"`
	Count        uint64 `json:"count"`
	LastAccessAt string `json:"last_access_at"`
}

type linkView struct {
	Link
	linkAccess
}

type requestStat struct {
	LinkID       string `json:"link_id"`
	Recipient    string `json:"recipient"`
	URL          string `json:"url"`
	JSONURL      string `json:"json_url"`
	AccountCode  string `json:"account_code"`
	AccountEmail string `json:"account_email"`
	PlatformName string `json:"platform_name"`
	linkAccess
}

func (a *App) recordAccess(r *http.Request) {
	if r.Method != http.MethodGet {
		return
	}
	page, api := 0, 0
	switch r.Pattern {
	case "GET /m/{token}", "GET /m/{platform}/{account}/{recipient}", "GET /show/{platform}/{account}/{recipient}":
		page = 1
	case "GET /api/mailboxes/{token}/messages", "GET /api/mailboxes/{platform}/{account}/{recipient}/messages":
		api = 1
	default:
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	link, e := a.requestLink(ctx, r)
	if e != nil {
		return
	}
	// A single atomic upsert prevents lost counts under concurrent requests.
	// Only existing links are recorded; raw tokens and unknown URLs are not stored.
	a.store.write.Lock()
	defer a.store.write.Unlock()
	_, e = a.store.db.ExecContext(ctx, `INSERT INTO link_access(link_id,page_count,json_count,last_access_at)
		SELECT id,?,?,? FROM records WHERE kind='link' AND id=?
		ON CONFLICT(link_id) DO UPDATE SET
		page_count=link_access.page_count+excluded.page_count,
		json_count=link_access.json_count+excluded.json_count,
		last_access_at=MAX(link_access.last_access_at,excluded.last_access_at)`,
		page, api, time.Now().UTC().Format(time.RFC3339Nano), link.ID)
	if e != nil {
		slog.Error("record mailbox access", "error", e)
	}
}

// Accept a recipient, a token, or either public URL in both admin search boxes.
func linkSearchToken(q string) string {
	if u, e := url.Parse(q); e == nil {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 && parts[0] == "m" {
			return parts[1]
		}
		if len(parts) == 4 && parts[0] == "api" && parts[1] == "mailboxes" && parts[3] == "messages" {
			return parts[2]
		}
	}
	return q
}

func (a *App) linkAccessCounts(ctx context.Context, records []Record) (map[string]linkAccess, error) {
	out := make(map[string]linkAccess, len(records))
	if len(records) == 0 {
		return out, nil
	}
	args := make([]any, len(records))
	for i, r := range records {
		args[i] = r.ID
	}
	rows, e := a.store.db.QueryContext(ctx, "SELECT link_id,page_count,json_count,last_access_at FROM link_access WHERE link_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+")", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var v linkAccess
		if e = rows.Scan(&id, &v.PageCount, &v.JSONCount, &v.LastAccessAt); e != nil {
			return nil, e
		}
		v.Count = v.PageCount + v.JSONCount
		out[id] = v
	}
	return out, rows.Err()
}

func (a *App) requestStats(w http.ResponseWriter, r *http.Request) {
	p, n := pagination(r)
	where, args := linkFilter(r)
	platforms, e := a.platformNames(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	var itemCount int
	if e := a.store.db.QueryRowContext(r.Context(), "SELECT count(*) FROM records WHERE "+where, args...).Scan(&itemCount); e != nil {
		fail(w, e)
		return
	}
	var total uint64
	if e := a.store.db.QueryRowContext(r.Context(), `SELECT COALESCE(sum(a.page_count+a.json_count),0)
		FROM link_access a JOIN records r ON r.kind='link' AND r.id=a.link_id`).Scan(&total); e != nil {
		fail(w, e)
		return
	}
	args = append(args, n, (p-1)*n)
	rows, e := a.store.db.QueryContext(r.Context(), `SELECT records.id,records.recipient,records.data,
		COALESCE(a.page_count,0),COALESCE(a.json_count,0),COALESCE(a.last_access_at,'')
		FROM records LEFT JOIN link_access a ON records.id=a.link_id WHERE `+where+`
		ORDER BY COALESCE(a.page_count,0)+COALESCE(a.json_count,0) DESC,records.id LIMIT ? OFFSET ?`, args...)
	if e != nil {
		fail(w, e)
		return
	}
	defer rows.Close()
	items := []requestStat{}
	base := a.baseURL(r)
	accounts := map[string]Account{}
	for rows.Next() {
		var v requestStat
		var encrypted string
		if e = rows.Scan(&v.LinkID, &v.Recipient, &encrypted, &v.PageCount, &v.JSONCount, &v.LastAccessAt); e != nil {
			fail(w, e)
			return
		}
		raw, err := unseal(a.key, encrypted)
		if err != nil {
			fail(w, err)
			return
		}
		var l Link
		if e = json.Unmarshal(raw, &l); e != nil {
			fail(w, e)
			return
		}
		v.PlatformName = platforms[l.PlatformID]
		if _, ok := accounts[l.AccountID]; !ok {
			account, err := a.store.Account(r.Context(), l.AccountID)
			if err != nil {
				fail(w, err)
				return
			}
			accounts[l.AccountID] = account
		}
		v.AccountEmail = accounts[l.AccountID].Email
		v.AccountCode = accounts[l.AccountID].PublicCode
		l.AccountCode = v.AccountCode
		v.URL, v.JSONURL = publicLinkURLs(base, l, v.PlatformName)
		v.Count = v.PageCount + v.JSONCount
		items = append(items, v)
	}
	if e = rows.Err(); e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, map[string]any{"items": items, "total": total, "item_count": itemCount, "page": p, "page_size": n})
}
