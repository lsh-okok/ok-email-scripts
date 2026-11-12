package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type AccessToken struct {
	Token     string    `json:"access_token"`
	ExpiresAt time.Time `json:"expires_at"`
	Scope     string    `json:"scope,omitempty"`
}
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}
type keyedLock struct{ gate chan struct{} }

func (k *keyedLock) Lock(ctx context.Context) error {
	select {
	case k.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (k *keyedLock) Unlock() { <-k.gate }

func (k *keyedLocks) get(id string) *keyedLock {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.locks == nil {
		k.locks = map[string]*keyedLock{}
	}
	l := k.locks[id]
	if l == nil {
		l = &keyedLock{gate: make(chan struct{}, 1)}
		k.locks[id] = l
	}
	return l
}

type TokenManager struct {
	app    *App
	mu     sync.Mutex
	values map[string]AccessToken
	epochs map[string]uint64
	locks  keyedLocks
}

func (t *TokenManager) Invalidate(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.epochs == nil {
		t.epochs = map[string]uint64{}
	}
	t.epochs[id]++
	for k := range t.values {
		if strings.HasPrefix(k, id+":") {
			delete(t.values, k)
		}
	}
}
func (t *TokenManager) Get(ctx context.Context, id, kind string, force bool) (AccessToken, error) {
	lock := t.locks.get(id)
	if e := lock.Lock(ctx); e != nil {
		return AccessToken{}, e
	}
	defer lock.Unlock()
	key := id + ":" + kind
	t.mu.Lock()
	cached := t.values[key]
	epoch := t.epochs[id]
	t.mu.Unlock()
	if !force && time.Until(cached.ExpiresAt) > 2*time.Minute {
		return cached, nil
	}
	var out AccessToken
	if t.app.cfg.Role == "replica" {
		if kind == "write" {
			return out, errors.New("replica is read-only")
		}
		e := t.app.clusterCall(ctx, "POST", "/internal/tokens", map[string]string{"account_id": id, "kind": kind}, &out)
		if e != nil {
			return out, e
		}
	} else {
		a, e := t.app.store.Account(ctx, id)
		if e != nil {
			return out, e
		}
		if a.Provider != "outlook" || a.RefreshToken == "" {
			return out, errors.New("Outlook credentials are missing")
		}
		proxies, e := t.app.effectiveProxies(ctx, a)
		if e != nil {
			return out, e
		}
		payload, e := t.request(ctx, a, proxies, kind, force)
		if e != nil {
			return out, e
		}
		if payload.Expires < 60 {
			return out, errors.New("invalid access token lifetime")
		}
		// Ordinary reads only cache access tokens, matching the original project.
		// Persist rotation only for an explicit manual or scheduled refresh.
		// The version check protects credentials changed while Microsoft replied.
		if force && payload.Refresh != "" && payload.Refresh != a.RefreshToken {
			a.RefreshToken = payload.Refresh
			expected := a.Version
			if _, e = t.app.store.Save(ctx, a, &expected); e != nil {
				return out, e
			}
		}
		out = AccessToken{Token: payload.Token, ExpiresAt: time.Now().Add(time.Duration(payload.Expires) * time.Second), Scope: payload.Scope}
	}
	t.mu.Lock()
	if t.epochs[id] != epoch {
		t.mu.Unlock()
		return AccessToken{}, ErrConflict
	}
	if t.values == nil {
		t.values = map[string]AccessToken{}
	}
	t.values[key] = out
	t.mu.Unlock()
	return out, nil
}

func (t *TokenManager) request(ctx context.Context, a Account, proxies []string, kind string, originalScope bool) (tokenResponse, error) {
	var payload tokenResponse
	scopes := tokenScopes(kind, originalScope)
	for i, scope := range scopes {
		form := url.Values{"client_id": {a.ClientID}, "refresh_token": {a.RefreshToken}, "grant_type": {"refresh_token"}}
		if scope != "" {
			form.Set("scope", scope)
		}
		status := 0
		var last error
		for _, p := range proxies {
			tr := transport(p)
			client := &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: noRedirect}
			req, e := http.NewRequestWithContext(ctx, "POST", t.app.cfg.tokenEndpoint(kind), strings.NewReader(form.Encode()))
			if e != nil {
				tr.CloseIdleConnections()
				return payload, errors.New("Microsoft 授权接口地址无效")
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			res, e := client.Do(req)
			if e != nil {
				tr.CloseIdleConnections()
				if ctx.Err() != nil {
					return payload, ctx.Err()
				}
				last = tokenConnectionError(e, p)
				continue
			}
			b, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
			res.Body.Close()
			tr.CloseIdleConnections()
			if e != nil || len(b) > 1<<20 {
				return payload, errors.New("Microsoft 授权响应中断或超过限制，请重试")
			}
			payload = tokenResponse{}
			if json.Unmarshal(b, &payload) != nil {
				return payload, errors.New("Microsoft 授权接口未返回有效 JSON，请检查代理或网络拦截")
			}
			status, last = res.StatusCode, nil
			break
		}
		if last != nil {
			return payload, last
		}
		if status == http.StatusOK && payload.Token != "" {
			return payload, nil
		}
		if i == len(scopes)-1 || !tokenScopeRetryable(status, payload) {
			return payload, tokenResponseError(status, payload, kind)
		}
	}
	return payload, errors.New("Microsoft 授权请求失败")
}
func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
