package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"mail-lite/web"
)

const changedAdminPassword = "updated-password-67890"

func passwordChangeBody() map[string]string {
	return map[string]string{"current_password": "test-password-12345", "new_password": changedAdminPassword, "confirm_password": changedAdminPassword}
}

func TestAdminPasswordChangeRevokesSessions(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	otherCookie, _ := loginFixture(t, a)
	w := request(t, a.Handler(), "PUT", "/api/admin/password", passwordChangeBody(), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("change: %d %s", w.Code, w.Body.String())
	}
	if cookies := w.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly {
		t.Fatal("password change did not expire the login cookie")
	}
	for _, oldCookie := range []*http.Cookie{cookie, otherCookie} {
		if w := request(t, a.Handler(), "GET", "/api/admin/settings", nil, oldCookie, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("old session remained authorized: %d", w.Code)
		}
	}
	if w := request(t, a.Handler(), "POST", "/api/login", map[string]string{"password": "test-password-12345"}, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("old password accepted: %d", w.Code)
	}
	if w := request(t, a.Handler(), "POST", "/api/login", map[string]string{"password": changedAdminPassword}, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("new password rejected: %d", w.Code)
	}
	var hash string
	if err := a.store.AuxGet("auth", "password", &hash); err != nil || hash == changedAdminPassword || bcrypt.CompareHashAndPassword([]byte(hash), []byte(changedAdminPassword)) != nil {
		t.Fatal("password was not persisted as a bcrypt hash")
	}
}

func TestAdminPasswordChangeValidation(t *testing.T) {
	for _, check := range []struct {
		name, field, value string
	}{
		{"wrong current", "current_password", "incorrect-password"},
		{"missing current", "current_password", ""},
		{"short new", "new_password", "short"},
		{"too long new", "new_password", strings.Repeat("a", 73)},
		{"unicode byte limit", "new_password", strings.Repeat("密", 25)},
		{"confirmation mismatch", "confirm_password", "another-password"},
	} {
		t.Run(check.name, func(t *testing.T) {
			a := testApp(t, "primary")
			cookie, csrf := loginFixture(t, a)
			body := passwordChangeBody()
			body[check.field] = check.value
			if w := request(t, a.Handler(), "PUT", "/api/admin/password", body, cookie, csrf); w.Code != http.StatusBadRequest {
				t.Fatalf("invalid change: %d %s", w.Code, w.Body.String())
			}
			if w := request(t, a.Handler(), "GET", "/api/admin/settings", nil, cookie, ""); w.Code != http.StatusOK {
				t.Fatal("invalid change revoked the current session")
			}
			if bcrypt.CompareHashAndPassword(a.password, []byte("test-password-12345")) != nil {
				t.Fatal("invalid request changed the password")
			}
		})
	}
	t.Run("same password", func(t *testing.T) {
		a := testApp(t, "primary")
		cookie, csrf := loginFixture(t, a)
		body := passwordChangeBody()
		body["new_password"], body["confirm_password"] = body["current_password"], body["current_password"]
		if w := request(t, a.Handler(), "PUT", "/api/admin/password", body, cookie, csrf); w.Code != http.StatusBadRequest {
			t.Fatalf("unchanged password accepted: %d", w.Code)
		}
	})
}

func TestAdminPasswordChangeAuthorization(t *testing.T) {
	a := testApp(t, "primary")
	if w := request(t, a.Handler(), "PUT", "/api/admin/password", passwordChangeBody(), nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated change: %d", w.Code)
	}
	cookie, csrf := loginFixture(t, a)
	if w := request(t, a.Handler(), "PUT", "/api/admin/password", passwordChangeBody(), cookie, "wrong-csrf"); w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF protection: %d", w.Code)
	}
	data, _ := json.Marshal(passwordChangeBody())
	r := httptest.NewRequest("PUT", "/api/admin/password", bytes.NewReader(data))
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Origin", "https://other.example")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin change accepted: %d", w.Code)
	}
	replica := testApp(t, "replica")
	if w := request(t, replica.Handler(), "PUT", "/api/admin/password", passwordChangeBody(), cookie, csrf); w.Code != http.StatusForbidden {
		t.Fatalf("replica exposed password changes: %d", w.Code)
	}
}

func TestAdminPasswordChangePersistsAcrossRestartAndEnvironmentReset(t *testing.T) {
	cfg := Config{Role: "primary", DataDir: t.TempDir(), AdminPassword: "test-password-12345"}
	a, err := NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := loginFixture(t, a)
	w := request(t, a.Handler(), "PUT", "/api/admin/password", passwordChangeBody(), cookie, csrf)
	a.Close()
	if w.Code != http.StatusOK {
		t.Fatalf("change: %d %s", w.Code, w.Body.String())
	}
	for _, check := range []struct{ configured, expected string }{
		{cfg.AdminPassword, changedAdminPassword},
		{"", changedAdminPassword},
		{"environment-reset-12345", "environment-reset-12345"},
	} {
		cfg.AdminPassword = check.configured
		a, err = NewApp(cfg, web.Assets)
		if err != nil {
			t.Fatal(err)
		}
		w := request(t, a.Handler(), "POST", "/api/login", map[string]string{"password": check.expected}, nil, "")
		a.Close()
		if w.Code != http.StatusOK {
			t.Fatalf("restart rejected expected password: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestAdminPasswordChangeRateLimit(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	body := passwordChangeBody()
	body["current_password"] = "incorrect-password"
	for i := 0; i < 7; i++ {
		if w := request(t, a.Handler(), "PUT", "/api/admin/password", body, cookie, csrf); w.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	if w := request(t, a.Handler(), "PUT", "/api/admin/password", body, cookie, csrf); w.Code != http.StatusTooManyRequests {
		t.Fatalf("password guessing was not rate limited: %d", w.Code)
	}
}
