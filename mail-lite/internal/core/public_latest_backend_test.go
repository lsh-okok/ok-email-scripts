package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/client"
)

type publicLatestGraphFixture struct {
	server                *httptest.Server
	healthy               atomic.Bool
	wrongDetailRecipient  atomic.Bool
	requests, inbox, junk atomic.Int64
	details               atomic.Int64
	recipient             string
}

func newPublicLatestGraphFixture(t *testing.T, app *App, account Account, recipient string) *publicLatestGraphFixture {
	t.Helper()
	f := &publicLatestGraphFixture{recipient: recipient}
	f.healthy.Store(true)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if !f.healthy.Load() {
			respond(w, http.StatusBadGateway, map[string]string{"error": "fixture Graph unavailable"})
			return
		}
		folder := "inbox"
		if strings.Contains(r.URL.Path, "/junkemail/") {
			folder = "junkemail"
		}
		to := recipient
		if !strings.HasSuffix(r.URL.Path, "/messages") && f.wrongDetailRecipient.Load() {
			to = "another@example.net"
		}
		date := "2026-10-27T12:00:00Z"
		if folder == "junkemail" {
			date = "2026-10-27T12:01:00Z"
		}
		message := map[string]any{
			"id": "id-" + folder, "subject": "header-" + folder,
			"receivedDateTime":       date,
			"from":                   map[string]any{"emailAddress": map[string]string{"address": "sender@example.net"}},
			"toRecipients":           []map[string]any{{"emailAddress": map[string]string{"address": to}}},
			"internetMessageHeaders": []map[string]string{{"name": "X-iCloud-HME", "value": "p=" + recipient}},
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			if r.URL.Query().Get("$top") != "1" || !strings.Contains(r.URL.Query().Get("$filter"), recipient) {
				t.Errorf("latest Graph query omitted recipient or one-message bound")
			}
			if folder == "inbox" {
				f.inbox.Add(1)
			} else {
				f.junk.Add(1)
			}
			respond(w, http.StatusOK, map[string]any{"value": []any{message}})
			return
		}
		f.details.Add(1)
		message["subject"] = "detail metadata should not replace header metadata"
		message["body"] = map[string]string{"contentType": "text", "content": "Graph newest body from " + folder}
		respond(w, http.StatusOK, message)
	}))
	t.Cleanup(f.server.Close)
	app.cfg.GraphURL = f.server.URL + "/v1.0"
	app.tokens.mu.Lock()
	app.tokens.values = map[string]AccessToken{account.ID + ":graph": {Token: "fixture-graph-access", ExpiresAt: time.Now().Add(time.Hour)}}
	app.tokens.mu.Unlock()
	return f
}

func latestBackendIMAPWire() *latestIMAPWire {
	return &latestIMAPWire{mailboxes: map[string][]latestIMAPTestMessage{
		"INBOX": {{uid: 10, received: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), to: "child@example.net", subject: "fallback-newest"}},
		"Junk":  {},
	}}
}

func TestPublicLatestBackendGraphNewestAcrossFolders(t *testing.T) {
	app := testApp(t, "primary")
	account := accountFixture(t, app, "graph-latest")
	graph := newPublicLatestGraphFixture(t, app, account, "hide@icloud.com")
	var imapCalls atomic.Int64
	backend := &liveBackend{app: app, imapDial: func(context.Context, Account, []string) (*client.Client, func(), error) {
		imapCalls.Add(1)
		return nil, nil, errors.New("unexpected IMAP fallback")
	}}
	items, err := backend.LatestRecipient(context.Background(), account, []string{""}, graph.recipient)
	if err != nil || len(items) != 1 || items[0].Source.Protocol != "graph" || items[0].Source.Folder != "junkemail" || items[0].Source.ID != "id-junkemail" || items[0].Subject != "header-junkemail" || items[0].Text != "Graph newest body from junkemail" {
		t.Fatalf("Graph newest selection/body merge changed: items=%+v error=%v", items, err)
	}
	if !matchesAccountRecipient(account, items[0], graph.recipient) || graph.inbox.Load() != 1 || graph.junk.Load() != 1 || graph.details.Load() != 1 || imapCalls.Load() != 0 {
		t.Fatalf("Graph did not retain forwarding checks or covered folders: inbox=%d junk=%d details=%d IMAP=%d", graph.inbox.Load(), graph.junk.Load(), graph.details.Load(), imapCalls.Load())
	}
	// The list match cannot authorize a different recipient returned by Detail.
	graph.wrongDetailRecipient.Store(true)
	items, err = backend.LatestRecipient(context.Background(), account, []string{""}, graph.recipient)
	if err != nil || len(items) != 0 || imapCalls.Load() != 0 {
		t.Fatalf("Graph detail bypassed recipient validation: items=%+v error=%v", items, err)
	}
}

