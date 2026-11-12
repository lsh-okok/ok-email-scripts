package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type MailBackend interface {
	List(context.Context, Account, []string, int) ([]Message, error)
	Detail(context.Context, Account, []string, MessageRef) (Message, error)
	Delete(context.Context, Account, []string, []MessageRef) ([]DeleteResult, error)
}
type recipientMailBackend interface {
	ListRecipient(context.Context, Account, []string, int, string) ([]Message, error)
}

// A backend may read the newest matching message and its body in one session.
type recipientMessageBackend interface {
	LatestRecipient(context.Context, Account, []string, string) ([]Message, error)
}
type orderedRecipientMailBackend interface {
	ListRecipientOrder(context.Context, Account, []string, int, string, string) ([]Message, error)
}
type orderedMailBackend interface {
	ListOrder(context.Context, Account, []string, int, string) ([]Message, error)
}
type MailService struct {
	app     *App
	backend MailBackend
	gate    chan struct{}
	locks   keyedLocks
}

func (m *MailService) permit(ctx context.Context) (func(), error) {
	t := time.NewTimer(3 * time.Second)
	defer t.Stop()
	select {
	case m.gate <- struct{}{}:
		return func() { <-m.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
		return nil, errors.New("取信繁忙，请稍后重试")
	}
}
func (m *MailService) List(ctx context.Context, a Account) ([]Message, error) {
	return m.ListOrder(ctx, a, "latest")
}
func (m *MailService) ListOrder(ctx context.Context, a Account, order string) ([]Message, error) {
	if a.Provider == "mx" {
		return m.app.mxSummaries(ctx, a, "", order, m.app.store.Settings(ctx).ScanLimit)
	}
	if a.Status != "active" {
		return nil, errors.New("邮箱已停用")
	}
	if order != "oldest" {
		order = "latest"
	}
	limit := m.app.store.Settings(ctx).ScanLimit
	key := fmt.Sprintf("%s:list:%d:%d:%s", a.ID, a.Generation, limit, order)
	return m.app.cache.Load(ctx, a.ID, key, func(work context.Context) ([]Message, error) {
		lock := m.locks.get(a.ID)
		if e := lock.Lock(work); e != nil {
			return nil, e
		}
		defer lock.Unlock()
		release, e := m.permit(work)
		if e != nil {
			return nil, e
		}
		defer release()
		proxies, e := m.app.effectiveProxies(work, a)
		if e != nil {
			return nil, e
		}
		var out []Message
		if backend, ok := m.backend.(orderedMailBackend); ok {
			out, e = backend.ListOrder(work, a, proxies, limit, order)
		} else {
			out, e = m.backend.List(work, a, proxies, limit)
		}
		if e != nil {
			return nil, e
		}
		if order == "oldest" {
			sort.SliceStable(out, func(i, j int) bool { return messageBefore(out[i], out[j]) })
		} else {
			sort.SliceStable(out, func(i, j int) bool { return messageAfter(out[i], out[j]) })
		}
		for i := range out {
			out[i].Ref = signRef(m.app.key, out[i].Source)
			out[i].MessageID = out[i].Source.ID
		}
		return out, nil
	})
}
func (m *MailService) ListRecipient(ctx context.Context, a Account, recipient string, limit int) ([]Message, error) {
	if a.Provider == "mx" {
		return m.app.mxSummaries(ctx, a, recipient, "latest", max(1, limit))
	}
	if a.Status != "active" {
		return nil, errors.New("邮箱已停用")
	}
	if limit < 1 {
		limit = 1
	}
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	key := fmt.Sprintf("%s:recipient:%d:%s:%d", a.ID, a.Generation, recipient, limit)
	return m.app.cache.Load(ctx, a.ID, key, func(work context.Context) ([]Message, error) {
		lock := m.locks.get(a.ID)
		if e := lock.Lock(work); e != nil {
			return nil, e
		}
		defer lock.Unlock()
		release, e := m.permit(work)
		if e != nil {
			return nil, e
		}
		defer release()
		proxies, e := m.app.effectiveProxies(work, a)
		if e != nil {
			return nil, e
		}
		var out []Message
		if backend, ok := m.backend.(recipientMailBackend); ok {
			out, e = backend.ListRecipient(work, a, proxies, limit, recipient)
		} else {
			out, e = m.backend.List(work, a, proxies, limit)
			if e == nil {
				filtered := out[:0]
				for _, v := range out {
					if matchesAccountRecipient(a, v, recipient) {
						filtered = append(filtered, v)
					}
				}
				out = filtered
			}
		}
		if e != nil {
			return nil, e
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].ReceivedAt.After(out[j].ReceivedAt) })
		if len(out) > limit {
			out = out[:limit]
		}
		for i := range out {
			out[i].Ref = signRef(m.app.key, out[i].Source)
			out[i].MessageID = out[i].Source.ID
		}
		return out, nil
	})
}

