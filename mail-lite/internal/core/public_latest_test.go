package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type publicLatestMail struct {
	latest  atomic.Int64
	lists   atomic.Int64
	details atomic.Int64
	read    func(context.Context, Account, string) ([]Message, error)
}

func latestMessageForTest(account Account, recipient string) Message {
	return Message{
		Subject: "latest combined message", From: "sender@example.net", To: recipient,
		ReceivedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), Folder: "inbox",
		Text:   "combined body for " + recipient,
		HTML:   "<p>combined body</p><script>unsafeLatestCode()</script>",
		Source: MessageRef{AccountID: account.ID, Protocol: "imap", Folder: "inbox", Mailbox: "INBOX", UID: 10, UIDValidity: 42},
	}
}

func (f *publicLatestMail) LatestRecipient(ctx context.Context, account Account, _ []string, recipient string) ([]Message, error) {
	f.latest.Add(1)
	if f.read != nil {
		return f.read(ctx, account, recipient)
	}
	return []Message{latestMessageForTest(account, recipient)}, nil
}

func (f *publicLatestMail) List(ctx context.Context, account Account, proxies []string, limit int) ([]Message, error) {
	return f.ListRecipient(ctx, account, proxies, limit, "child@example.net")
}

func (f *publicLatestMail) ListRecipient(_ context.Context, account Account, _ []string, _ int, recipient string) ([]Message, error) {
	f.lists.Add(1)
	item := latestMessageForTest(account, recipient)
	item.HTML, item.Text = "", ""
	return []Message{item}, nil
}

func (f *publicLatestMail) Detail(_ context.Context, account Account, _ []string, source MessageRef) (Message, error) {
	f.details.Add(1)
	item := latestMessageForTest(account, "child@example.net")
	item.Source = source
	return item, nil
}

func (*publicLatestMail) Delete(context.Context, Account, []string, []MessageRef) ([]DeleteResult, error) {
	return nil, errors.New("unexpected delete")
}

// Deliberately omit LatestRecipient to verify support for existing backends.
type publicLatestLegacyMail struct{ mail *publicLatestMail }

func (f *publicLatestLegacyMail) List(ctx context.Context, account Account, proxies []string, limit int) ([]Message, error) {
	return f.mail.List(ctx, account, proxies, limit)
}
func (f *publicLatestLegacyMail) ListRecipient(ctx context.Context, account Account, proxies []string, limit int, recipient string) ([]Message, error) {
	return f.mail.ListRecipient(ctx, account, proxies, limit, recipient)
}
func (f *publicLatestLegacyMail) Detail(ctx context.Context, account Account, proxies []string, source MessageRef) (Message, error) {
	return f.mail.Detail(ctx, account, proxies, source)
}
func (f *publicLatestLegacyMail) Delete(ctx context.Context, account Account, proxies []string, refs []MessageRef) ([]DeleteResult, error) {
	return f.mail.Delete(ctx, account, proxies, refs)
}

