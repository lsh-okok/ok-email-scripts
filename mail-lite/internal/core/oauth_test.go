package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestScheduledRefreshForcesTokenValidation(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "scheduled-cache")
	a.tokens.mu.Lock()
	a.tokens.values = map[string]AccessToken{
		"scheduled-cache:graph": {Token: "cached", ExpiresAt: time.Now().Add(time.Hour)},
	}
	a.tokens.mu.Unlock()
	serverCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls++
		respond(w, 200, map[string]any{"access_token": "fresh", "refresh_token": "same", "expires_in": 3600})
	}))
	defer server.Close()
	a.cfg.OAuthURL = server.URL
	job := &Job{ID: "scheduled-cache"}
	a.runRefresh(context.Background(), job, []string{"scheduled-cache"}, true)
	if serverCalls != 1 || job.Failed != 0 || len(job.Errors) != 0 {
		t.Fatalf("scheduled refresh did not force token validation: calls=%d job=%+v", serverCalls, job)
	}
}

func TestTokenScopesReflectMicrosoftResponse(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "scopes")
	granted := "https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/User.Read"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.PostForm.Get("scope") != graphConfiguredScope || r.PostForm.Get("grant_type") != "refresh_token" {
			t.Error("incorrect read permission request")
		}
		respond(w, 200, map[string]any{"access_token": "access", "refresh_token": "rotated", "expires_in": 3600, "scope": granted})
	}))
	defer server.Close()
	a.cfg.OAuthURL = server.URL
	token, err := a.tokens.Get(context.Background(), "scopes", "graph", false)
	if err != nil || token.Scope != granted {
		t.Fatalf("actual granted scopes missing: %+v %v", token, err)
	}
	cookie, csrf := loginFixture(t, a)
	w := request(t, a.Handler(), "POST", "/api/admin/accounts/scopes/credentials", map[string]any{}, cookie, csrf)
	var detail accountDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || len(detail.AccessTokens) != 1 || detail.AccessTokens[0].Scope != granted || detail.RefreshToken != "secret-refresh" {
		t.Fatalf("credential details lost scope or changed imported refresh token: %s %v", w.Body.String(), err)
	}
}

func TestTokenErrorsDistinguishPermissionsAndExpiredCredentials(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "failure")
	for _, check := range []struct {
		code, want string
		status     int
	}{
		{"invalid_scope", "权限范围", 400},
		{"invalid_grant", "Refresh Token", 400},
		{"invalid_client", "Client ID", 400},
		{"interaction_required", "重新登录", 400},
		{"temporarily_unavailable", "暂时不可用", 503},
		{"", "限流", 429},
	} {
		t.Run(check.code+check.want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				respond(w, check.status, map[string]any{"error": check.code, "error_codes": []int{70011}, "error_description": "sensitive-refresh-and-proxy-password"})
			}))
			defer server.Close()
			a.cfg.OAuthURL = server.URL
			_, err := a.tokens.Get(context.Background(), "failure", "graph", true)
			if err == nil || !strings.Contains(err.Error(), check.want) || !strings.Contains(err.Error(), "AADSTS70011") || strings.Contains(err.Error(), "sensitive-refresh-and-proxy-password") {
				t.Fatalf("incorrect or unsafe error: %v", err)
			}
		})
	}
	if len(a.tokens.values) != 0 {
		t.Fatal("failed grant populated token cache")
	}
}

func TestOutlookFallbackKeepsBothAuthorizationErrors(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "fallback")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		code, number := "invalid_scope", 70011
		if strings.Contains(r.PostForm.Get("scope"), "IMAP.AccessAsUser.All") {
			code, number = "invalid_grant", 700082
		}
		respond(w, 400, map[string]any{"error": code, "error_codes": []int{number}})
	}))
	defer server.Close()
	a.cfg.OAuthURL = server.URL
	_, err := a.mail.List(context.Background(), account)
	if err == nil || !strings.Contains(err.Error(), "AADSTS70011") || !strings.Contains(err.Error(), "AADSTS700082") {
		t.Fatalf("fallback concealed the original failure: %v", err)
	}
	job := &Job{ID: "refresh-errors"}
	a.runRefresh(context.Background(), job, []string{account.ID}, false)
	fresh, err := a.store.Account(context.Background(), account.ID)
	if err != nil || !strings.Contains(fresh.RefreshError, "AADSTS70011") || !strings.Contains(fresh.RefreshError, "AADSTS700082") || len(job.Errors) != 1 || !strings.Contains(job.Errors[0], "AADSTS70011") {
		t.Fatalf("refresh details concealed the cause: %+v %+v %v", fresh, job, err)
	}
}

func TestTokenNetworkErrorDoesNotExposeProxyCredentials(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://user:proxy-password@example.test/token?refresh_token=secret", Err: syscall.Errno(10013)}
	message := tokenConnectionError(err, "http://user:proxy-password@proxy.test:8080").Error()
	if !strings.Contains(message, "网络连接被系统拒绝") || !strings.Contains(message, "邮箱代理") || strings.Contains(message, "proxy-password") || strings.Contains(message, "refresh_token") {
		t.Fatalf("unsafe or misleading connection error: %s", message)
	}
}
