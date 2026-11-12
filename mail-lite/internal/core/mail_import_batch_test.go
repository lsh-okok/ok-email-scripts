package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mailJSONRequest(t *testing.T, a *App, method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func TestMailImportExportAndMasterBatch(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	preview := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/import/preview", map[string]any{
		"files":     []map[string]string{{"name": "stock.txt", "content": "master@example.com----secret\none@example.com----https://example.com/one\ntwo@example.com----https://example.com/two"}},
		"folder_id": 0,
	}, cookie, csrf)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var p struct {
		Token  string `json:"token"`
		Report struct {
			AddedMasters  int `json:"added_masters"`
			AddedChildren int `json:"added_children"`
		} `json:"report"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &p); err != nil || p.Token == "" || p.Report.AddedMasters != 1 || p.Report.AddedChildren != 2 {
		t.Fatalf("bad preview: %v %s", err, preview.Body.String())
	}
	commit := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/import/commit", map[string]string{"token": p.Token}, cookie, csrf)
	if commit.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", commit.Code, commit.Body.String())
	}
	var master MailMaster
	var masters struct {
		Items []MailMaster `json:"items"`
	}
	list := request(t, a.Handler(), http.MethodGet, "/api/admin/mail/masters", nil, cookie, csrf)
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &masters) != nil || len(masters.Items) != 1 {
		t.Fatalf("master list: %d %s", list.Code, list.Body.String())
	}
	master = masters.Items[0]
	var stored string
	if err := a.store.db.QueryRow("SELECT raw FROM mailboxes WHERE id=?", master.ID).Scan(&stored); err != nil || stored == "master@example.com----secret" {
		t.Fatalf("imported master material was not encrypted: %v", err)
	}
	children := request(t, a.Handler(), http.MethodGet, "/api/admin/mail/children", nil, cookie, csrf)
	if children.Code != http.StatusOK || !strings.Contains(children.Body.String(), "one@example.com") {
		t.Fatalf("children: %d %s", children.Code, children.Body.String())
	}
	var childPage struct {
		Items []MailChild `json:"items"`
	}
	if err := json.Unmarshal(children.Body.Bytes(), &childPage); err != nil || len(childPage.Items) != 2 {
		t.Fatalf("children payload: %v %s", err, children.Body.String())
	}
	childID := childPage.Items[0].ID
	updated := request(t, a.Handler(), http.MethodPut, fmt.Sprintf("/api/admin/mail/children/%d", childID), map[string]string{"note": "=FORMULA()"}, cookie, csrf)
	if updated.Code != http.StatusOK {
		t.Fatalf("child note: %d %s", updated.Code, updated.Body.String())
	}
	export := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/export", map[string]any{"ids": []int64{master.ID}, "kind": "master", "format": "txt"}, cookie, csrf)
	if export.Code != http.StatusOK || !strings.Contains(export.Body.String(), "master@example.com----secret") {
		t.Fatalf("txt export: %d %s", export.Code, export.Body.String())
	}
	csvExport := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/export", map[string]any{"ids": []int64{master.ID}, "kind": "master", "format": "csv"}, cookie, csrf)
	if csvExport.Code != http.StatusOK || !strings.Contains(csvExport.Body.String(), "'=FORMULA()") {
		t.Fatalf("csv export: %d %s", csvExport.Code, csvExport.Body.String())
	}
	folder := request(t, a.Handler(), http.MethodPost, "/api/admin/mail/folders", map[string]string{"name": "Moved"}, cookie, csrf)
	var f MailFolder
	if folder.Code != http.StatusCreated || json.Unmarshal(folder.Body.Bytes(), &f) != nil {
		t.Fatalf("folder: %d %s", folder.Code, folder.Body.String())
	}
	moved := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{"ids": []int64{master.ID}, "action": "move", "folder_id": f.ID}, cookie, csrf)
	if moved.Code != http.StatusOK {
		t.Fatalf("move: %d %s", moved.Code, moved.Body.String())
	}
	impact := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{"ids": []int64{master.ID}, "action": "preview_delete"}, cookie, csrf)
	if impact.Code != http.StatusOK {
		t.Fatalf("delete preview: %d %s", impact.Code, impact.Body.String())
	}
	var di mailDeleteImpact
	if err := json.Unmarshal(impact.Body.Bytes(), &di); err != nil || di.Children != 2 || di.Version == "" {
		t.Fatalf("bad impact: %v %s", err, impact.Body.String())
	}
	deleted := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{"ids": []int64{master.ID}, "action": "delete", "version": di.Version}, cookie, csrf)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	var remaining int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mailboxes").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("cascade delete: %d %v", remaining, err)
	}
}

func TestMailImportCommitIsSessionBound(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	preview := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/import/preview", map[string]any{"files": []map[string]string{{"name": "one.txt", "content": "one@example.com"}}}, cookie, csrf)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var p struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(preview.Body.Bytes(), &p) != nil || p.Token == "" {
		t.Fatal("missing preview token")
	}
	otherCookie, otherCSRF := loginFixture(t, a)
	commit := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/import/commit", map[string]string{"token": p.Token}, otherCookie, otherCSRF)
	if commit.Code != http.StatusConflict {
		t.Fatalf("cross-session commit: %d %s", commit.Code, commit.Body.String())
	}
}
