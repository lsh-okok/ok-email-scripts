package core

import (
	"sync"
	"time"
)

const (
	imapReadHostTTL        = 30 * time.Minute
	imapReadHostMaxEntries = 256
)

type imapReadHostKey struct {
	accountID  string
	generation int64
}

type imapReadHostPreference struct {
	host    string
	expires time.Time
}

// Remember successful read hosts briefly so repeated reads do not pay for an
// unavailable canonical host. Every read still falls back to the full allowlist.
type imapReadHostPreferences struct {
	mu      sync.Mutex
	entries map[imapReadHostKey]imapReadHostPreference
}

func (p *imapReadHostPreferences) hosts(account Account, now time.Time) []string {
	hosts := append([]string{}, imapHosts(account)...)
	if account.ID == "" || len(hosts) < 2 {
		return hosts
	}
	key := imapReadHostKey{accountID: account.ID, generation: account.Generation}
	p.mu.Lock()
	defer p.mu.Unlock()
	preference, exists := p.entries[key]
	if !exists {
		return hosts
	}
	if !now.Before(preference.expires) {
		delete(p.entries, key)
		return hosts
	}
	for index, host := range hosts {
		if host == preference.host {
			copy(hosts[1:index+1], hosts[:index])
			hosts[0] = host
			return hosts
		}
	}
	// Never use a host outside the provider's current allowlist.
	delete(p.entries, key)
	return hosts
}

func (p *imapReadHostPreferences) remember(account Account, host string, now time.Time) {
	hosts := imapHosts(account)
	if account.ID == "" || len(hosts) < 2 {
		return
	}
	allowed := false
	for _, candidate := range hosts {
		allowed = allowed || host == candidate
	}
	if !allowed {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = make(map[imapReadHostKey]imapReadHostPreference)
	}
	key := imapReadHostKey{accountID: account.ID, generation: account.Generation}
	for entryKey, entry := range p.entries {
		if !now.Before(entry.expires) {
			delete(p.entries, entryKey)
		}
	}
	if _, exists := p.entries[key]; !exists && len(p.entries) >= imapReadHostMaxEntries {
		var oldestKey imapReadHostKey
		var oldestExpiry time.Time
		for entryKey, entry := range p.entries {
			if oldestExpiry.IsZero() || entry.expires.Before(oldestExpiry) {
				oldestKey, oldestExpiry = entryKey, entry.expires
			}
		}
		delete(p.entries, oldestKey)
	}
	p.entries[key] = imapReadHostPreference{host: host, expires: now.Add(imapReadHostTTL)}
}

func (p *imapReadHostPreferences) forget(account Account, host string) {
	key := imapReadHostKey{accountID: account.ID, generation: account.Generation}
	p.mu.Lock()
	defer p.mu.Unlock()
	if preference, exists := p.entries[key]; exists && preference.host == host {
		delete(p.entries, key)
	}
}
