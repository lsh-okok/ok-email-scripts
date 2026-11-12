package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type adminTestSource struct {
	items  []Message
	offset int
}

func (s *adminTestSource) clone() adminPageSource { out := *s; return &out }
func (s *adminTestSource) next(_ context.Context, count int) ([]Message, bool, error) {
	end := min(s.offset+count, len(s.items))
	items := append([]Message{}, s.items[s.offset:end]...)
	s.offset = end
	return items, end < len(s.items), nil
}

func adminTestSourceFor(items []Message, order, folder string) adminPageSource {
	out := make([]Message, 0, len(items))
	for _, item := range items {
		if folder == "" || item.Folder == folder {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if order == "oldest" {
			return messageBefore(out[i], out[j])
		}
		return messageAfter(out[i], out[j])
	})
	return &adminTestSource{items: out}
}

// Adapt the existing test backends without weakening live pagination with a
// fallback that downloads an entire mailbox's headers.
func (p *paginationMail) startAdminPage(ctx context.Context, account Account, proxies []string, order, folder string, _ time.Time) (adminPageSource, error) {
	items, err := p.List(ctx, account, proxies, adminPageSize)
	return adminTestSourceFor(items, order, folder), err
}
func (p *fakeMail) startAdminPage(ctx context.Context, account Account, proxies []string, order, folder string, _ time.Time) (adminPageSource, error) {
	items, err := p.List(ctx, account, proxies, 100)
	return adminTestSourceFor(items, order, folder), err
}

func TestAdminPaginationCursorValidation(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "admin-pages")
	other := accountFixture(t, a, "admin-other")
	fake := &paginationMail{}
	a.mail.backend = fake
	cookie, _ := loginFixture(t, a)
	path := "/api/admin/accounts/" + account.ID + "/messages"
	get := func(path string) (*httptest.ResponseRecorder, adminMailPage) {
		t.Helper()
		w := request(t, a.Handler(), http.MethodGet, path, nil, cookie, "")
		var page adminMailPage
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
		}
		return w, page
	}
	w, first := get(path)
	if w.Code != 200 || len(first.Items) != 20 || first.Items[0].Subject != "subject-00" || first.Items[19].Subject != "subject-19" || !first.HasMore || first.NextCursor == "" || len(first.NextCursor) > 2048 || fake.details.Load() != 0 || first.Items[0].Text != "" {
		t.Fatalf("bad first admin page: %d %+v", w.Code, first)
	}
	cursorPath := path + "?cursor=" + url.QueryEscape(first.NextCursor)
	w, second := get(cursorPath)
	if w.Code != 200 || len(second.Items) != 20 || second.Items[0].Subject != "subject-20" || !second.HasMore {
		t.Fatalf("bad second admin page: %d %+v", w.Code, second)
	}
	w, repeat := get(cursorPath)
	if w.Code != 200 || len(repeat.Items) != 20 || repeat.Items[0].Ref != second.Items[0].Ref {
		t.Fatal("repeating a cursor changed its page")
	}
	w, third := get(path + "?cursor=" + url.QueryEscape(second.NextCursor))
	if w.Code != 200 || len(third.Items) != 5 || third.HasMore || third.NextCursor != "" {
		t.Fatalf("bad final admin page: %d %+v", w.Code, third)
	}
	for _, invalid := range []string{
		"/api/admin/accounts/" + other.ID + "/messages?cursor=" + url.QueryEscape(first.NextCursor),
		cursorPath + "&order=oldest",
		cursorPath + "&folder=inbox",
		path + "?cursor=x" + url.QueryEscape(first.NextCursor[1:]),
		path + "?folder=trash",
		path + "?order=random",
	} {
		if w, _ := get(invalid); w.Code != 400 {
			t.Fatalf("invalid pagination returned %d for %s", w.Code, invalid)
		}
	}
	w, oldest := get(path + "?order=oldest&limit=100")
	if w.Code != 200 || len(oldest.Items) != 20 || oldest.Items[0].Subject != "subject-44" || oldest.Order != "oldest" {
		t.Fatalf("oldest/clamped limit failed: %d %+v", w.Code, oldest)
	}
	plain, _ := unseal(a.key, first.NextCursor)
	var expired adminPageCursor
	json.Unmarshal(plain, &expired)
	expired.CreatedAt = time.Now().Add(-time.Hour)
	expired.ExpiresAt = expired.CreatedAt.Add(publicCursorTTL)
	plain, _ = json.Marshal(expired)
	raw, _ := seal(a.key, plain)
	if w, _ := get(path + "?cursor=" + raw); w.Code != 400 {
		t.Fatal("expired cursor was accepted")
	}
	account.Generation++
	if _, err := a.store.Save(context.Background(), account, nil); err != nil {
		t.Fatal(err)
	}
	if w, _ := get(cursorPath); w.Code != 400 {
		t.Fatal("account generation change did not invalidate cursor")
	}
}

type adminGraphFixture struct {
	mu            sync.Mutex
	items         map[string][]graphMessage
	messageCalls  int
	failNext      bool
	filters       []string
	detailQueried bool
}

