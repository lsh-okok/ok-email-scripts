package core

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAccountCredentialAccessAndRedaction(t *testing.T) {
	a := testApp(t, "primary")
	v := accountFixture(t, a, "credential")
	linkFixture(t, a, v.ID, "alias@example.net")
	v.Note = "detail note"
	v.Proxies = []string{"http://proxy-user:proxy-pass@localhost:8080"}
	if record, err := a.store.Save(context.Background(), v, &v.Version); err != nil {
		t.Fatal(err)
	} else {
		v.Version = record.Version
	}
	a.tokens.values = map[string]AccessToken{
		v.ID + ":graph": {Token: "cached-access", ExpiresAt: time.Now().Add(time.Hour)},
		v.ID + ":imap":  {Token: "expired-access", ExpiresAt: time.Now().Add(-time.Hour)},
		"other:graph":   {Token: "other-account-access", ExpiresAt: time.Now().Add(time.Hour)},
	}
	cookie, csrf := loginFixture(t, a)
	path := "/api/admin/accounts/" + v.ID + "/credentials"
	for _, check := range []struct {
		name, method string
		cookie       *http.Cookie
		csrf         string
		code         int
	}{
		{"anonymous", "POST", nil, "", 401},
		{"csrf", "POST", cookie, "", 403},
		{"get", "GET", cookie, csrf, 405},
		{"admin", "POST", cookie, csrf, 200},
	} {
		w := request(t, a.Handler(), check.method, path, map[string]any{}, check.cookie, check.csrf)
		if w.Code != check.code {
			t.Fatalf("%s: got %d want %d", check.name, w.Code, check.code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("credential response may be cached")
		}
		if check.code == 200 && (!strings.Contains(w.Body.String(), "secret-refresh") || !strings.Contains(w.Body.String(), "cached-access") || !strings.Contains(w.Body.String(), "detail note") || !strings.Contains(w.Body.String(), "proxy-pass") || !strings.Contains(w.Body.String(), "link_count") || strings.Contains(w.Body.String(), "expired-access") || strings.Contains(w.Body.String(), "other-account-access")) {
			t.Fatal("credential detail has incorrect token scope")
		}
	}
	w := request(t, a.Handler(), "GET", "/api/admin/accounts", nil, cookie, csrf)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret-refresh") || strings.Contains(w.Body.String(), "cached-access") {
		t.Fatal("account list leaked a secret")
	}
	var list struct{ Items []accountView }
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].LinkCount != 1 || !list.Items[0].HasCredential {
		t.Fatal("account metadata or link count missing")
	}
	// The detail and export must read the latest persisted refresh token.
	v.RefreshToken = "rotated-refresh"
	if record, err := a.store.Save(context.Background(), v, &v.Version); err != nil {
		t.Fatal(err)
	} else {
		v.Version = record.Version
	}
	w = request(t, a.Handler(), "POST", path, map[string]any{}, cookie, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "rotated-refresh") || strings.Contains(w.Body.String(), "secret-refresh") {
		t.Fatal("credential detail did not reflect rotation")
	}
	for _, method := range []string{"GET", "POST"} {
		w = request(t, a.Handler(), method, "/api/admin/accounts/missing/credentials", map[string]any{}, cookie, csrf)
		want := 404
		if method == "GET" {
			want = 405
		}
		if w.Code != want {
			t.Fatal("missing credential route", w.Code)
		}
	}
	replica := testApp(t, "replica")
	for _, route := range []string{path, "/api/admin/accounts/export"} {
		if w := request(t, replica.Handler(), "POST", route, map[string]any{}, cookie, csrf); w.Code != 403 {
			t.Fatal("replica exposed credential route", w.Code)
		}
	}
}