// Platform filtering must precede the result limit. Cache only the selected
// summaries, using the rule fingerprint so edits cannot reuse an older policy.
func (m *MailService) ListPlatformRecipient(ctx context.Context, a Account, recipient string, platform Platform, limit int) ([]Message, error) {
	if a.Status != "active" {
		return nil, ErrNotFound
	}
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	limit = max(1, min(publicPageSize, limit))
	key := fmt.Sprintf("%s:platform-recipient:%d:%s:%s:%d", a.ID, a.Generation, recipient, publicPlatformPolicy(&platform), limit)
	return m.app.cache.Load(ctx, a.ID, key, func(work context.Context) ([]Message, error) {
		items, err := m.ListRecipientSnapshot(work, a, recipient, "latest")
		if err != nil {
			return nil, err
		}
		out := []Message{}
		for _, item := range items {
			if platformMatches(item, platform) {
				out = append(out, item)
				if len(out) == limit {
					break
				}
			}
		}
		return out, nil
	})
}

func (m *MailService) LatestRecipient(ctx context.Context, a Account, recipient string) ([]Message, error) {
	if a.Provider == "mx" {
		return m.app.mxLinkMessages(ctx, a, Link{Recipient: recipient}, 1)
	}
	if a.Status != "active" {
		return nil, errors.New("邮箱已停用")
	}
	backend, combined := m.backend.(recipientMessageBackend)
	if !combined {
		values, err := m.ListRecipient(ctx, a, recipient, 1)
		if err != nil {
			return nil, err
		}
		for _, item := range values {
			if !matchesAccountRecipient(a, item, recipient) {
				continue
			}
			detail, err := m.Detail(ctx, a, item.Source)
			if err != nil {
				return nil, err
			}
			item.Text, item.HTML = detail.Text, detail.HTML
			return []Message{item}, nil
		}
		return []Message{}, nil
	}
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	key := fmt.Sprintf("%s:latest-recipient:%d:%s", a.ID, a.Generation, recipient)
	return m.app.cache.Load(ctx, a.ID, key, func(work context.Context) ([]Message, error) {
		lock := m.locks.get(a.ID)
		if err := lock.Lock(work); err != nil {
			return nil, err
		}
		defer lock.Unlock()
		release, err := m.permit(work)
		if err != nil {
			return nil, err
		}
		defer release()
		proxies, err := m.app.effectiveProxies(work, a)
		if err != nil {
			return nil, err
		}
		items, err := backend.LatestRecipient(work, a, proxies, recipient)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(items, func(i, j int) bool { return messageAfter(items[i], items[j]) })
		for _, item := range items {
			if item.Source.AccountID != a.ID || !matchesAccountRecipient(a, item, recipient) {
				continue
			}
			item.Ref = signRef(m.app.key, item.Source)
			item.MessageID = item.Source.ID
			item.HTML = sanitizeEmail(item.HTML)
			return []Message{item}, nil
		}
		return []Message{}, nil
	})
}

