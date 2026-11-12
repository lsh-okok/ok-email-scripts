package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func newJSONBody(t *testing.T, value any) io.Reader {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

func TestMailMappingOnlyActiveAccountsAndIsIdempotent(t *testing.T) {
	a := testApp(t, "primary")
	active := accountFixture(t, a, "active-account")
	linkFixture(t, a, active.ID, "child@example.com")
	disabled := Account{ID: "disabled-account", Email: "disabled@example.com", Provider: "outlook", Status: "disabled", ClientID: "client", RefreshToken: "secret"}
	if _, err := a.store.Save(context.Background(), disabled, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Save(context.Background(), Link{ID: "disabled-link", AccountID: disabled.ID, Recipient: "disabled-child@example.com"}, nil); err != nil {
		t.Fatal(err)
	}

	preview := httptest.NewRecorder()
	a.mailMappingPreview(preview, httptest.NewRequest("GET", "/api/admin/mail/mapping/preview", nil))
	if preview.Code != 200 {
		t.Fatalf("preview status %d: %s", preview.Code, preview.Body.String())
	}
	var p mailMappingPreview
	if err := json.Unmarshal(preview.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Summary.ActiveAccounts != 1 || p.Summary.Links != 1 || len(p.Accounts) != 1 || len(p.Links) != 1 {
		t.Fatalf("preview included disabled records: %+v", p)
	}

	commit := httptest.NewRecorder()
	a.mailMappingCommit(commit, httptest.NewRequest("POST", "/api/admin/mail/mapping", newJSONBody(t, map[string]any{"include_links": true})))
	if commit.Code != 200 {
		t.Fatalf("commit status %d: %s", commit.Code, commit.Body.String())
	}
	var first mailMappingCommitResponse
	if err := json.Unmarshal(commit.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.CreatedAccounts != 1 || first.CreatedLinks != 1 {
		t.Fatalf("unexpected first mapping result: %+v", first)
	}

	second := httptest.NewRecorder()
	a.mailMappingCommit(second, httptest.NewRequest("POST", "/api/admin/mail/mapping", newJSONBody(t, map[string]any{"include_links": true})))
	if second.Code != 200 {
		t.Fatalf("second commit status %d: %s", second.Code, second.Body.String())
	}
	var again mailMappingCommitResponse
	if err := json.Unmarshal(second.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.CreatedAccounts != 0 || again.CreatedLinks != 0 || again.SkippedAccounts != 1 || again.SkippedLinks != 1 {
		t.Fatalf("mapping is not idempotent: %+v", again)
	}

	var masters, children, mappings int
	if err := a.store.db.QueryRow("SELECT count(*) FROM mailboxes WHERE kind='master'").Scan(&masters); err != nil {
		t.Fatal(err)
	}
	if err := a.store.db.QueryRow("SELECT count(*) FROM mailboxes WHERE kind='child'").Scan(&children); err != nil {
		t.Fatal(err)
	}
	if err := a.store.db.QueryRow("SELECT count(*) FROM mail_source_mappings").Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if masters != 1 || children != 1 || mappings != 2 {
		t.Fatalf("unexpected mapped inventory: masters=%d children=%d mappings=%d", masters, children, mappings)
	}
	var raw, detectionLink string
	if err := a.store.db.QueryRow("SELECT raw FROM mailboxes WHERE kind='master'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, active.RefreshToken) {
		t.Fatal("mapped credentials stored in plaintext")
	}
	plain, err := unseal(a.key, raw)
	if err != nil || string(plain) != mailAccountRaw(active) {
		t.Fatalf("credential snapshot mismatch: %v", err)
	}
	if err := a.store.db.QueryRow("SELECT link FROM mailboxes WHERE kind='child'").Scan(&detectionLink); err != nil {
		t.Fatal(err)
	}
	if detectionLink != "" {
		t.Fatal("mapping used a public URL as a detection link")
	}
}

func TestMailInventoryRoutesUseParentAdminSession(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	created := request(t, a.Handler(), "POST", "/api/admin/mail/masters", map[string]any{
		"raw":       "master@example.com----secret",
		"note":      "mapped inventory",
		"folder_id": 0,
	}, cookie, csrf)
	if created.Code != 201 {
		t.Fatalf("create master status %d: %s", created.Code, created.Body.String())
	}
	listed := request(t, a.Handler(), "GET", "/api/admin/mail/masters?limit=10", nil, cookie, csrf)
	if listed.Code != 200 {
		t.Fatalf("list masters status %d: %s", listed.Code, listed.Body.String())
	}
	var page struct {
		Items []MailMaster `json:"items"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Email != "master@example.com" {
		t.Fatalf("unexpected inventory list: %+v", page.Items)
	}
}

func TestMailMappingReportsDuplicateRecipientsWithoutRollingBack(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "source")
	for _, platformID := range []string{"p1", "p2"} {
		if _, err := a.store.Save(context.Background(), Platform{ID: platformID, Name: platformID}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.Save(context.Background(), Link{ID: platformID, PlatformID: platformID, AccountID: account.ID, Recipient: "duplicate@example.com", TokenHash: platformID}, nil); err != nil {
			t.Fatal(err)
		}
	}
	preview := httptest.NewRecorder()
	a.mailMappingPreview(preview, httptest.NewRequest("GET", "/", nil))
	var p mailMappingPreview
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &p) != nil || p.Summary.Conflicts != 1 {
		t.Fatalf("duplicate was not previewed: %d %s", preview.Code, preview.Body.String())
	}
	w := httptest.NewRecorder()
	a.mailMappingCommit(w, httptest.NewRequest("POST", "/", newJSONBody(t, mailMappingCommitRequest{})))
	var result mailMappingCommitResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("commit failed: %d %s", w.Code, w.Body.String())
	}
	if result.CreatedAccounts != 1 || result.CreatedLinks != 1 || result.SkippedLinks != 1 || len(result.Conflicts) != 1 {
		t.Fatalf("unexpected conflict report: %+v", result)
	}
}

func TestMailSalesRequiresAdminCSRFAndPrimary(t *testing.T) {
	a := testApp(t, "primary")
	if w := request(t, a.Handler(), "GET", "/api/admin/mail/mapping/preview", nil, nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated preview returned %d", w.Code)
	}
	cookie, _ := loginFixture(t, a)
	if w := request(t, a.Handler(), "POST", "/api/admin/mail/mapping", map[string]any{}, cookie, ""); w.Code != 403 {
		t.Fatalf("mapping without CSRF returned %d", w.Code)
	}
	replica := testApp(t, "replica")
	if w := request(t, replica.Handler(), "GET", "/api/admin/mail/stats", nil, nil, ""); w.Code != 403 {
		t.Fatalf("replica sales route returned %d", w.Code)
	}
}
