package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	messages           []Message
	expires, timeAdded time.Time
	size               int
}
type flight struct {
	done     chan struct{}
	messages []Message
	err      error
}
type MailCache struct {
	mu         sync.Mutex
	entries    map[string]cacheEntry
	flights    map[string]*flight
	epochs     map[string]uint64
	generation uint64
	bytes      int
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	closed     bool
}

func newMailCache() *MailCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &MailCache{ctx: ctx, cancel: cancel, entries: map[string]cacheEntry{}, flights: map[string]*flight{}, epochs: map[string]uint64{}}
}
func (c *MailCache) Close() { c.mu.Lock(); c.closed = true; c.cancel(); c.mu.Unlock(); c.wg.Wait() }
func (c *MailCache) Invalidate(account string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if account == "" {
		c.generation++
		c.entries = map[string]cacheEntry{}
		c.bytes = 0
		return
	}
	c.epochs[account]++
	for k, v := range c.entries {
		if strings.HasPrefix(k, account+":") {
			c.bytes -= v.size
			delete(c.entries, k)
		}
	}
}
func (c *MailCache) Load(ctx context.Context, account, key string, load func(context.Context) ([]Message, error)) ([]Message, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, context.Canceled
	}
	if v, ok := c.entries[key]; ok && time.Now().Before(v.expires) {
		out := append([]Message{}, v.messages...)
		c.mu.Unlock()
		return out, nil
	}
	f := c.flights[key]
	if f == nil {
		if len(c.flights) >= 256 {
			c.mu.Unlock()
			return nil, errors.New("取信繁忙，请稍后重试")
		}
		f = &flight{done: make(chan struct{})}
		c.flights[key] = f
		epoch, gen := c.epochs[account], c.generation
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			work, cancel := context.WithTimeout(c.ctx, 45*time.Second)
			defer cancel()
			messages, e := safeLoad(work, load)
			size := 0
			if e == nil {
				b, _ := json.Marshal(messages)
				size = len(b)
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if epoch != c.epochs[account] || gen != c.generation {
				messages = nil
				e = ErrConflict
			}
			if e == nil && size <= 8<<20 {
				for len(c.entries) >= 256 || c.bytes+size > 64<<20 {
					oldKey := ""
					var oldest time.Time
					for k, v := range c.entries {
						if oldKey == "" || v.timeAdded.Before(oldest) {
							oldKey = k
							oldest = v.timeAdded
						}
					}
					if oldKey == "" {
						break
					}
					c.bytes -= c.entries[oldKey].size
					delete(c.entries, oldKey)
				}
				if old, ok := c.entries[key]; ok {
					c.bytes -= old.size
				}
				c.entries[key] = cacheEntry{messages: append([]Message{}, messages...), expires: time.Now().Add(8 * time.Second), timeAdded: time.Now(), size: size}
				c.bytes += size
			}
			f.messages, f.err = messages, e
			delete(c.flights, key)
			close(f.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		return append([]Message{}, f.messages...), f.err
	}
}
func safeLoad(ctx context.Context, load func(context.Context) ([]Message, error)) (messages []Message, err error) {
	defer func() {
		if recover() != nil {
			messages = nil
			err = errors.New("邮件读取内部错误")
		}
	}()
	return load(ctx)
}