func TestPublicLatestBackendRemembersWorkingFallbackAndRecovers(t *testing.T) {
	app := testApp(t, "primary")
	account := accountFixture(t, app, "fallback-latest")
	graph := newPublicLatestGraphFixture(t, app, account, "child@example.net")
	graph.healthy.Store(false)
	wire := latestBackendIMAPWire()
	connect := wire.connector(t)
	var failIMAP atomic.Bool
	backend := &liveBackend{app: app, imapDial: func(ctx context.Context, account Account, proxies []string) (*client.Client, func(), error) {
		if failIMAP.Load() {
			return nil, nil, errors.New("fixture IMAP unavailable")
		}
		return connect(ctx, account, proxies)
	}}
	for i := range 2 {
		items, err := backend.LatestRecipient(context.Background(), account, []string{""}, graph.recipient)
		if err != nil || len(items) != 1 || items[0].Text != "Body for fallback-newest" {
			t.Fatalf("fallback read %d failed: items=%+v error=%v", i, items, err)
		}
		if graph.requests.Load() != 1 {
			t.Fatalf("successful fallback retried unavailable Graph: requests=%d", graph.requests.Load())
		}
	}
	if wire.dials != 2 {
		t.Fatalf("each uncached fallback should use one authenticated connection: %d", wire.dials)
	}
	// A preferred protocol outage must retry a recovered Graph immediately.
	failIMAP.Store(true)
	graph.healthy.Store(true)
	items, err := backend.LatestRecipient(context.Background(), account, []string{""}, graph.recipient)
	if err != nil || len(items) != 1 || items[0].Source.Protocol != "graph" || graph.requests.Load() != 4 {
		t.Fatalf("preferred IMAP failure did not recover through Graph: items=%+v error=%v requests=%d", items, err, graph.requests.Load())
	}
	failIMAP.Store(false)
	items, err = backend.LatestRecipient(context.Background(), account, []string{""}, graph.recipient)
	if err != nil || len(items) != 1 || items[0].Source.Protocol != "graph" || graph.requests.Load() != 7 || wire.dials != 2 {
		t.Fatalf("recovered Graph did not clear fallback preference: items=%+v error=%v requests=%d dials=%d", items, err, graph.requests.Load(), wire.dials)
	}
}

func TestPublicLatestBackendRechecksProtocolAfterExpiryOrAccountChanges(t *testing.T) {
	for _, change := range []string{"expiry", "generation", "version", "proxy"} {
		t.Run(change, func(t *testing.T) {
			app := testApp(t, "primary")
			account := accountFixture(t, app, "preference-"+change)
			graph := newPublicLatestGraphFixture(t, app, account, "child@example.net")
			graph.healthy.Store(false)
			wire := latestBackendIMAPWire()
			backend := &liveBackend{app: app, imapDial: wire.connector(t)}
			proxies := []string{""}
			if _, err := backend.LatestRecipient(context.Background(), account, proxies, graph.recipient); err != nil {
				t.Fatal(err)
			}
			graph.healthy.Store(true)
			switch change {
			case "expiry":
				backend.latestPreferences.mu.Lock()
				backend.latestPreferences.entries[latestReadKey(account, proxies)] = time.Now().Add(-time.Minute)
				backend.latestPreferences.mu.Unlock()
			case "generation":
				account.Generation++
			case "version":
				account.Version++
			case "proxy":
				// Both choices route directly; the configured proxy set changed.
				proxies = []string{"", ""}
			}
			items, err := backend.LatestRecipient(context.Background(), account, proxies, graph.recipient)
			if err != nil || len(items) != 1 || items[0].Source.Protocol != "graph" || graph.requests.Load() != 4 || wire.dials != 1 {
				t.Fatalf("%s retained a stale protocol preference: items=%+v error=%v Graph=%d IMAP=%d", change, items, err, graph.requests.Load(), wire.dials)
			}
		})
	}
}