func TestAccountExportScopesFormatsAndPermissions(t *testing.T) {
	a := testApp(t, "primary")
	for i := 0; i < 25; i++ {
		v := accountFixture(t, a, fmt.Sprintf("outlook-%02d", i))
		if i == 0 {
			v.Status, v.Note, v.RefreshToken = "disabled", "=HYPERLINK(\"https://example.net\")", "latest-refresh"
			if _, err := a.store.Save(context.Background(), v, &v.Version); err != nil {
				t.Fatal(err)
			}
		}
	}
	icloud := Account{ID: "icloud", Email: "direct@icloud.com", Provider: "icloud", Status: "active", Password: "app-password", Proxies: []string{"http://proxy-user:proxy-pass@localhost:8080"}}
	if _, err := a.store.Save(context.Background(), icloud, nil); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := loginFixture(t, a)
	path := "/api/admin/accounts/export"
	body := accountExportRequest{Scope: "filtered", Format: "txt", IncludeCredentials: true}
	if w := request(t, a.Handler(), "POST", path, body, nil, ""); w.Code != 401 {
		t.Fatal("anonymous export", w.Code)
	}
	if w := request(t, a.Handler(), "POST", path, body, cookie, ""); w.Code != 403 {
		t.Fatal("export without CSRF", w.Code)
	}
	w := request(t, a.Handler(), "POST", path, body, cookie, csrf)
	if w.Code != 200 || w.Header().Get("X-Export-Count") != "26" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("export did not include all pages", w.Code, w.Header().Get("X-Export-Count"))
	}
	for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
		account, err := parseAccountLine(line)
		if err != nil || account.Email == "" {
			t.Fatal("TXT cannot be reimported", err)
		}
		if account.Email == "outlook-00@outlook.com" && account.RefreshToken != "latest-refresh" {
			t.Fatal("TXT exported an outdated token")
		}
	}
	body.Format, body.Status = "json", "disabled"
	w = request(t, a.Handler(), "POST", path, body, cookie, csrf)
	var exported []Account
	if err := json.Unmarshal(w.Body.Bytes(), &exported); err != nil || len(exported) != 1 || exported[0].ID != "outlook-00" {
		t.Fatal("status filter was ignored")
	}
	body.Status, body.Provider, body.GroupID, body.Query = "", "outlook", "__ungrouped__", "outlook-1"
	w = request(t, a.Handler(), "POST", path, body, cookie, csrf)
	if w.Code != 200 || w.Header().Get("X-Export-Count") != "10" {
		t.Fatal("search/provider/group export filter mismatch")
	}
	body.Scope, body.IDs = "selected", []string{"icloud", "outlook-00", "icloud"}
	w = request(t, a.Handler(), "POST", path, body, cookie, csrf)
	if w.Code != 200 || w.Header().Get("X-Export-Count") != "2" {
		t.Fatal("selected export incorrectly applied current filters or duplicates")
	}
	body.IncludeCredentials = false
	w = request(t, a.Handler(), "POST", path, body, cookie, csrf)
	if w.Code != 200 || strings.Contains(w.Body.String(), "latest-refresh") || strings.Contains(w.Body.String(), "app-password") || strings.Contains(w.Body.String(), "proxy-pass") {
		t.Fatal("metadata export exposed credentials")
	}
	body.IncludeCredentials, body.Format = true, "csv"
	w = request(t, a.Handler(), "POST", path, body, cookie, csrf)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if w.Code != 200 || err != nil || len(rows) != 3 || len(rows[0]) != 10 {
		t.Fatal("invalid CSV export", err)
	}
	if rows[2][7] != "'=HYPERLINK(\"https://example.net\")" || rows[2][8] != "latest-refresh" {
		t.Fatal("CSV formula escaping or credential field mismatch")
	}
	body.IDs = append(body.IDs, "missing")
	if w := request(t, a.Handler(), "POST", path, body, cookie, csrf); w.Code != 400 {
		t.Fatal("export silently ignored missing selection")
	}
	body.IDs, body.Format = nil, "txt"
	if w := request(t, a.Handler(), "POST", path, body, cookie, csrf); w.Code != 400 {
		t.Fatal("empty selection exported all accounts")
	}
	body.Scope = "unknown"
	if w := request(t, a.Handler(), "POST", path, body, cookie, csrf); w.Code != 400 {
		t.Fatal("unknown scope accepted")
	}
	body.Scope, body.Format = "filtered", "exe"
	if w := request(t, a.Handler(), "POST", path, body, cookie, csrf); w.Code != 400 {
		t.Fatal("unknown format accepted")
	}
	stats := request(t, a.Handler(), "GET", "/api/admin/stats", nil, cookie, csrf)
	var counts map[string]int
	json.Unmarshal(stats.Body.Bytes(), &counts)
	if counts["accounts"] != 26 || counts["active_accounts"] != 25 || counts["outlook_accounts"] != 25 || counts["icloud_accounts"] != 1 {
		t.Fatal("incorrect account overview", counts)
	}
	list := request(t, a.Handler(), "GET", "/api/admin/accounts?status=disabled&group_id=__ungrouped__", nil, cookie, csrf)
	var page struct{ Total int }
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || page.Total != 1 {
		t.Fatal("list filters differ from export filters")
	}
}

func TestAccountExportAllUsesFourFieldImportFormat(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "four-field")
	account.Password = "import-password"
	if _, err := a.store.Save(context.Background(), account, &account.Version); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := loginFixture(t, a)
	w := request(t, a.Handler(), "POST", "/api/admin/accounts/export", accountExportRequest{Scope: "all", Format: "txt"}, cookie, csrf)
	if w.Code != 200 || w.Header().Get("X-Export-Count") != "1" {
		t.Fatal("all export failed", w.Code, w.Body.String())
	}
	line := strings.TrimSpace(w.Body.String())
	if want := "four-field@outlook.com----import-password----client----secret-refresh"; line != want {
		t.Fatalf("unexpected four-field export: %q", line)
	}
	parsed, err := parseAccountLine(line)
	if err != nil || parsed.Password != "import-password" || parsed.ClientID != "client" || parsed.RefreshToken != "secret-refresh" {
		t.Fatalf("four-field export did not round trip: %+v %v", parsed, err)
	}
}
