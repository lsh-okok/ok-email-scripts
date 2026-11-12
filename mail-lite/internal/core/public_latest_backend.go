package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Remember a working fallback briefly, so fresh reads do not repeatedly wait
// for an unavailable Graph endpoint. Results still use the normal 8s cache.
const latestReadPreferenceTTL = 15 * time.Minute

type latestReadPreferences struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func latestReadKey(account Account, proxies []string) string {
	return fmt.Sprintf("%s:%d:%d:%s", account.ID, account.Generation, account.Version, digest(strings.Join(proxies, "\x00")))
}

func (p *latestReadPreferences) preferIMAP(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	until, exists := p.entries[key]
	if exists && !time.Now().Before(until) {
		delete(p.entries, key)
		return false
	}
	return exists
}

func (p *latestReadPreferences) rememberIMAP(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = map[string]time.Time{}
	}
	if len(p.entries) >= 256 {
		oldKey := ""
		var oldest time.Time
		for key, until := range p.entries {
			if oldKey == "" || until.Before(oldest) {
				oldKey, oldest = key, until
			}
		}
		delete(p.entries, oldKey)
	}
	p.entries[key] = time.Now().Add(latestReadPreferenceTTL)
}

func (p *latestReadPreferences) forget(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.entries, key)
}

func (b *liveBackend) LatestRecipient(ctx context.Context, account Account, proxies []string, recipient string) ([]Message, error) {
	if account.Provider != "outlook" {
		return b.imapLatestRecipient(ctx, account, proxies, recipient)
	}
	key := latestReadKey(account, proxies)
	if b.latestPreferences.preferIMAP(key) {
		items, imapErr := b.imapLatestRecipient(ctx, account, proxies, recipient)
		if imapErr == nil {
			return items, nil
		}
		b.latestPreferences.forget(key)
		items, graphErr := b.graphLatestRecipient(ctx, account, proxies, recipient)
		if graphErr == nil {
			return items, nil
		}
		return nil, fmt.Errorf("Graph 收件人查询失败：%v；IMAP 查询失败：%w", graphErr, imapErr)
	}
	items, graphErr := b.graphLatestRecipient(ctx, account, proxies, recipient)
	if graphErr == nil {
		return items, nil
	}
	items, imapErr := b.imapLatestRecipient(ctx, account, proxies, recipient)
	if imapErr != nil {
		return nil, fmt.Errorf("Graph 收件人查询失败：%v；IMAP 查询失败：%w", graphErr, imapErr)
	}
	b.latestPreferences.rememberIMAP(key)
	return items, nil
}

func (b *liveBackend) graphLatestRecipient(ctx context.Context, account Account, proxies []string, recipient string) ([]Message, error) {
	items, err := b.graphListRecipient(ctx, account, proxies, 1, recipient)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool { return messageAfter(items[i], items[j]) })
	for _, item := range items {
		if !matchesAccountRecipient(account, item, recipient) {
			continue
		}
		detail, err := b.graphDetail(ctx, account, proxies, item.Source)
		if err != nil {
			return nil, err
		}
		if !matchesAccountRecipient(account, detail, recipient) {
			return []Message{}, nil
		}
		item.Text, item.HTML = detail.Text, detail.HTML
		return []Message{item}, nil
	}
	return []Message{}, nil
}
