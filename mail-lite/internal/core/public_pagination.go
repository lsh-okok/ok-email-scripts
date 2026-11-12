package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	publicPageSize  = 20
	publicCursorTTL = 30 * time.Minute
)

var errPublicCursor = errors.New("分页游标无效或已过期，请刷新列表")

type publicCursorItem struct {
	Message Message    `json:"message"`
	Source  MessageRef `json:"source"`
}

type publicCursor struct {
	Version    int       `json:"v"`
	Session    string    `json:"session"`
	Offset     int       `json:"offset"`
	LinkID     string    `json:"link_id"`
	AccountID  string    `json:"account_id"`
	Recipient  string    `json:"recipient"`
	PlatformID string    `json:"platform_id"`
	PolicyKey  string    `json:"policy_key"`
	Generation int64     `json:"generation"`
	Order      string    `json:"order"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type publicPage struct {
	Recipient  string
	Order      string
	Items      []Message
	NextCursor string
	HasMore    bool
}

func clampPublicLimit(raw string) int {
	n, _ := strconv.Atoi(raw)
	if n < 1 {
		n = publicPageSize
	}
	if n > publicPageSize {
		n = publicPageSize
	}
	return n
}

func validPublicOrder(raw string) (string, error) {
	if raw == "" {
		return "latest", nil
	}
	if raw != "latest" && raw != "oldest" {
		return "", errors.New("order 只能是 latest 或 oldest")
	}
	return raw, nil
}

func (a *App) publicPageRequest(ctx context.Context, r *http.Request, limit int, order string) (Link, publicPage, error) {
	var page publicPage
	if !a.isReady() {
		return Link{}, page, errors.New("节点同步未就绪，请稍后重试")
	}
	l, e := a.requestLink(ctx, r)
	if e != nil {
		return l, page, e
	}
	q := r.URL.Query()
	accountForMX, _, mxErr := a.publicLinkContext(ctx, l)
	if mxErr != nil {
		return l, page, mxErr
	}
	if accountForMX.Provider == "mx" {
		return a.mxPublicPage(ctx, accountForMX, l, q, limit, order)
	}
	if q.Get("ref") != "" || q.Get("id") != "" || q.Get("message_id") != "" {
		return l, page, errors.New("single message query")
	}
	if raw := strings.TrimSpace(q.Get("cursor")); raw != "" {
		return a.publicCursorPage(ctx, l, raw, limit, q.Get("details") == "1")
	}
	if q.Get("recent") == "1" {
		order = "latest"
		limit = publicPageSize
	}
	if order == "" {
		order = "latest"
	}
	order, e = validPublicOrder(order)
	if e != nil {
		return l, page, e
	}
	account, platform, e := a.publicLinkContext(ctx, l)
	if e != nil {
		return l, page, e
	}
	values, e := a.mail.ListRecipientSnapshot(ctx, account, l.Recipient, order)
	if e != nil {
		return l, page, e
	}
	// Upstream reads may finish after an account or platform edit. Never
	// publish an old-policy snapshot after such a change has taken effect.
	fresh, freshPlatform, e := a.publicLinkContext(ctx, l)
	if e != nil {
		return l, page, e
	}
	if fresh.Generation != account.Generation || publicPlatformPolicy(freshPlatform) != publicPlatformPolicy(platform) {
		return l, page, errPublicCursor
	}
	filtered := values[:0]
	for _, m := range values {
		if matchesAccountRecipient(account, m, l.Recipient) && (platform == nil || platformMatches(m, *platform)) {
			m.Text, m.HTML, m.Headers = "", "", nil
			filtered = append(filtered, m)
		}
	}
	page = publicPage{Recipient: l.Recipient, Order: order, HasMore: len(filtered) > limit}
	items := filtered
	if len(items) > limit {
		items = items[:limit]
	}
	page.Items = items
	if page.HasMore {
		now := time.Now().UTC()
		c := publicCursor{Version: 2, Session: randomID(), LinkID: l.ID, AccountID: l.AccountID, Recipient: l.Recipient, PlatformID: l.PlatformID, PolicyKey: publicPlatformPolicy(platform), Generation: account.Generation, Order: order, CreatedAt: now, ExpiresAt: now.Add(publicCursorTTL)}
		snapshot := &publicPageSession{cursor: c, items: make([]publicCursorItem, 0, len(filtered))}
		for _, m := range filtered {
			snapshot.items = append(snapshot.items, publicCursorItem{Message: m, Source: m.Source})
		}
		if e = a.publicPages.put(snapshot); e != nil {
			return l, publicPage{}, e
		}
		c.Offset = limit
		page.NextCursor, e = a.sealPublicCursor(c)
		if e != nil {
			return l, publicPage{}, e
		}
	}
	if q.Get("details") == "1" {
		if e = a.publicPageDetails(ctx, account, l, page.Items); e != nil {
			return l, publicPage{}, e
		}
	}
	return l, page, nil
}

func (a *App) publicCursorPage(ctx context.Context, l Link, raw string, limit int, details bool) (Link, publicPage, error) {
	var page publicPage
	if len(raw) > 4096 {
		return l, page, errPublicCursor
	}
	b, e := unseal(a.key, raw)
	if e != nil {
		return l, page, errPublicCursor
	}
	var c publicCursor
	if e = json.Unmarshal(b, &c); e != nil || c.Version != 2 || c.Session == "" || c.Offset <= 0 || c.LinkID != l.ID || c.AccountID != l.AccountID || !strings.EqualFold(c.Recipient, l.Recipient) || c.PlatformID != l.PlatformID || time.Now().UTC().After(c.ExpiresAt) || c.ExpiresAt.Sub(c.CreatedAt) != publicCursorTTL {
		return l, page, errPublicCursor
	}
	account, platform, e := a.publicLinkContext(ctx, l)
	if e != nil {
		return l, page, e
	}
	if c.Generation != account.Generation || c.PolicyKey != publicPlatformPolicy(platform) || (c.Order != "latest" && c.Order != "oldest") {
		return l, page, errPublicCursor
	}
	snapshot := a.publicPages.get(c.Session)
	if snapshot == nil || !snapshot.matches(c) || c.Offset >= len(snapshot.items) {
		return l, page, errPublicCursor
	}
	end := min(c.Offset+limit, len(snapshot.items))
	page = publicPage{Recipient: l.Recipient, Order: c.Order, HasMore: end < len(snapshot.items)}
	for _, item := range snapshot.items[c.Offset:end] {
		m := item.Message
		m.Source = item.Source
		page.Items = append(page.Items, m)
	}
	if page.HasMore {
		c.Offset = end
		page.NextCursor, e = a.sealPublicCursor(c)
		if e != nil {
			return l, publicPage{}, e
		}
	}
	if details {
		if e = a.publicPageDetails(ctx, account, l, page.Items); e != nil {
			return l, publicPage{}, e
		}
	}
	return l, page, nil
}

func (a *App) sealPublicCursor(c publicCursor) (string, error) {
	b, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	return seal(a.key, b)
}

func (a *App) publicPageDetails(ctx context.Context, account Account, l Link, items []Message) error {
	for i := range items {
		detail, e := a.mail.Detail(ctx, account, items[i].Source)
		if e != nil {
			return e
		}
		fresh, platform, e := a.publicLinkContext(ctx, l)
		if e != nil {
			return e
		}
		if fresh.Generation != account.Generation || !matchesAccountRecipient(fresh, detail, l.Recipient) || platform != nil && !platformMatches(detail, *platform) {
			return ErrNotFound
		}
		items[i].Text, items[i].HTML = detail.Text, detail.HTML
	}
	return nil
}

// Display names do not affect authorization. Bind cursors to the sender and
// subject rules so a previously filtered snapshot cannot outlive a policy edit.
func publicPlatformPolicy(platform *Platform) string {
	if platform == nil {
		return ""
	}
	rules, _ := json.Marshal([][]string{platform.Emails, platform.Domains, platform.Keywords})
	return digest(string(rules))
}
