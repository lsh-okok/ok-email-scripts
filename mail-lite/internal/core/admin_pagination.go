package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	adminPageSize         = 20
	adminSessionMaxBytes  = 16 << 20
	adminSessionsMaxBytes = 64 << 20
)

var errAdminCursor = errors.New("邮件分页已失效，请刷新列表")

// The browser receives only a small authenticated session reference. In
// particular, IMAP UID snapshots and Microsoft nextLinks stay on the server.
type adminPageCursor struct {
	Kind       string    `json:"kind"`
	Session    string    `json:"session"`
	AccountID  string    `json:"account"`
	Generation int64     `json:"generation"`
	Order      string    `json:"order"`
	Folder     string    `json:"folder"`
	Offset     int       `json:"offset"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type adminMailPage struct {
	Items      []Message `json:"items"`
	Order      string    `json:"order"`
	Folder     string    `json:"folder"`
	NextCursor string    `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
	ScanLimit  int       `json:"scan_limit"`
}

type adminPageSource interface {
	next(context.Context, int) ([]Message, bool, error)
	clone() adminPageSource
}

type adminPagedBackend interface {
	startAdminPage(context.Context, Account, []string, string, string, time.Time) (adminPageSource, error)
}

type adminPageSession struct {
	mu         sync.Mutex
	id         string
	account    string
	generation int64
	order      string
	folder     string
	created    time.Time
	expires    time.Time
	source     adminPageSource
	items      []Message
	seen       map[string]bool
	done       bool
	bytes      int
}

type adminPageStore struct {
	mu       sync.Mutex
	sessions map[string]*adminPageSession
	costs    map[string]int
	bytes    int
}

func newAdminPageStore() *adminPageStore {
	return &adminPageStore{sessions: make(map[string]*adminPageSession), costs: make(map[string]int)}
}

func (s *adminPageStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]*adminPageSession)
	s.costs = make(map[string]int)
	s.bytes = 0
}

func (s *adminPageStore) get(id string) *adminPageSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, session := range s.sessions {
		if now.After(session.expires) {
			s.remove(key)
		}
	}
	return s.sessions[id]
}

func (s *adminPageStore) put(session *adminPageSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, current := range s.sessions {
		if now.After(current.expires) || current.account == session.account && current.generation != session.generation {
			s.remove(key)
		}
	}
	// Bound idle pagination sessions; opening a fresh list can always start a
	// new session after an old tab has expired or been evicted.
	s.remove(session.id)
	for len(s.sessions) >= 64 || s.bytes+session.bytes > adminSessionsMaxBytes {
		var oldest *adminPageSession
		for _, current := range s.sessions {
			if oldest == nil || current.created.Before(oldest.created) {
				oldest = current
			}
		}
		s.remove(oldest.id)
	}
	s.sessions[session.id] = session
	s.costs[session.id] = session.bytes
	s.bytes += session.bytes
}

func (s *adminPageStore) remove(id string) {
	s.bytes -= s.costs[id]
	delete(s.costs, id)
	delete(s.sessions, id)
}

func validAdminFolder(folder string) bool {
	return folder == "" || folder == "inbox" || folder == "junkemail"
}

func adminFolders(folder string) []string {
	if folder != "" {
		return []string{folder}
	}
	return []string{"inbox", "junkemail"}
}