func newAdminGraphFixture(t *testing.T, a *App) *adminGraphFixture {
	t.Helper()
	f := &adminGraphFixture{items: map[string][]graphMessage{"inbox": {}, "junkemail": {}}}
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 47; i++ {
		folder := "inbox"
		if i%2 != 0 {
			folder = "junkemail"
		}
		f.items[folder] = append(f.items[folder], graphMessage{ID: fmt.Sprintf("message-%02d", i), Subject: fmt.Sprintf("subject-%02d", i), Received: base.Add(-time.Duration(i) * time.Minute), Preview: "preview"})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			respond(w, 200, map[string]any{"access_token": "access", "expires_in": 3600})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			f.detailQueried = true
			http.NotFound(w, r)
			return
		}
		folder := "inbox"
		if strings.Contains(r.URL.Path, "/junkemail/") {
			folder = "junkemail"
		}
		query := r.URL.Query()
		offset, _ := strconv.Atoi(query.Get("page"))
		if offset > 0 && f.failNext {
			f.failNext = false
			http.Error(w, "temporary folder failure", 503)
			return
		}
		f.messageCalls++
		filter := query.Get("$filter")
		f.filters = append(f.filters, filter)
		boundary, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(filter, "receivedDateTime le "))
		if err != nil || strings.Contains(query.Get("$select"), ",body,") {
			t.Errorf("invalid header query: %s", r.URL.RawQuery)
		}
		items := make([]graphMessage, 0, len(f.items[folder]))
		for _, item := range f.items[folder] {
			if !item.Received.After(boundary) {
				items = append(items, item)
			}
		}
		sort.SliceStable(items, func(i, j int) bool {
			if strings.HasSuffix(query.Get("$orderby"), " asc") {
				return items[i].Received.Before(items[j].Received)
			}
			return items[i].Received.After(items[j].Received)
		})
		top, _ := strconv.Atoi(query.Get("$top"))
		if top != 20 || offset > len(items) {
			t.Errorf("invalid Graph page bounds top=%d offset=%d", top, offset)
		}
		end := min(offset+top, len(items))
		response := map[string]any{"value": items[offset:end]}
		if end < len(items) {
			query.Set("page", strconv.Itoa(end))
			response["@odata.nextLink"] = "http://" + r.Host + r.URL.Path + "?" + query.Encode()
		}
		respond(w, 200, response)
	}))
	t.Cleanup(server.Close)
	a.cfg.GraphURL, a.cfg.OAuthURL = server.URL+"/v1.0", server.URL+"/token"
	return f
}

func TestAdminGraphIncrementalPaginationAndSnapshot(t *testing.T) {
	for _, order := range []string{"latest", "oldest"} {
		t.Run(order, func(t *testing.T) {
			a := testApp(t, "primary")
			account := accountFixture(t, a, "graph-admin")
			fixture := newAdminGraphFixture(t, a)
			cookie, _ := loginFixture(t, a)
			path := "/api/admin/accounts/" + account.ID + "/messages"
			get := func(query string) (*httptest.ResponseRecorder, adminMailPage) {
				w := request(t, a.Handler(), http.MethodGet, path+query, nil, cookie, "")
				var page adminMailPage
				if w.Code == 200 {
					json.Unmarshal(w.Body.Bytes(), &page)
				}
				return w, page
			}
			w, page := get("?order=" + order)
			if w.Code != 200 || len(page.Items) != 20 {
				t.Fatalf("first Graph page: %d %s", w.Code, w.Body.String())
			}
			fixture.mu.Lock()
			if fixture.messageCalls != 2 || fixture.detailQueried {
				t.Errorf("first page should only query two 20-header pages, got %d", fixture.messageCalls)
			}
			fixture.items["inbox"] = append(fixture.items["inbox"], graphMessage{ID: "arrived-later", Subject: "new arrival", Received: time.Now().UTC()})
			fixture.failNext = true
			fixture.mu.Unlock()
			all := append([]Message{}, page.Items...)
			cursor := page.NextCursor
			w, _ = get("?cursor=" + url.QueryEscape(cursor))
			if w.Code != 400 || !strings.Contains(w.Body.String(), "503") {
				t.Fatalf("folder failure should be explicit: %d %s", w.Code, w.Body.String())
			}
			for page.HasMore {
				w, page = get("?cursor=" + url.QueryEscape(cursor))
				if w.Code != 200 {
					t.Fatalf("Graph continuation failed: %d %s", w.Code, w.Body.String())
				}
				all = append(all, page.Items...)
				cursor = page.NextCursor
			}
			if len(all) != 47 {
				t.Fatalf("Graph merged %d messages, want 47", len(all))
			}
			seen := make(map[string]bool)
			for i, item := range all {
				want := i
				if order == "oldest" {
					want = 46 - i
				}
				if item.Subject != fmt.Sprintf("subject-%02d", want) || seen[item.Ref] || item.Text != "" || item.HTML != "" {
					t.Fatalf("unstable Graph page at %d: %+v", i, item)
				}
				seen[item.Ref] = true
			}
			w, refreshed := get("?order=latest")
			if w.Code != 200 || refreshed.Items[0].Subject != "new arrival" {
				t.Fatal("refresh did not include the new arrival")
			}
			w, inbox := get("?folder=inbox")
			if w.Code != 200 || inbox.Folder != "inbox" {
				t.Fatal("folder query failed")
			}
			for _, item := range inbox.Items {
				if item.Folder != "inbox" {
					t.Fatal("backend folder filtering returned another folder")
				}
			}
		})
	}
}
