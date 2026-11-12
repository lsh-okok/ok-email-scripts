package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGraphScopeFallbackAndRefreshPersistenceMatchOriginalProject(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-refresh=%v", force), func(t *testing.T) {
			a := testApp(t, "primary")
			account := accountFixture(t, a, "original-scopes")
			var requested []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				scope := r.PostForm.Get("scope")
				requested = append(requested, scope)
				if r.PostForm.Get("refresh_token") != "secret-refresh" {
					t.Error("scope fallback replaced the imported credential")
				}
				if !force && scope == graphDefaultScope || force && scope == "" {
					respond(w, 200, map[string]any{"access_token": "access", "refresh_token": "rotated", "expires_in": 3600})
					return
				}
				respond(w, 400, map[string]any{"error": "invalid_grant", "error_description": "AADSTS70000: one or more scopes requested are unauthorized or expired; secret-refresh"})
			}))
			defer server.Close()
			a.cfg.OAuthURL = server.URL
			if _, err := a.tokens.Get(context.Background(), account.ID, "graph", force); err != nil {
				t.Fatal(err)
			}
			if want := tokenScopes("graph", force); !reflect.DeepEqual(requested, want) {
				t.Fatalf("scope order = %v, want %v", requested, want)
			}
			if _, err := a.tokens.Get(context.Background(), account.ID, "graph", false); err != nil || len(requested) != len(tokenScopes("graph", force)) {
				t.Fatalf("ordinary read failed to reuse access token: %v requests=%v", err, requested)
			}
			fresh, err := a.store.Account(context.Background(), account.ID)
			if err != nil {
				t.Fatal(err)
			}
			if force && fresh.RefreshToken != "rotated" || !force && (fresh.RefreshToken != "secret-refresh" || fresh.Version != account.Version) {
				t.Fatal("ordinary reads must preserve imported credentials; explicit refresh must save rotation")
			}
		})
	}
}

func TestExpiredRefreshTokenDoesNotRetryPermissionScopes(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "expired")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		respond(w, 400, map[string]any{"error": "invalid_grant", "error_codes": []int{700082}, "error_description": "private-credential"})
	}))
	defer server.Close()
	a.cfg.OAuthURL = server.URL
	_, err := a.tokens.Get(context.Background(), "expired", "graph", true)
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "已失效") || strings.Contains(err.Error(), "private-credential") {
		t.Fatalf("expired credential was misclassified or retried: calls=%d err=%v", calls, err)
	}
	permissionErr := tokenResponseError(400, tokenResponse{Error: "invalid_grant", ErrorCodes: []int{70000}}, "graph")
	if !strings.Contains(permissionErr.Error(), "权限范围") || strings.Contains(permissionErr.Error(), "Refresh Token 已失效") {
		t.Fatalf("permission mismatch was mislabeled as credential expiry: %v", permissionErr)
	}
}

func TestTokenEndpointsMatchOriginalProject(t *testing.T) {
	if (Config{}).tokenEndpoint("graph") != graphOAuthURL || (Config{}).tokenEndpoint("imap") != imapOAuthURL {
		t.Fatal("Graph must use common and Outlook IMAP must use consumers")
	}
	a := testApp(t, "primary")
	accountFixture(t, a, "endpoints")
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		r.ParseForm()
		if r.URL.Path == "/imap" && r.PostForm.Get("scope") != imapTokenScope {
			t.Error("IMAP requested a Graph scope")
		}
		respond(w, 200, map[string]any{"access_token": "access", "refresh_token": "rotated", "expires_in": 3600})
	}))
	defer server.Close()
	a.cfg.OAuthURL, a.cfg.IMAPOAuthURL = server.URL+"/graph", server.URL+"/imap"
	for _, kind := range []string{"graph", "imap"} {
		if _, err := a.tokens.Get(context.Background(), "endpoints", kind, false); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(paths, []string{"/graph", "/imap"}) {
		t.Fatalf("wrong token endpoints: %v", paths)
	}
}

func TestOutlookOrderChangesReuseAccessToken(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "sorting")
	grants := 0
	var orders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			grants++
			respond(w, 200, map[string]any{"access_token": "cached-access", "refresh_token": "do-not-save", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer cached-access" {
			t.Error("sorting did not reuse the access token")
		}
		orders = append(orders, r.URL.Query().Get("$orderby"))
		respond(w, 200, map[string]any{"value": []any{}})
	}))
	defer server.Close()
	a.cfg.OAuthURL, a.cfg.GraphURL = server.URL+"/token", server.URL+"/v1.0"
	for _, order := range []string{"latest", "oldest", "latest"} {
		if _, err := a.mail.ListOrder(context.Background(), account, order); err != nil {
			t.Fatal(err)
		}
	}
	wantOrders := []string{"receivedDateTime desc", "receivedDateTime desc", "receivedDateTime asc", "receivedDateTime asc"}
	if grants != 1 || !reflect.DeepEqual(orders, wantOrders) {
		t.Fatalf("sorting refreshed authorization or used wrong order: grants=%d orders=%v", grants, orders)
	}
	fresh, err := a.store.Account(context.Background(), account.ID)
	if err != nil || fresh.RefreshToken != account.RefreshToken || fresh.Version != account.Version {
		t.Fatal("sorting changed the imported credential")
	}
}
