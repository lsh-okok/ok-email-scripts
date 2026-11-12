package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mail-lite/web"
)

func testApp(t *testing.T, role string) *App {
	t.Helper()
	a, e := NewApp(Config{Role: role, DataDir: t.TempDir(), AdminPassword: "test-password-12345"}, web.Assets)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	return a
}
func accountFixture(t *testing.T, a *App, id string) Account {
	t.Helper()
	v := Account{ID: id, Email: id + "@outlook.com", Provider: "outlook", Status: "active", ClientID: "client", RefreshToken: "secret-refresh"}
	if _, e := a.store.Save(context.Background(), v, nil); e != nil {
		t.Fatal(e)
	}
	v, _ = a.store.Account(context.Background(), id)
	return v
}
func linkFixture(t *testing.T, a *App, account, recipient string) Link {
	t.Helper()
	token := randomToken()
	v := Link{ID: randomID(), AccountID: account, Recipient: recipient, Token: token, TokenHash: digest(token)}
	if _, e := a.store.Save(context.Background(), v, nil); e != nil {
		t.Fatal(e)
	}
	return v
}

type fakeMail struct {
	mu      sync.Mutex
	deleted map[string]bool
	calls   atomic.Int64
	key     []byte
	delay   time.Duration
}

func (f *fakeMail) List(ctx context.Context, a Account, _ []string, n int) ([]Message, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Message{}
	for i := 0; i < min(n, 100); i++ {
		id := fmt.Sprint(i)
		if f.deleted[a.ID+":"+id] {
			continue
		}
		out = append(out, Message{Subject: fmt.Sprintf("验证邮件 %03d", i), From: "verify@example.net", To: fmt.Sprintf("recipient%03d@example.net", i), ReceivedAt: time.Now().Add(-time.Duration(i) * time.Minute), Folder: "inbox", Preview: "Your verification code", Source: MessageRef{AccountID: a.ID, Protocol: "graph", Folder: "inbox", ID: id}})
	}
	return out, nil
}
func (f *fakeMail) Detail(_ context.Context, a Account, _ []string, r MessageRef) (Message, error) {
	return Message{Subject: "验证邮件 " + r.ID, From: "verify@example.net", To: "recipient000@example.net", Text: "验证码：123456", HTML: "<p>验证码：<strong>123456</strong></p><script>parent.bad=true</script>", Source: r}, nil
}
func (f *fakeMail) Delete(_ context.Context, a Account, _ []string, refs []MessageRef) ([]DeleteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleted == nil {
		f.deleted = map[string]bool{}
	}
	out := []DeleteResult{}
	for _, r := range refs {
		v := DeleteResult{Ref: signRef(f.key, r), Success: r.ID != "1"}
		if v.Success {
			f.deleted[a.ID+":"+r.ID] = true
		} else {
			v.Error = "模拟权限不足"
		}
		out = append(out, v)
	}
	return out, nil
}
func installFake(a *App) *fakeMail { f := &fakeMail{key: a.key}; a.mail.backend = f; return f }
func request(t *testing.T, h http.Handler, method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var data io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		data = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, data)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func loginFixture(t *testing.T, a *App) (*http.Cookie, string) {
	t.Helper()
	w := request(t, a.Handler(), "POST", "/api/login", map[string]string{"password": "test-password-12345"}, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var s session
	json.Unmarshal(w.Body.Bytes(), &s)
	return w.Result().Cookies()[0], s.CSRF
}

func TestRecipientAndPlatformIsolation(t *testing.T) {
	cases := []struct {
		name, to, target string
		headers          map[string][]string
		want             bool
	}{
		{"exact", "box@example.net", "box@example.net", nil, true}, {"substring", "mybox@example.net", "box@example.net", nil, false},
		{"icloud-delivery", "hide@icloud.com", "hide@icloud.com", map[string][]string{"received-spf": {"pass hide=icloud.com@relay.example"}}, true},
		{"icloud-prefix", "hide@icloud.com", "hide@icloud.com", map[string][]string{"return-path": {"otherhide=icloud.com@relay"}}, false},
		{"hme", "hide@icloud.com", "hide@icloud.com", map[string][]string{"x-icloud-hme": {"v=1; p=hide@icloud.com; k=1"}}, true},
		{"plus-no-hme", "hide+tag@icloud.com", "hide+tag@icloud.com", map[string][]string{"x-icloud-hme": {"p=hide+tag@icloud.com"}}, false},
		{"plus-delivery", "hide+tag@icloud.com", "hide+tag@icloud.com", map[string][]string{"authentication-results-original": {"smtp.mailfrom= hide+tag=icloud.com@relay"}}, true},
		{"to-required", "else@icloud.com", "hide@icloud.com", map[string][]string{"x-icloud-hme": {"p=hide@icloud.com"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := recipientMatches(Message{To: c.to, Headers: c.headers}, c.target); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
	p := Platform{Domains: []string{"example.net"}, Keywords: []string{"code"}}
	for _, c := range []struct {
		from, subject string
		want          bool
	}{{"a@example.net", "Code 123", true}, {"a@sub.example.net", "code", true}, {"a@badexample.net", "code", false}, {"a@example.net", "newsletter", false}} {
		if platformMatches(Message{From: c.from, Subject: c.subject}, p) != c.want {
			t.Fatal(c)
		}
	}
}
func TestCacheCoalescingAndDeleteRace(t *testing.T) {
	c := newMailCache()
	defer c.Close()
	start, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	load := func(ctx context.Context) ([]Message, error) {
		if calls.Add(1) == 1 {
			close(start)
		}
		<-release
		return []Message{{Subject: "old"}}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Load(context.Background(), "a", "a:list", load) }()
	}
	<-start
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("expected one shared request", calls.Load())
	}
	c.Invalidate("a")
	started, finish := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, e := c.Load(context.Background(), "a", "a:list", func(context.Context) ([]Message, error) {
			close(started)
			<-finish
			return []Message{{Subject: "old"}}, nil
		})
		result <- e
	}()
	<-started
	c.Invalidate("a")
	close(finish)
	if e := <-result; !errors.Is(e, ErrConflict) {
		t.Fatal("expected invalidated in-flight request to be rejected", e)
	}
	v, e := c.Load(context.Background(), "a", "a:list", func(context.Context) ([]Message, error) { return []Message{{Subject: "new"}}, nil })
	if e != nil || len(v) != 1 || v[0].Subject != "new" {
		t.Fatal(v, e)
	}
}
func TestAdminDeletionPartialIdempotentAndPublicReadOnly(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "a")
	f := installFake(a)
	link := linkFixture(t, a, "a", "recipient000@example.net")
	h := a.Handler()
	messages, e := a.mail.List(context.Background(), account)
	if e != nil {
		t.Fatal(e)
	}
	refs := []string{messages[0].Ref, messages[1].Ref, messages[0].Ref}
	path := "/api/admin/accounts/a/messages/delete"
	body := map[string]any{"refs": refs}
	if w := request(t, h, "POST", path, body, nil, ""); w.Code != 401 {
		t.Fatal("unauthenticated delete", w.Code)
	}
	cookie, csrf := loginFixture(t, a)
	if w := request(t, h, "POST", path, body, cookie, ""); w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	before := a.store.Cursor()
	w := request(t, h, "POST", path, body, cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Results []DeleteResult `json:"results"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Results) != 2 || !result.Results[0].Success || result.Results[1].Success {
		t.Fatal(result)
	}
	if !f.deleted["a:0"] || f.deleted["a:1"] || f.deleted["a:2"] {
		t.Fatal("unselected message changed")
	}
	if a.store.Cursor() <= before {
		t.Fatal("no invalidation event")
	}
	if w = request(t, h, "POST", path, body, cookie, csrf); w.Code != 200 {
		t.Fatal("retry", w.Code)
	}
	for _, method := range []string{"POST", "DELETE"} {
		w = request(t, h, method, "/api/mailboxes/"+link.Token+"/messages", body, cookie, csrf)
		if w.Code != 405 {
			t.Fatal("public mutation", w.Code)
		}
	}
	forged := signRef(a.key, MessageRef{AccountID: "another", Protocol: "imap", Folder: "inbox", Mailbox: "INBOX", UID: 1, UIDValidity: 1})
	w = request(t, h, "POST", path, map[string]any{"refs": []string{forged}}, cookie, csrf)
	if w.Code != 400 {
		t.Fatal("cross-account ref", w.Code)
	}
	replica := testApp(t, "replica")
	w = request(t, replica.Handler(), "POST", path, body, cookie, csrf)
	if w.Code != 403 {
		t.Fatal("replica mutation", w.Code)
	}
}
func TestTokenRotationAndConcurrentCredentialEdit(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "a")
	var count atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		r.ParseForm()
		if n == 2 {
			close(started)
			<-release
		}
		respond(w, 200, map[string]any{"access_token": fmt.Sprint(n), "refresh_token": fmt.Sprintf("rotated-%d", n), "expires_in": 3600})
	}))
	defer server.Close()
	a.cfg.OAuthURL = server.URL
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := a.tokens.Get(context.Background(), "a", "graph", false); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("parallel refresh not coalesced", count.Load())
	}
	v, _ := a.store.Account(context.Background(), "a")
	if v.RefreshToken != "secret-refresh" {
		t.Fatal(v.RefreshToken)
	}
	done := make(chan error, 1)
	go func() { _, e := a.tokens.Get(context.Background(), "a", "graph", true); done <- e }()
	<-started
	v, _ = a.store.Account(context.Background(), "a")
	version := v.Version
	v.RefreshToken = "admin-new"
	if _, e := a.store.Save(context.Background(), v, &version); e != nil {
		t.Fatal(e)
	}
	a.tokens.Invalidate("a")
	close(release)
	if !errors.Is(<-done, ErrConflict) {
		t.Fatal("expected stale rotation conflict")
	}
	v, _ = a.store.Account(context.Background(), "a")
	if v.RefreshToken != "admin-new" {
		t.Fatal("overwrote new credential")
	}
}
func TestGraphDeleteProtocolAndPartialResponses(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "a")
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			respond(w, 200, map[string]any{"access_token": "write", "expires_in": 3600})
			return
		}
		var b struct {
			Requests []struct{ ID, Method, URL string }
		}
		json.NewDecoder(r.Body).Decode(&b)
		rs := []map[string]any{}
		for _, v := range b.Requests {
			requested = append(requested, v.URL)
			if v.Method != "DELETE" || !strings.HasPrefix(v.URL, "/me/mailFolders/inbox/messages/") {
				t.Error("not folder scoped Graph deletion", v)
			}
			status := 204
			if v.ID == "1" {
				status = 403
			}
			rs = append(rs, map[string]any{"id": v.ID, "status": status})
		}
		respond(w, 200, map[string]any{"responses": rs})
	}))
	defer server.Close()
	a.cfg.GraphURL = server.URL + "/v1.0"
	a.cfg.OAuthURL = server.URL + "/token"
	refs := []MessageRef{{AccountID: "a", Protocol: "graph", Folder: "inbox", ID: "graph-id"}, {AccountID: "a", Protocol: "graph", Folder: "inbox", ID: "other"}}
	out, e := (&liveBackend{app: a}).graphDelete(context.Background(), account, []string{""}, refs)
	if e != nil || len(out) != 2 || !out[0].Success || out[1].Success || len(requested) != 2 {
		t.Fatal(out, e)
	}
}
func TestStoreTransactionReplicationAndEncryption(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "a")
	link := linkFixture(t, a, "a", "recipient@example.net")
	cursor := a.store.Cursor()
	bad, _ := recordOf(Link{ID: "bad", AccountID: "absent", Recipient: "bad@example.net", TokenHash: "unique"})
	if a.store.Put(context.Background(), []Record{bad}, nil) == nil || a.store.Cursor() != cursor {
		t.Fatal("transaction not atomic")
	}
	events, required, e := a.store.Events(context.Background(), 0, 500)
	if e != nil || required {
		t.Fatal(e, required)
	}
	replica := testApp(t, "replica")
	if e = replica.store.Apply(context.Background(), events, cursor); e != nil {
		t.Fatal(e)
	}
	v, _ := replica.store.Account(context.Background(), "a")
	if v.RefreshToken != "" {
		t.Fatal("refresh token leaked to replica")
	}
	r, _ := replica.store.Get(context.Background(), "link", link.ID)
	var l Link
	decode(r.Data, &l)
	if l.Token != "" {
		t.Fatal("raw link token leaked")
	}
	var raw string
	a.store.db.QueryRow("SELECT data FROM records WHERE kind='account'").Scan(&raw)
	if strings.Contains(raw, "secret-refresh") {
		t.Fatal("plaintext credential")
	}
	a.store.db.Exec("DELETE FROM events")
	_, required, e = a.store.Events(context.Background(), 0, 500)
	if e != nil || !required {
		t.Fatal("pruned cursor must resnapshot")
	}
	if store, err := OpenStore(filepath.Join(t.TempDir(), "nested", "db"), a.key); err != nil {
		t.Fatal(err)
	} else {
		store.Close()
	}
}

func TestDeleteAccountCascadesLinksAndReplicaTombstones(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "owner")
	link := linkFixture(t, a, "owner", "child@example.net")
	if _, err := a.store.db.Exec("INSERT INTO link_access(link_id,page_count,json_count,last_access_at) VALUES(?,?,?,?)", link.ID, 3, 2, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.Delete(context.Background(), "account", []string{"owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Account(context.Background(), "owner"); err != ErrNotFound {
		t.Fatalf("account remains after cascade: %v", err)
	}
	if _, err := a.store.Get(context.Background(), "link", link.ID); err != ErrNotFound {
		t.Fatalf("child link remains after cascade: %v", err)
	}
	var accessCount int
	if err := a.store.db.QueryRow("SELECT count(*) FROM link_access WHERE link_id=?", link.ID).Scan(&accessCount); err != nil || accessCount != 0 {
		t.Fatalf("link access row remains: count=%d err=%v", accessCount, err)
	}
	events, required, err := a.store.Events(context.Background(), 0, 50)
	if err != nil || required || len(events) != 4 || !events[2].Deleted || events[2].Record.Kind != "link" || !events[3].Deleted || events[3].Record.Kind != "account" {
		t.Fatalf("unexpected cascade events: required=%v err=%v events=%+v", required, err, events)
	}
	replica := testApp(t, "replica")
	if err := replica.store.Apply(context.Background(), events, a.store.Cursor()); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.store.Account(context.Background(), "owner"); err != ErrNotFound {
		t.Fatalf("replica account remains: %v", err)
	}
	if _, err := replica.store.Get(context.Background(), "link", link.ID); err != ErrNotFound {
		t.Fatalf("replica child link remains: %v", err)
	}
}
func TestPublicHTMLIsolationAndExpiry(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "a")
	installFake(a)
	l := linkFixture(t, a, "a", "recipient000@example.net")
	w := request(t, a.Handler(), "GET", "/m/"+l.Token, nil, nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `sandbox=""`) || !strings.Contains(w.Body.String(), "srcdoc=") {
		t.Fatal(w.Code, w.Body.String())
	}
	l.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	a.store.Save(context.Background(), l, nil)
	w = request(t, a.Handler(), "GET", "/api/mailboxes/"+l.Token+"/messages", nil, nil, "")
	if w.Code != 410 {
		t.Fatal(w.Code)
	}
}

func TestLinkExpiryCanBeChangedAfterCreation(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "a")
	link := linkFixture(t, a, "a", "recipient000@example.net")
	cookie, csrf := loginFixture(t, a)
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	w := request(t, a.Handler(), "POST", "/api/admin/links/batch", map[string]any{
		"ids": []string{link.ID}, "action": "expiry", "value": expiry,
	}, cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("expiry mutation returned %d: %s", w.Code, w.Body.String())
	}
	stored, e := a.store.Get(context.Background(), "link", link.ID)
	if e != nil {
		t.Fatal(e)
	}
	var current Link
	if e = decode(stored.Data, &current); e != nil {
		t.Fatal(e)
	}
	if current.ExpiresAt != expiry {
		t.Fatalf("expiry update was not saved: %q", current.ExpiresAt)
	}
}

func TestEmailHTMLDropsNavigationAndActiveContent(t *testing.T) {
	source := `<meta http-equiv="refresh" content="0;url=https://external.example"><script>alert(1)</script><div onclick="alert(2)"><strong>code 123</strong><img src="https://external.example/pixel"><a href="javascript:alert(3)">open</a><svg onload="alert(4)"></svg></div>`
	out := sanitizeEmail(source)
	for _, bad := range []string{"script", "onclick", "onload", "external.example", "refresh", "javascript:", "<svg"} {
		if strings.Contains(out, bad) {
			t.Fatal("unsafe HTML", out)
		}
	}
	if !strings.Contains(out, "<strong>code 123</strong>") {
		t.Fatal("lost mail formatting", out)
	}
}
