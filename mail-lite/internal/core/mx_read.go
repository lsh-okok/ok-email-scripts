package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (a *App) mxReadLocal(ctx context.Context, account Account, q mxReadRequest) (mxReadResponse, error) {
	empty := mxReadResponse{Items: []mxWireMessage{}}
	fresh, err := a.store.Account(ctx, account.ID)
	if err != nil {
		return empty, err
	}
	if fresh.Provider != "mx" || fresh.Status != "active" {
		return empty, ErrNotFound
	}
	if q.Recipient != "" && !mxRecipient(fresh, q.Recipient) {
		return empty, ErrNotFound
	}
	var platform *Platform
	if q.PlatformID != "" {
		record, err := a.store.Get(ctx, "platform", q.PlatformID)
		if err != nil {
			return empty, err
		}
		platform = &Platform{}
		if err = json.Unmarshal(record.Data, platform); err != nil {
			return empty, err
		}
	}
	if q.MessageID != "" {
		seq, err := strconv.ParseInt(q.MessageID, 10, 64)
		if err != nil || seq <= 0 {
			return empty, ErrNotFound
		}
		m, err := a.mx.load(ctx, fresh, seq)
		if err != nil {
			return empty, err
		}
		if q.Recipient != "" && !matchesAccountRecipient(fresh, m, q.Recipient) || platform != nil && !platformMatches(m, *platform) {
			return empty, ErrNotFound
		}
		empty.Items = append(empty.Items, mxWireMessage{m, m.Source, m.EnvelopeRecipients})
		return empty, nil
	}
	return a.mx.page(ctx, fresh, q, platform)
}
func (a *App) mxRead(ctx context.Context, account Account, q mxReadRequest) (mxReadResponse, error) {
	if account.Provider != "mx" || account.Status != "active" {
		return mxReadResponse{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	q.AccountID = account.ID
	if q.Limit < 1 || q.Limit > 20 {
		q.Limit = 20
	}
	if q.Order != "oldest" {
		q.Order = "latest"
	}
	if a.cfg.Role == "primary" {
		return a.mxReadLocal(ctx, account, q)
	}
	var out mxReadResponse
	if err := a.clusterCall(ctx, "POST", "/internal/mx/read", q, &out); err != nil {
		if strings.Contains(err.Error(), "（404）") {
			return out, ErrNotFound
		}
		return out, errors.New("MX 主节点未就绪，暂时无法取件")
	}
	return out, nil
}
func (a *App) internalMXRead(w http.ResponseWriter, r *http.Request) {
	var q mxReadRequest
	if !readJSON(w, r, &q) {
		return
	}
	if q.Limit < 1 || q.Limit > 20 || q.Position.After < 0 || q.Position.Upper < 0 || len(q.MessageID) > 20 || len(q.Recipient) > 320 {
		respond(w, 400, map[string]string{"error": "invalid MX query"})
		return
	}
	account, err := a.store.Account(r.Context(), q.AccountID)
	if err != nil {
		fail(w, err)
		return
	}
	out, err := a.mxReadLocal(r.Context(), account, q)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, out)
}
func (a *App) mxMessage(ctx context.Context, account Account, ref MessageRef) (Message, error) {
	if ref.AccountID != account.ID {
		return Message{}, ErrNotFound
	}
	if _, err := mxSourceID(ref); err != nil {
		return Message{}, err
	}
	out, err := a.mxRead(ctx, account, mxReadRequest{MessageID: ref.ID, Limit: 1})
	if err != nil {
		return Message{}, err
	}
	if len(out.Items) != 1 {
		return Message{}, ErrNotFound
	}
	m := out.Items[0].Message
	m.Source = out.Items[0].Source
	m.EnvelopeRecipients = out.Items[0].Recipients
	m.Ref = signRef(a.key, m.Source)
	m.MessageID = m.Source.ID
	m.HTML = sanitizeEmail(m.HTML)
	return m, nil
}

type mxPageCursor struct {
	Kind       string     `json:"kind"`
	AccountID  string     `json:"account"`
	Generation int64      `json:"generation"`
	LinkID     string     `json:"link"`
	Recipient  string     `json:"recipient"`
	PlatformID string     `json:"platform"`
	Folder     string     `json:"folder"`
	Order      string     `json:"order"`
	Position   mxPosition `json:"position"`
	Expires    time.Time  `json:"expires"`
}

func (a *App) mxPage(ctx context.Context, account Account, link Link, q url.Values, limit int, admin bool) (mxReadResponse, mxPageCursor, string, error) {
	cursor := mxPageCursor{Kind: "mx-public/v1", AccountID: account.ID, Generation: account.Generation, LinkID: link.ID, Recipient: link.Recipient, PlatformID: link.PlatformID, Folder: q.Get("folder"), Order: q.Get("order"), Expires: time.Now().Add(publicCursorTTL)}
	if admin {
		cursor.Kind = "mx-admin/v1"
		cursor.LinkID = ""
		cursor.Recipient = ""
		cursor.PlatformID = ""
	}
	empty := mxReadResponse{Items: []mxWireMessage{}}
	if account.Status != "active" {
		return empty, cursor, "", ErrNotFound
	}
	if raw := q.Get("cursor"); raw != "" {
		var saved mxPageCursor
		plain, err := unseal(a.key, raw)
		if len(raw) > 4096 || err != nil || json.Unmarshal(plain, &saved) != nil || saved.Kind != cursor.Kind || saved.AccountID != account.ID || saved.Generation != account.Generation || saved.LinkID != cursor.LinkID || saved.Recipient != cursor.Recipient || saved.PlatformID != cursor.PlatformID || time.Now().After(saved.Expires) || saved.Expires.After(time.Now().Add(publicCursorTTL+time.Minute)) || saved.Position.Upper <= 0 || saved.Position.After <= 0 {
			return empty, cursor, "", errPublicCursor
		}
		if q.Has("order") && q.Get("order") != saved.Order || q.Has("folder") && q.Get("folder") != saved.Folder {
			return empty, cursor, "", errPublicCursor
		}
		cursor = saved
	}
	var err error
	cursor.Order, err = validPublicOrder(cursor.Order)
	if err != nil {
		return empty, cursor, "", err
	}
	if !validAdminFolder(cursor.Folder) {
		return empty, cursor, "", errors.New("folder 只能是 inbox 或 junkemail")
	}
	if cursor.Folder == "junkemail" {
		return empty, cursor, "", nil
	}
	out, err := a.mxRead(ctx, account, mxReadRequest{Recipient: cursor.Recipient, PlatformID: cursor.PlatformID, Order: cursor.Order, Limit: limit, Position: cursor.Position})
	if err != nil {
		return empty, cursor, "", err
	}
	cursor.Position = out.Position
	next := ""
	if out.HasMore {
		plain, _ := json.Marshal(cursor)
		next, err = seal(a.key, plain)
	}
	return out, cursor, next, err
}
func (a *App) mxPublicPage(ctx context.Context, account Account, link Link, q url.Values, limit int, order string) (Link, publicPage, error) {
	if q.Get("recent") == "1" {
		limit = 20
		order = "latest"
	}
	copyQuery := url.Values{}
	for k, v := range q {
		copyQuery[k] = append([]string{}, v...)
	}
	if q.Get("cursor") == "" {
		copyQuery.Set("order", order)
	}
	out, cursor, next, err := a.mxPage(ctx, account, link, copyQuery, limit, false)
	if err != nil {
		return link, publicPage{}, err
	}
	page := publicPage{Recipient: link.Recipient, Order: cursor.Order, Items: []Message{}, NextCursor: next, HasMore: out.HasMore}
	for _, wire := range out.Items {
		m := wire.Message
		m.Source = wire.Source
		m.EnvelopeRecipients = wire.Recipients
		m.Ref = signRef(a.key, m.Source)
		m.MessageID = m.Source.ID
		page.Items = append(page.Items, m)
	}
	if q.Get("details") == "1" {
		if err = a.publicPageDetails(ctx, account, link, page.Items); err != nil {
			return link, publicPage{}, err
		}
	}
	return link, page, nil
}
func (a *App) mxAdminPage(ctx context.Context, account Account, q url.Values) (adminMailPage, error) {
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 20 {
		limit = 20
	}
	out, cursor, next, err := a.mxPage(ctx, account, Link{}, q, limit, true)
	if err != nil {
		return adminMailPage{}, err
	}
	page := adminMailPage{Items: []Message{}, Order: cursor.Order, Folder: cursor.Folder, HasMore: out.HasMore, NextCursor: next, ScanLimit: 20}
	for _, wire := range out.Items {
		m := wire.Message
		m.Source = wire.Source
		m.Ref = signRef(a.key, m.Source)
		m.MessageID = m.Source.ID
		page.Items = append(page.Items, m)
	}
	return page, nil
}
func (a *App) mxLinkMessages(ctx context.Context, account Account, l Link, limit int) ([]Message, error) {
	out, err := a.mxRead(ctx, account, mxReadRequest{Recipient: l.Recipient, PlatformID: l.PlatformID, Limit: limit, Order: "latest"})
	if err != nil {
		return nil, err
	}
	items := []Message{}
	for _, wire := range out.Items {
		m, err := a.mxMessage(ctx, account, wire.Source)
		if err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, nil
}

// Shared service callers also use the local database, never IMAP or the upstream cache.
func (a *App) mxSummaries(ctx context.Context, account Account, recipient, order string, limit int) ([]Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	items := []Message{}
	position := mxPosition{}
	for len(items) < limit {
		out, err := a.mxRead(ctx, account, mxReadRequest{Recipient: recipient, Order: order, Limit: min(20, limit-len(items)), Position: position})
		if err != nil {
			return nil, err
		}
		for _, wire := range out.Items {
			m := wire.Message
			m.Source = wire.Source
			m.EnvelopeRecipients = wire.Recipients
			m.Ref = signRef(a.key, m.Source)
			m.MessageID = m.Source.ID
			items = append(items, m)
		}
		if !out.HasMore {
			break
		}
		position = out.Position
	}
	return items, nil
}

type mxOverview struct {
	Domain        string `json:"domain"`
	RetentionDays int    `json:"retention_days"`
	SMTPEnabled   bool   `json:"smtp_enabled"`
	SMTPRunning   bool   `json:"smtp_running"`
	Hostname      string `json:"hostname"`
	Listen        string `json:"listen"`
	STARTTLS      bool   `json:"starttls"`
	Messages      int    `json:"messages"`
	LastReceived  string `json:"last_received"`
}

func (a *App) mxAccountOverview(w http.ResponseWriter, r *http.Request) {
	account, err := a.store.Account(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if account.Provider != "mx" {
		fail(w, ErrNotFound)
		return
	}
	out := mxOverview{Domain: account.MXDomain, RetentionDays: mxDays(account), SMTPEnabled: a.cfg.SMTPEnabled, SMTPRunning: a.smtpRunning.Load(), Hostname: a.cfg.SMTPHostname, Listen: a.cfg.SMTPListen, STARTTLS: a.cfg.SMTPTLSCert != ""}
	var last sql.NullInt64
	if err = a.mx.db.QueryRowContext(r.Context(), "SELECT count(*),max(received) FROM mx_messages WHERE account_id=? AND received>=?", account.ID, mxCutoff(account)).Scan(&out.Messages, &last); err != nil {
		fail(w, err)
		return
	}
	if last.Valid {
		out.LastReceived = time.UnixMicro(last.Int64).UTC().Format(time.RFC3339)
	}
	respond(w, 200, out)
}