// ListRecipientSnapshot reads the complete recipient-matching message header
// set used to start a public pagination session.  The returned values contain
// metadata only; message bodies are loaded by Detail when a message is opened.
// A snapshot is intentionally taken once per cursor so newly-arrived messages
// cannot change the order of later pages.
func (m *MailService) ListRecipientSnapshot(ctx context.Context, a Account, recipient, order string) ([]Message, error) {
	if a.Provider == "mx" {
		return m.app.mxSummaries(ctx, a, recipient, order, 100000)
	}
	if a.Status != "active" {
		return nil, errors.New("邮箱已停用")
	}
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	lock := m.locks.get(a.ID)
	if e := lock.Lock(ctx); e != nil {
		return nil, e
	}
	defer lock.Unlock()
	release, e := m.permit(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	proxies, e := m.app.effectiveProxies(ctx, a)
	if e != nil {
		return nil, e
	}
	// A large bound lets Graph follow all @odata.nextLink pages while IMAP
	// still returns every matching UID.  It is only used to create a cursor;
	// normal mailbox scans retain their configured limits.
	const snapshotLimit = 100000
	var out []Message
	if backend, ok := m.backend.(orderedRecipientMailBackend); ok {
		out, e = backend.ListRecipientOrder(ctx, a, proxies, snapshotLimit, recipient, order)
	} else if backend, ok := m.backend.(recipientMailBackend); ok {
		out, e = backend.ListRecipient(ctx, a, proxies, snapshotLimit, recipient)
	} else {
		out, e = m.backend.List(ctx, a, proxies, snapshotLimit)
		if e == nil {
			filtered := out[:0]
			for _, v := range out {
				if matchesAccountRecipient(a, v, recipient) {
					filtered = append(filtered, v)
				}
			}
			out = filtered
		}
	}
	if e != nil {
		return nil, e
	}
	if order == "oldest" {
		sort.SliceStable(out, func(i, j int) bool { return messageBefore(out[i], out[j]) })
	} else {
		sort.SliceStable(out, func(i, j int) bool { return messageAfter(out[i], out[j]) })
	}
	seen := map[string]bool{}
	filtered := out[:0]
	for _, v := range out {
		if !matchesAccountRecipient(a, v, recipient) {
			continue
		}
		// Public pagination applies recipient and platform checks again before
		// exposing summaries. Keep forwarding headers until that final check.
		v.Text, v.HTML = "", ""
		v.Ref = signRef(m.app.key, v.Source)
		v.MessageID = v.Source.ID
		key := messageIdentity(v)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		filtered = append(filtered, v)
	}
	return filtered, nil
}

func messageIdentity(m Message) string {
	if m.Source.Protocol == "mx" {
		return "mx:" + m.Source.ID
	}
	if m.Source.Protocol == "graph" {
		return "graph:" + m.Source.ID
	}
	return fmt.Sprintf("imap:%s:%d:%d", m.Source.Mailbox, m.Source.UIDValidity, m.Source.UID)
}

func messageAfter(a, b Message) bool {
	if !a.ReceivedAt.Equal(b.ReceivedAt) {
		return a.ReceivedAt.After(b.ReceivedAt)
	}
	return messageTie(a) < messageTie(b)
}

func messageBefore(a, b Message) bool {
	if !a.ReceivedAt.Equal(b.ReceivedAt) {
		return a.ReceivedAt.Before(b.ReceivedAt)
	}
	return messageTie(a) < messageTie(b)
}

func messageTie(m Message) string {
	return m.Source.Protocol + ":" + m.Source.Folder + ":" + m.Source.ID + ":" + fmt.Sprint(m.Source.UID)
}
func (m *MailService) Detail(ctx context.Context, a Account, r MessageRef) (Message, error) {
	if a.Status != "active" {
		return Message{}, ErrNotFound
	}
	if a.Provider == "mx" {
		return m.app.mxMessage(ctx, a, r)
	}
	if r.Protocol == "mx" {
		return Message{}, ErrNotFound
	}
	key := fmt.Sprintf("%s:detail:%d:%s:%s:%d:%d", a.ID, a.Generation, r.Protocol, r.ID, r.UIDValidity, r.UID) + ":" + r.Mailbox
	values, e := m.app.cache.Load(ctx, a.ID, key, func(work context.Context) ([]Message, error) {
		lock := m.locks.get(a.ID)
		if e := lock.Lock(work); e != nil {
			return nil, e
		}
		defer lock.Unlock()
		release, e := m.permit(work)
		if e != nil {
			return nil, e
		}
		defer release()
		proxies, e := m.app.effectiveProxies(work, a)
		if e != nil {
			return nil, e
		}
		v, e := m.backend.Detail(work, a, proxies, r)
		if e != nil {
			return nil, e
		}
		v.Ref = signRef(m.app.key, r)
		v.MessageID = r.ID
		v.Source = r
		v.HTML = sanitizeEmail(v.HTML)
		return []Message{v}, nil
	})
	if e != nil {
		return Message{}, e
	}
	if len(values) != 1 {
		return Message{}, ErrNotFound
	}
	return values[0], nil
}
func (m *MailService) Delete(ctx context.Context, a Account, refs []string) ([]DeleteResult, error) {
	if m.app.cfg.Role != "primary" {
		return nil, errors.New("replica is read-only")
	}
	parsed := []MessageRef{}
	seen := map[string]bool{}
	for _, v := range refs {
		if seen[v] {
			continue
		}
		seen[v] = true
		r, e := parseRef(m.app.key, v, a.ID)
		if e != nil {
			return nil, e
		}
		if (a.Provider == "mx") != (r.Protocol == "mx") {
			return nil, ErrNotFound
		}
		parsed = append(parsed, r)
	}
	lock := m.locks.get(a.ID)
	if e := lock.Lock(ctx); e != nil {
		return nil, e
	}
	defer lock.Unlock()
	release, e := m.permit(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	var results []DeleteResult
	if a.Provider == "mx" {
		for _, ref := range parsed {
			seq, err := mxSourceID(ref)
			if err != nil {
				return nil, err
			}
			_, err = m.app.mx.db.ExecContext(ctx, "DELETE FROM mx_messages WHERE account_id=? AND seq=?", a.ID, seq)
			result := DeleteResult{Ref: signRef(m.app.key, ref), Success: err == nil}
			if err != nil {
				result.Error = "本地邮件删除失败"
			}
			results = append(results, result)
		}
	} else {
		for _, ref := range parsed {
			if ref.Protocol == "mx" {
				return nil, ErrNotFound
			}
		}
		proxies, err := m.app.effectiveProxies(ctx, a)
		if err != nil {
			return nil, err
		}
		results, e = m.backend.Delete(ctx, a, proxies, parsed)
	}
	// Even a timed-out remote mutation may have succeeded. Invalidate on every
	// attempt, including partial failures, and publish a new account generation.
	m.app.cache.Invalidate(a.ID)
	// Publish invalidation even when the client disconnected during deletion.
	work, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for retry := 0; retry < 5; retry++ {
		fresh, err := m.app.store.Account(work, a.ID)
		if err != nil {
			break
		}
		fresh.Generation++
		expected := fresh.Version
		if _, err = m.app.store.Save(work, fresh, &expected); err == nil {
			break
		} else if !errors.Is(err, ErrConflict) {
			break
		}
	}
	return results, e
}

type liveBackend struct {
	app                 *App
	imapDial            imapConnector
	imapDialHost        imapHostConnector
	imapHostPreferences imapReadHostPreferences
	latestPreferences   latestReadPreferences
}

func (b *liveBackend) List(ctx context.Context, a Account, p []string, n int) ([]Message, error) {
	return b.ListOrder(ctx, a, p, n, "latest")
}
func (b *liveBackend) ListOrder(ctx context.Context, a Account, p []string, n int, order string) ([]Message, error) {
	if a.Provider == "outlook" {
		v, e := b.graphListOrder(ctx, a, p, n, order)
		if e == nil {
			return v, nil
		}
		fallback, imapErr := b.imapListOrder(ctx, a, p, n, order)
		if imapErr != nil {
			return nil, fmt.Errorf("Graph 取信失败：%v；IMAP 取信失败：%w", e, imapErr)
		}
		return fallback, nil
	}
	return b.imapListOrder(ctx, a, p, n, order)
}
func (b *liveBackend) ListRecipient(ctx context.Context, a Account, p []string, n int, recipient string) ([]Message, error) {
	return b.ListRecipientOrder(ctx, a, p, n, recipient, "latest")
}
func (b *liveBackend) ListRecipientOrder(ctx context.Context, a Account, p []string, n int, recipient, order string) ([]Message, error) {
	if a.Provider == "outlook" {
		v, e := b.graphListRecipientOrder(ctx, a, p, n, recipient, order)
		if e == nil {
			return v, nil
		}
		fallback, imapErr := b.imapListRecipientOrder(ctx, a, p, n, recipient, order)
		if imapErr != nil {
			return nil, fmt.Errorf("Graph 收件人查询失败：%v；IMAP 查询失败：%w", e, imapErr)
		}
		return fallback, nil
	}
	return b.imapListRecipientOrder(ctx, a, p, n, recipient, order)
}
func (b *liveBackend) Detail(ctx context.Context, a Account, p []string, r MessageRef) (Message, error) {
	if r.Protocol == "graph" {
		return b.graphDetail(ctx, a, p, r)
	}
	return b.imapDetail(ctx, a, p, r)
}
func (b *liveBackend) Delete(ctx context.Context, a Account, p []string, refs []MessageRef) ([]DeleteResult, error) {
	var graph, imapRefs []MessageRef
	for _, r := range refs {
		if r.Protocol == "graph" {
			graph = append(graph, r)
		} else {
			imapRefs = append(imapRefs, r)
		}
	}
	out := []DeleteResult{}
	if len(graph) > 0 {
		v, e := b.graphDelete(ctx, a, p, graph)
		if e != nil {
			for _, r := range graph {
				out = append(out, DeleteResult{Ref: signRef(b.app.key, r), Error: e.Error()})
			}
		} else {
			out = append(out, v...)
		}
	}
	if len(imapRefs) > 0 {
		v, e := b.imapDelete(ctx, a, p, imapRefs)
		if e != nil {
			for _, r := range imapRefs {
				out = append(out, DeleteResult{Ref: signRef(b.app.key, r), Error: e.Error()})
			}
		} else {
			out = append(out, v...)
		}
	}
	return out, nil
}
