package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type largePublicPaginationMail struct {
	paginationMail
	count int
}

func (p *largePublicPaginationMail) List(_ context.Context, account Account, _ []string, _ int) ([]Message, error) {
	items := make([]Message, p.count)
	for i := range items {
		items[i] = Message{Subject: fmt.Sprintf("message-%04d", i), From: "sender@example.net", To: "child@example.net", Preview: strings.Repeat("summary ", 10), ReceivedAt: time.Unix(1_800_000_000-int64(i), 0).UTC(), Folder: "inbox", Source: MessageRef{AccountID: account.ID, Protocol: "graph", Folder: "inbox", ID: fmt.Sprint(i)}}
	}
	return items, nil
}

func TestPublicPaginationUsesBoundedURLsOverHTTP(t *testing.T) {
	app := testApp(t, "primary")
	account := accountFixture(t, app, "large-public-page")
	link := linkFixture(t, app, account.ID, "child@example.net")
	backend := &largePublicPaginationMail{count: 3000}
	app.mail.backend = backend
	server := httptest.NewUnstartedServer(app.Handler())
	server.Config.MaxHeaderBytes = 8 << 10
	server.Start()
	defer server.Close()
	apiPath := server.URL + "/api/mailboxes/" + link.Token + "/messages"
	readPage := func(path string) (items []Message, cursor string, more bool) {
		t.Helper()
		response, err := server.Client().Get(path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var page struct {
			Items []Message `json:"items"`
			Next  string    `json:"next_cursor"`
			More  bool      `json:"has_more"`
		}
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&page) != nil {
			t.Fatalf("HTTP pagination failed with %d", response.StatusCode)
		}
		if len(page.Next) > 2048 {
			t.Fatalf("cursor contains an oversized snapshot: %d bytes", len(page.Next))
		}
		return page.Items, page.Next, page.More
	}
	first, cursor, more := readPage(apiPath)
	if len(first) != 20 || !more || cursor == "" {
		t.Fatal("large mailbox did not start pagination")
	}
	t.Logf("3000 messages: first cursor is %d bytes", len(cursor))
	seen := map[string]bool{}
	for _, message := range first {
		seen[message.MessageID] = true
	}
	initialCursor := cursor
	for more {
		items, next, hasMore := readPage(apiPath + "?cursor=" + url.QueryEscape(cursor))
		for _, message := range items {
			if seen[message.MessageID] {
				t.Fatalf("duplicate message while paging: %s", message.MessageID)
			}
			seen[message.MessageID] = true
		}
		cursor, more = next, hasMore
	}
	if len(seen) != backend.count || cursor != "" {
		t.Fatalf("pagination lost messages: got=%d want=%d", len(seen), backend.count)
	}
	// A later request or a body expansion cannot consume or mutate the snapshot.
	items, _, _ := readPage(apiPath + "?cursor=" + url.QueryEscape(initialCursor) + "&details=1")
	if len(items) != 20 || items[0].MessageID != "20" || items[0].Text != "body" {
		t.Fatal("cursor retry did not preserve its page and detail behavior")
	}
	items, _, _ = readPage(apiPath + "?cursor=" + url.QueryEscape(initialCursor))
	if items[0].Text != "" || items[0].HTML != "" {
		t.Fatal("body expansion mutated the stored summary")
	}
}

func TestPublicPaginationSessionRejectsInvalidReferences(t *testing.T) {
	app := testApp(t, "primary")
	account := accountFixture(t, app, "session-binding")
	link := linkFixture(t, app, account.ID, "child@example.net")
	app.mail.backend = &paginationMail{}
	cursor, _ := publicPaginationFixture(t, app, link)
	plain, err := unseal(app.key, cursor)
	if err != nil {
		t.Fatal(err)
	}
	var valid publicCursor
	if err := json.Unmarshal(plain, &valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*publicCursor)
	}{
		{"old-version", func(c *publicCursor) { c.Version = 1 }},
		{"unknown-session", func(c *publicCursor) { c.Session = randomID() }},
		{"negative-offset", func(c *publicCursor) { c.Offset = -1 }},
		{"past-end", func(c *publicCursor) { c.Offset = 1000 }},
		{"changed-account", func(c *publicCursor) { c.AccountID = "other" }},
		{"changed-link", func(c *publicCursor) { c.LinkID = "other" }},
		{"changed-recipient", func(c *publicCursor) { c.Recipient = "other@example.net" }},
		{"changed-order", func(c *publicCursor) { c.Order = "oldest" }},
		{"changed-policy", func(c *publicCursor) { c.PolicyKey = "other" }},
		{"expired", func(c *publicCursor) {
			c.CreatedAt = time.Now().Add(-time.Hour).UTC()
			c.ExpiresAt = c.CreatedAt.Add(publicCursorTTL)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := valid
			test.change(&invalid)
			raw, err := app.sealPublicCursor(invalid)
			if err != nil {
				t.Fatal(err)
			}
			w := request(t, app.Handler(), http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?cursor="+url.QueryEscape(raw), nil, nil, "")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid cursor accepted: %d", w.Code)
			}
		})
	}
	other := linkFixture(t, app, account.ID, "another@example.net")
	w := request(t, app.Handler(), http.MethodGet, "/api/mailboxes/"+other.Token+"/messages?cursor="+url.QueryEscape(cursor), nil, nil, "")
	if w.Code != http.StatusBadRequest {
		t.Fatal("cursor crossed public links")
	}
	app.publicPages.clear()
	w = request(t, app.Handler(), http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?cursor="+url.QueryEscape(cursor), nil, nil, "")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "刷新列表") {
		t.Fatal("lost/evicted session did not request a refresh")
	}
}