func (a *App) adminMessagesPage(ctx context.Context, account Account, query url.Values) (adminMailPage, error) {
	if account.Provider == "mx" {
		return a.mxAdminPage(ctx, account, query)
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	var page adminMailPage
	if account.Status != "active" {
		return page, errors.New("邮箱已停用")
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit < 1 || limit > adminPageSize {
		limit = adminPageSize
	}
	order, folder := strings.TrimSpace(query.Get("order")), strings.TrimSpace(query.Get("folder"))
	var session *adminPageSession
	offset := 0
	if raw := strings.TrimSpace(query.Get("cursor")); raw != "" {
		if len(raw) > 4096 {
			return page, errAdminCursor
		}
		plain, err := unseal(a.key, raw)
		var cursor adminPageCursor
		if err != nil || json.Unmarshal(plain, &cursor) != nil || cursor.Kind != "admin-mail/v1" || cursor.AccountID != account.ID || cursor.Generation != account.Generation || cursor.Offset < 0 || time.Now().After(cursor.ExpiresAt) || cursor.ExpiresAt.Sub(cursor.CreatedAt) != publicCursorTTL {
			return page, errAdminCursor
		}
		if query.Has("order") && order != cursor.Order || query.Has("folder") && folder != cursor.Folder {
			return page, errAdminCursor
		}
		session = a.adminPages.get(cursor.Session)
		if session == nil || session.account != account.ID || session.generation != account.Generation || session.order != cursor.Order || session.folder != cursor.Folder || !session.created.Equal(cursor.CreatedAt) || !session.expires.Equal(cursor.ExpiresAt) {
			return page, errAdminCursor
		}
		order, folder, offset = cursor.Order, cursor.Folder, cursor.Offset
	} else {
		var err error
		order, err = validPublicOrder(order)
		if err != nil {
			return page, err
		}
		if !validAdminFolder(folder) {
			return page, errors.New("folder 只能是 inbox 或 junkemail")
		}
		now := time.Now().UTC()
		session = &adminPageSession{id: randomID(), account: account.ID, generation: account.Generation, order: order, folder: folder, created: now, expires: now.Add(publicCursorTTL), seen: make(map[string]bool)}
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if offset > len(session.items) {
		return page, errAdminCursor
	}
	if session.source == nil || !session.done && len(session.items) < offset+limit+1 {
		lock := a.mail.locks.get(account.ID)
		if err := lock.Lock(ctx); err != nil {
			return page, err
		}
		defer lock.Unlock()
		release, err := a.mail.permit(ctx)
		if err != nil {
			return page, err
		}
		defer release()
		proxies, err := a.effectiveProxies(ctx, account)
		if err != nil {
			return page, err
		}
		if session.source == nil {
			backend, ok := a.mail.backend.(adminPagedBackend)
			if !ok {
				return page, errors.New("邮箱服务不支持分页")
			}
			session.source, err = backend.startAdminPage(ctx, account, proxies, order, folder, session.created)
			if err != nil {
				return page, err
			}
		}
		if err := session.fill(ctx, a.key, offset+limit+1); err != nil {
			return page, err
		}
		fresh, err := a.store.Account(ctx, account.ID)
		if err != nil || fresh.Generation != account.Generation {
			return page, errAdminCursor
		}
	}
	end := min(offset+limit, len(session.items))
	if time.Now().After(session.expires) {
		return page, errAdminCursor
	}
	page = adminMailPage{Items: append([]Message{}, session.items[offset:end]...), Order: order, Folder: folder, HasMore: end < len(session.items) || !session.done, ScanLimit: adminPageSize}
	if page.HasMore {
		cursor := adminPageCursor{Kind: "admin-mail/v1", Session: session.id, AccountID: account.ID, Generation: account.Generation, Order: order, Folder: folder, Offset: end, CreatedAt: session.created, ExpiresAt: session.expires}
		plain, _ := json.Marshal(cursor)
		var err error
		page.NextCursor, err = seal(a.key, plain)
		if err != nil {
			return adminMailPage{}, err
		}
		a.adminPages.put(session)
	}
	return page, nil
}

// Work on a copy so a failed upstream request does not consume the browser's
// cursor. Cached preceding summaries also make retries of a successful cursor
// idempotent, even after a later page has already been requested.
func (s *adminPageSession) fill(ctx context.Context, key []byte, count int) error {
	source := s.source.clone()
	seen := make(map[string]bool, len(s.seen))
	for id := range s.seen {
		seen[id] = true
	}
	var pending []Message
	done := s.done
	for len(s.items)+len(pending) < count && !done {
		items, more, err := source.next(ctx, count-len(s.items)-len(pending))
		if err != nil {
			return err
		}
		if len(items) == 0 && more {
			return errors.New("邮件分页未返回有效进度，请重试")
		}
		for _, item := range items {
			id := messageIdentity(item)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			item.HTML, item.Text, item.Headers = "", "", nil
			item.Ref = signRef(key, item.Source)
			item.MessageID = item.Source.ID
			if item.MessageID == "" && item.Source.UID != 0 {
				item.MessageID = strconv.FormatUint(uint64(item.Source.UID), 10)
			}
			pending = append(pending, item)
		}
		done = !more
	}
	bytes := adminSourceBytes(source)
	for _, item := range s.items {
		bytes += adminMessageBytes(item)
	}
	for _, item := range pending {
		bytes += adminMessageBytes(item)
	}
	for id := range seen {
		bytes += len(id) + 64
	}
	if bytes > adminSessionMaxBytes {
		return errors.New("邮箱邮件列表过大，请选择单个文件夹后重试")
	}
	s.source, s.seen, s.done = source, seen, done
	s.items = append(s.items, pending...)
	s.bytes = bytes
	return nil
}

func adminMessageBytes(item Message) int {
	return 384 + len(item.Subject) + len(item.From) + len(item.To) + len(item.Preview) + len(item.Ref) + len(item.MessageID) + len(item.Source.ID) + len(item.Source.Mailbox)
}

func adminSourceBytes(source adminPageSource) int {
	switch s := source.(type) {
	case *adminIMAPSource:
		return len(s.entries) * 160
	case *adminGraphSource:
		size := 512
		for _, folder := range s.folders {
			for _, item := range folder.buffer {
				size += adminMessageBytes(item)
			}
			for target := range folder.visited {
				size += len(target) + 64
			}
		}
		return size
	}
	return 512
}

func (b *liveBackend) startAdminPage(ctx context.Context, account Account, proxies []string, order, folder string, snapshot time.Time) (adminPageSource, error) {
	if account.Provider == "outlook" {
		graph := newAdminGraphSource(b, account, proxies, order, folder, snapshot)
		// Probe only enough headers for the first page; a failed initial Graph
		// query can use IMAP, while an established cursor keeps its protocol.
		if err := graph.prime(ctx); err == nil {
			return graph, nil
		} else {
			imap, imapErr := b.startAdminIMAPPage(ctx, account, proxies, order, folder)
			if imapErr != nil {
				return nil, errors.New("Graph 取信失败：" + err.Error() + "；IMAP 取信失败：" + imapErr.Error())
			}
			return imap, nil
		}
	}
	return b.startAdminIMAPPage(ctx, account, proxies, order, folder)
}
