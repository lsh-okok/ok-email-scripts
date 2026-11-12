package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func publicPaginationFixture(t *testing.T, app *App, link Link) (string, []Message) {
	t.Helper()
	w := request(t, app.Handler(), http.MethodGet, "/api/mailboxes/"+link.Token+"/messages", nil, nil, "")
	var page struct {
		Items []Message `json:"items"`
		Next  string    `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != http.StatusOK || len(page.Items) != 20 || page.Next == "" {
		t.Fatalf("initial page: status=%d error=%v", w.Code, err)
	}
	return page.Next, page.Items
}

func TestPublicDisabledAccountRejectsEveryReadRoute(t *testing.T) {
	for _, provider := range []string{"outlook", "icloud"} {
		t.Run(provider, func(t *testing.T) {
			app := testApp(t, "primary")
			account := accountFixture(t, app, "disabled-public")
			account.Provider = provider
			if _, err := app.store.Save(context.Background(), account, nil); err != nil {
				t.Fatal(err)
			}
			account, _ = app.store.Account(context.Background(), account.ID)
			link := linkFixture(t, app, account.ID, "child@example.net")
			backend := &paginationMail{}
			app.mail.backend = backend
			cursor, items := publicPaginationFixture(t, app, link)

			account.Status = "disabled"
			// Keep the generation unchanged: the current status must independently
			// revoke access even when a replica or a caller retains old references.
			if _, err := app.store.Save(context.Background(), account, nil); err != nil {
				t.Fatal(err)
			}
			apiPath := "/api/mailboxes/" + link.Token + "/messages"
			pagePath := "/m/" + link.Token
			for _, path := range []string{
				apiPath, apiPath + "?details=1", apiPath + "?id=0", apiPath + "?message_id=0",
				apiPath + "?ref=" + url.QueryEscape(items[0].Ref),
				apiPath + "?cursor=" + url.QueryEscape(cursor) + "&details=1",
				pagePath, pagePath + "?list=1", pagePath + "?id=0",
				pagePath + "?ref=" + url.QueryEscape(items[0].Ref),
				"/show/p0/" + account.PublicCode + "/child",
				"/api/mailboxes/p0/" + account.PublicCode + "/child/messages?id=0",
			} {
				w := request(t, app.Handler(), http.MethodGet, path, nil, nil, "")
				if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), `"text":"body"`) {
					t.Errorf("disabled read was not rejected: path=%s status=%d", strings.Split(path, "?")[0], w.Code)
				}
			}
			if backend.details.Load() != 0 {
				t.Fatalf("disabled reads reached the detail backend: %d", backend.details.Load())
			}
			if _, err := app.mail.Detail(context.Background(), account, items[0].Source); err == nil {
				t.Fatal("direct detail service accepted a disabled account")
			}
		})
	}
}

func TestPublicCursorRejectsChangedPlatformRules(t *testing.T) {
	for _, rule := range []string{"domains", "emails", "keywords"} {
		t.Run(rule, func(t *testing.T) {
			app := testApp(t, "primary")
			account := accountFixture(t, app, "platform-cursor")
			platform := Platform{ID: "cursor-platform", Name: "Allowed sender", Domains: []string{"example.net"}}
			if _, err := app.store.Save(context.Background(), platform, nil); err != nil {
				t.Fatal(err)
			}
			link := linkFixture(t, app, account.ID, "child@example.net")
			link.PlatformID = platform.ID
			if _, err := app.store.Save(context.Background(), link, nil); err != nil {
				t.Fatal(err)
			}
			backend := &paginationMail{}
			app.mail.backend = backend
			cursor, _ := publicPaginationFixture(t, app, link)
			apiPath := "/api/mailboxes/" + link.Token + "/messages"

			platform.Name = "Renamed display label"
			if _, err := app.store.Save(context.Background(), platform, nil); err != nil {
				t.Fatal(err)
			}
			if w := request(t, app.Handler(), http.MethodGet, apiPath+"?cursor="+url.QueryEscape(cursor), nil, nil, ""); w.Code != http.StatusOK {
				t.Fatalf("display rename invalidated the policy: %d", w.Code)
			}
			switch rule {
			case "domains":
				platform.Domains = []string{"blocked.invalid"}
			case "emails":
				platform.Domains, platform.Emails = nil, []string{"blocked@elsewhere.invalid"}
			case "keywords":
				platform.Keywords = []string{"absent-subject-keyword"}
			}
			if _, err := app.store.Save(context.Background(), platform, nil); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"", "&details=1"} {
				w := request(t, app.Handler(), http.MethodGet, apiPath+"?cursor="+url.QueryEscape(cursor)+suffix, nil, nil, "")
				if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "subject-20") {
					t.Errorf("old policy cursor was accepted: status=%d", w.Code)
				}
			}
			w := request(t, app.Handler(), http.MethodGet, apiPath, nil, nil, "")
			var fresh struct {
				Items []Message `json:"items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &fresh); err != nil || w.Code != http.StatusOK || len(fresh.Items) != 0 {
				t.Fatalf("fresh page did not apply changed rules: status=%d error=%v", w.Code, err)
			}
			if backend.details.Load() != 0 {
				t.Fatal("rejected cursor fetched a message body")
			}
		})
	}
}
