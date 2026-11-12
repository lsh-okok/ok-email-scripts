package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"mail-lite/web"
)

type accessStatsResponse struct {
	Items     []requestStat `json:"items"`
	Total     uint64        `json:"total"`
	ItemCount int           `json:"item_count"`
}

func readAccessStats(t *testing.T, a *App, cookie *http.Cookie, q string) accessStatsResponse {
	t.Helper()
	w := request(t, a.Handler(), "GET", "/api/admin/request-stats?"+q, nil, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out accessStatsResponse
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out
}

func TestAccessCountsSeparateLinksAndSurviveRestart(t *testing.T) {
	cfg := Config{Role: "primary", DataDir: t.TempDir(), AdminPassword: "test-password-12345"}
	a, e := NewApp(cfg, web.Assets)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { a.Close() }()
	account := accountFixture(t, a, "a")
	installFake(a)
	first := linkFixture(t, a, "a", "recipient000@example.net")
	first.AccountCode = account.PublicCode
	second := linkFixture(t, a, "a", "recipient001@example.net")
	unvisited := linkFixture(t, a, "a", "recipient002@example.net")
	cursor := a.store.Cursor()
	h := a.Handler()
	for _, path := range []string{
		"/m/" + first.Token, "/m/" + first.Token + "?recent=1",
		"/api/mailboxes/" + first.Token + "/messages?limit=20",
		"/m/" + second.Token,
		"/api/mailboxes/" + second.Token + "/messages",
		"/api/mailboxes/" + second.Token + "/messages?limit=2",
	} {
		if w := request(t, h, "GET", path, nil, nil, ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/health/live", "/api/session", "/m/" + randomToken(), "/m/bad", "/other/" + first.Token} {
		request(t, h, "GET", path, nil, nil, "")
	}
	request(t, h, "HEAD", "/m/"+first.Token, nil, nil, "")
	request(t, h, "POST", "/m/"+first.Token, nil, nil, "")
	cookie, _ := loginFixture(t, a)
	out := readAccessStats(t, a, cookie, "page_size=2")
	if out.Total != 6 || out.ItemCount != 3 || len(out.Items) != 2 {
		t.Fatalf("incorrect summary/pagination: %+v", out)
	}
	// The two URLs and raw token all locate exactly the same link.
	for _, q := range []string{first.Token, "http://localhost/m/" + first.Token + "?recent=1", "/api/mailboxes/" + first.Token + "/messages?limit=20", first.Recipient} {
		filtered := readAccessStats(t, a, cookie, "q="+url.QueryEscape(q))
		if filtered.ItemCount != 1 || len(filtered.Items) != 1 {
			t.Fatalf("exact link search failed: %q", q)
		}
		v := filtered.Items[0]
		if v.LinkID != first.ID || v.PageCount != 2 || v.JSONCount != 1 || v.Count != 3 || v.LastAccessAt == "" {
			t.Fatalf("links merged or request counts incorrect: %+v", v)
		}
		pageURL, jsonURL := publicLinkURLs("http://example.com", first, "")
		if v.URL != pageURL || v.JSONURL != jsonURL {
			t.Fatal("statistics do not identify concrete URLs", v)
		}
	}
	page2 := readAccessStats(t, a, cookie, "page_size=2&page=2")
	if len(page2.Items) != 1 || page2.Items[0].LinkID != unvisited.ID || page2.Items[0].Count != 0 {
		t.Fatal("unvisited link/pagination missing", page2)
	}
	w := request(t, h, "GET", "/api/admin/links?q="+url.QueryEscape(first.Token), nil, cookie, "")
	var links struct{ Items []linkView }
	if e = json.Unmarshal(w.Body.Bytes(), &links); e != nil || w.Code != 200 || len(links.Items) != 1 || links.Items[0].Count != 3 {
		t.Fatal("link list is missing per-link counts", w.Code, w.Body.String(), e)
	}
	var rows int
	a.store.db.QueryRow("SELECT count(*) FROM link_access").Scan(&rows)
	if rows != 2 || a.store.Cursor() != cursor {
		t.Fatal("invalid requests stored or visits changed replication config", rows, a.store.Cursor())
	}
	if w = request(t, h, "GET", "/api/admin/request-stats", nil, nil, ""); w.Code != 401 {
		t.Fatal("public stats expose links", w.Code)
	}
	a.Close()
	a, e = NewApp(cfg, web.Assets)
	if e != nil {
		t.Fatal(e)
	}
	out = readAccessStats(t, a, cookie, "q="+second.Token)
	if out.Total != 6 || len(out.Items) != 1 || out.Items[0].PageCount != 1 || out.Items[0].JSONCount != 2 {
		t.Fatal("per-link counts lost across restart", out)
	}
}

func TestConcurrentAndExpiredLinkAccess(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "a")
	link := linkFixture(t, a, "a", "recipient000@example.net")
	link.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, e := a.store.Save(context.Background(), link, nil); e != nil {
		t.Fatal(e)
	}
	h := a.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "/m/" + link.Token
			if i%2 == 0 {
				path = "/api/mailboxes/" + link.Token + "/messages"
			}
			r := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 410 {
				t.Errorf("expired request status %d", w.Code)
			}
		}(i)
	}
	wg.Wait()
	cookie, _ := loginFixture(t, a)
	out := readAccessStats(t, a, cookie, "")
	if out.Total != 200 || len(out.Items) != 1 || out.Items[0].PageCount != 100 || out.Items[0].JSONCount != 100 {
		t.Fatal("concurrent requests lost counts or failed attempts not counted", out)
	}
	if e := a.store.Delete(context.Background(), "link", []string{link.ID}); e != nil {
		t.Fatal(e)
	}
	out = readAccessStats(t, a, cookie, "")
	if out.Total != 0 || out.ItemCount != 0 {
		t.Fatal("deleted link still appears", out)
	}
}