func publicLatestAccount(t *testing.T, app *App) Account {
	t.Helper()
	account := Account{ID: "latest-icloud", Email: "main@icloud.com", Provider: "icloud", Status: "active", Password: "fake-password"}
	if _, err := app.store.Save(context.Background(), account, nil); err != nil {
		t.Fatal(err)
	}
	account, err := app.store.Account(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func TestPublicLatestICloudCombinedReadAndCacheIsolation(t *testing.T) {
	app := testApp(t, "primary")
	account := publicLatestAccount(t, app)
	link := linkFixture(t, app, account.ID, "child@example.net")
	backend := &publicLatestMail{}
	app.mail.backend = backend
	for _, route := range []string{"/show/p0/" + account.PublicCode + "/child", "/m/" + link.Token} {
		w := request(t, app.Handler(), http.MethodGet, route, nil, nil, "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "combined body") || strings.Contains(w.Body.String(), "unsafeLatestCode") {
			t.Fatalf("latest page must render the sanitized complete body: status=%d", w.Code)
		}
	}
	items, err := app.mail.LatestRecipient(context.Background(), account, " CHILD@example.net ")
	if err != nil || len(items) != 1 || items[0].Ref == "" || strings.Contains(items[0].HTML, "<script") {
		t.Fatalf("combined result lost reference/body isolation: items=%d error=%v", len(items), err)
	}
	source, err := parseRef(app.key, items[0].Ref, account.ID)
	if err != nil || source.UID != 10 || source.UIDValidity != 42 || source.AccountID != account.ID {
		t.Fatalf("combined result did not retain its signed source: %+v error=%v", source, err)
	}
	if backend.latest.Load() != 1 || backend.lists.Load() != 0 || backend.details.Load() != 0 {
		t.Fatalf("public route should share one combined read: latest=%d lists=%d details=%d", backend.latest.Load(), backend.lists.Load(), backend.details.Load())
	}
	other, err := app.mail.LatestRecipient(context.Background(), account, "other@example.net")
	if err != nil || len(other) != 1 || other[0].To != "other@example.net" || backend.latest.Load() != 2 {
		t.Fatal("recipient cache reused another child's result")
	}
	account.Generation++
	if _, err := app.mail.LatestRecipient(context.Background(), account, "child@example.net"); err != nil || backend.latest.Load() != 3 {
		t.Fatalf("generation change reused stale latest result: calls=%d error=%v", backend.latest.Load(), err)
	}
	app.cache.Invalidate(account.ID)
	if _, err := app.mail.LatestRecipient(context.Background(), account, "child@example.net"); err != nil || backend.latest.Load() != 4 {
		t.Fatalf("account invalidation did not clear latest result: calls=%d error=%v", backend.latest.Load(), err)
	}
}

func TestPublicLatestCombinedConcurrentReadCoalesces(t *testing.T) {
	app := testApp(t, "primary")
	account := publicLatestAccount(t, app)
	started, release := make(chan struct{}), make(chan struct{})
	backend := &publicLatestMail{read: func(ctx context.Context, account Account, recipient string) ([]Message, error) {
		close(started)
		select {
		case <-release:
			return []Message{latestMessageForTest(account, recipient)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	app.mail.backend = backend
	const callers = 12
	var group sync.WaitGroup
	results := make(chan error, callers)
	launch := make(chan struct{})
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-launch
			items, err := app.mail.LatestRecipient(context.Background(), account, "child@example.net")
			if err == nil && (len(items) != 1 || items[0].To != "child@example.net") {
				err = errors.New("missing or wrong recipient result")
			}
			results <- err
		}()
	}
	close(launch)
	select {
	case <-started:
		close(release)
	case <-time.After(5 * time.Second):
		t.Fatal("combined backend did not start")
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if backend.latest.Load() != 1 {
		t.Fatalf("concurrent callers duplicated upstream work: %d", backend.latest.Load())
	}
}

func TestPublicLatestCombinedValidatesRecipientAndAccount(t *testing.T) {
	for _, test := range []struct {
		name, to, sourceAccount, forwarding string
		allowed                             bool
	}{
		{"forwarded", "hide@icloud.com", "latest-icloud", "p=hide@icloud.com", true},
		{"wrong-forwarding", "hide@icloud.com", "latest-icloud", "p=other@icloud.com", false},
		{"substring", "otherhide@icloud.com", "latest-icloud", "p=hide@icloud.com", false},
		{"wrong-account", "hide@icloud.com", "another-account", "p=hide@icloud.com", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := testApp(t, "primary")
			account := publicLatestAccount(t, app)
			link := linkFixture(t, app, account.ID, "hide@icloud.com")
			backend := &publicLatestMail{read: func(_ context.Context, account Account, _ string) ([]Message, error) {
				item := latestMessageForTest(account, test.to)
				item.Headers = map[string][]string{"x-icloud-hme": {test.forwarding}}
				item.Source.AccountID = test.sourceAccount
				item.Text, item.HTML = "private-combined-content", ""
				return []Message{item}, nil
			}}
			app.mail.backend = backend
			w := request(t, app.Handler(), http.MethodGet, "/m/"+link.Token, nil, nil, "")
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-combined-content") != test.allowed {
				t.Fatalf("combined read bypassed recipient/account verification: status=%d allowed=%t", w.Code, test.allowed)
			}
		})
	}
}

func TestPublicLatestDisabledAndExpiredLinksNeverRead(t *testing.T) {
	for _, cause := range []string{"disabled", "expired"} {
		t.Run(cause, func(t *testing.T) {
			app := testApp(t, "primary")
			account := publicLatestAccount(t, app)
			link := linkFixture(t, app, account.ID, "child@example.net")
			if cause == "disabled" {
				account.Status = "disabled"
				if _, err := app.store.Save(context.Background(), account, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				link.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
				if _, err := app.store.Save(context.Background(), link, nil); err != nil {
					t.Fatal(err)
				}
			}
			backend := &publicLatestMail{}
			app.mail.backend = backend
			w := request(t, app.Handler(), http.MethodGet, "/m/"+link.Token, nil, nil, "")
			if w.Code == http.StatusOK || backend.latest.Load() != 0 || backend.lists.Load() != 0 || backend.details.Load() != 0 {
				t.Fatalf("unavailable link reached upstream: status=%d latest=%d lists=%d details=%d", w.Code, backend.latest.Load(), backend.lists.Load(), backend.details.Load())
			}
		})
	}
}

func TestPublicLatestOutlookUsesCombinedRead(t *testing.T) {
	app := testApp(t, "primary")
	account := accountFixture(t, app, "latest-outlook")
	link := linkFixture(t, app, account.ID, "child@example.net")
	backend := &publicLatestMail{}
	app.mail.backend = backend
	w := request(t, app.Handler(), http.MethodGet, "/m/"+link.Token, nil, nil, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "combined body") || backend.latest.Load() != 1 || backend.lists.Load() != 0 || backend.details.Load() != 0 {
		t.Fatalf("Outlook default page did not use combined read: status=%d latest=%d lists=%d details=%d", w.Code, backend.latest.Load(), backend.lists.Load(), backend.details.Load())
	}
}

func TestPublicLatestLegacyBackendAndPlatformScope(t *testing.T) {
	for _, scope := range []string{"legacy", "platform"} {
		t.Run(scope, func(t *testing.T) {
			app := testApp(t, "primary")
			account := publicLatestAccount(t, app)
			link := linkFixture(t, app, account.ID, "child@example.net")
			if scope == "platform" {
				platform := Platform{ID: "sender-filter", Name: "Sender filter", Domains: []string{"example.net"}}
				if _, err := app.store.Save(context.Background(), platform, nil); err != nil {
					t.Fatal(err)
				}
				link.PlatformID = platform.ID
				if _, err := app.store.Save(context.Background(), link, nil); err != nil {
					t.Fatal(err)
				}
			}
			backend := &publicLatestMail{}
			if scope == "legacy" {
				app.mail.backend = &publicLatestLegacyMail{mail: backend}
			} else {
				app.mail.backend = backend
			}
			w := request(t, app.Handler(), http.MethodGet, "/m/"+link.Token, nil, nil, "")
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "combined body") || backend.latest.Load() != 0 || backend.lists.Load() != 1 || backend.details.Load() != 1 {
				t.Fatalf("legacy or scoped read changed: status=%d latest=%d lists=%d details=%d", w.Code, backend.latest.Load(), backend.lists.Load(), backend.details.Load())
			}
		})
	}
}