func TestPublicPaginationConcurrentRetriesPreserveSnapshot(t *testing.T) {
	app := testApp(t, "primary")
	account := accountFixture(t, app, "concurrent-public-page")
	link := linkFixture(t, app, account.ID, "child@example.net")
	app.mail.backend = &paginationMail{}
	cursor, _ := publicPaginationFixture(t, app, link)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, page, err := app.publicCursorPage(context.Background(), link, cursor, 20, true)
			if err != nil || len(page.Items) != 20 || page.Items[0].MessageID != "20" || page.Items[0].Text != "body" {
				t.Errorf("concurrent cursor retry: error=%v items=%d", err, len(page.Items))
			}
		}()
	}
	group.Wait()
}

func TestPublicPageStoreBoundsAndGenerationOrdering(t *testing.T) {
	store := newPublicPageStore()
	newSession := func(id string, generation int64, created time.Time, items []publicCursorItem) *publicPageSession {
		return &publicPageSession{cursor: publicCursor{Version: 2, Session: id, AccountID: "store-account", Generation: generation, CreatedAt: created, ExpiresAt: created.Add(publicCursorTTL)}, items: items}
	}
	now := time.Now().UTC()
	for i := range publicPagesMaxSessions + 1 {
		if err := store.put(newSession(fmt.Sprint(i), 1, now.Add(time.Duration(i)*time.Millisecond), nil)); err != nil {
			t.Fatal(err)
		}
	}
	if store.get("0") != nil || len(store.sessions) != publicPagesMaxSessions {
		t.Fatal("session count did not evict the oldest snapshot")
	}
	newer := newSession("newer", 2, now, nil)
	if err := store.put(newer); err != nil {
		t.Fatal(err)
	}
	if err := store.put(newSession("late-old", 1, now, nil)); err == nil || store.get("newer") != newer || len(store.sessions) != 1 {
		t.Fatal("late old-generation snapshot displaced a newer session")
	}
	expired := newSession("expired", 2, now.Add(-time.Hour), nil)
	if err := store.put(expired); err != nil {
		t.Fatal(err)
	}
	if store.get("expired") != nil || store.get("newer") == nil {
		t.Fatal("expiration did not prune only the expired session")
	}
	store.clear()
	// Large independent sessions exercise the byte budget before the count cap.
	for i := range 6 {
		items := []publicCursorItem{{Message: Message{Preview: strings.Repeat("x", 12<<20)}}}
		if err := store.put(newSession(fmt.Sprint(i), 2, now.Add(time.Duration(i)*time.Millisecond), items)); err != nil {
			t.Fatal(err)
		}
	}
	if store.bytes > publicPagesMaxBytes || store.get("0") != nil {
		t.Fatal("snapshot byte budget was not enforced")
	}
	oversized := []publicCursorItem{{Message: Message{Preview: strings.Repeat("x", publicPageSessionMaxBytes)}}}
	if err := store.put(newSession("oversized", 2, now, oversized)); err == nil {
		t.Fatal("oversized individual snapshot was stored")
	}
	store.clear()
	if store.bytes != 0 || len(store.sessions) != 0 {
		t.Fatal("clearing sessions retained their byte budget")
	}
}

func TestPublicPaginationCursorDoesNotSurviveRestart(t *testing.T) {
	app := testApp(t, "primary")
	account := accountFixture(t, app, "restart-public-page")
	link := linkFixture(t, app, account.ID, "child@example.net")
	app.mail.backend = &paginationMail{}
	cursor, _ := publicPaginationFixture(t, app, link)
	// A fresh store models restart without reopening a real database or service.
	app.publicPages = newPublicPageStore()
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	res, err := server.Client().Get(server.URL + "/api/mailboxes/" + link.Token + "/messages?cursor=" + url.QueryEscape(cursor))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "刷新列表") {
		t.Fatalf("restart cursor status=%d", res.StatusCode)
	}
}
