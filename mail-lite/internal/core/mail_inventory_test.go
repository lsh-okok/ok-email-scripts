package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func createInventoryMaster(t *testing.T, a *App, cookie *http.Cookie, csrf, raw string) int64 {
	t.Helper()
	w := request(t, a.Handler(), "POST", "/api/admin/mail/masters", map[string]any{"raw": raw, "folder_id": 0, "note": "keep note"}, cookie, csrf)
	if w.Code != http.StatusCreated {
		t.Fatalf("create master: %d %s", w.Code, w.Body.String())
	}
	var result struct{ ID int64 }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.ID
}

func TestMailInventoryMasterMaterialEncryptedAndSnakeCase(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	raw := "master@example.com----private-application-password"
	id := createInventoryMaster(t, a, cookie, csrf, raw)
	var stored string
	var complete bool
	if err := a.store.db.QueryRow("SELECT raw,raw_complete FROM mailboxes WHERE id=?", id).Scan(&stored, &complete); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "private-application-password") || stored == raw || !complete {
		t.Fatalf("mailbox material was not encrypted or marked complete: complete=%v", complete)
	}
	plain, err := unseal(a.key, stored)
	if err != nil || string(plain) != raw {
		t.Fatalf("stored material cannot be decrypted: %v", err)
	}
	detail := request(t, a.Handler(), "GET", fmt.Sprintf("/api/admin/mail/masters/%d", id), nil, cookie, csrf)
	var result struct {
		Master MailMaster `json:"master"`
		Raw    string     `json:"raw"`
	}
	if detail.Code != http.StatusOK || json.Unmarshal(detail.Body.Bytes(), &result) != nil || result.Raw != raw || result.Master.Incomplete {
		t.Fatalf("detail did not decrypt material: %d %s", detail.Code, detail.Body.String())
	}
	if !strings.Contains(detail.Body.String(), `"folder_id"`) || !strings.Contains(detail.Body.String(), `"created_at"`) || strings.Contains(detail.Body.String(), `"folderId"`) {
		t.Fatalf("master DTO is not snake_case: %s", detail.Body.String())
	}
	list := request(t, a.Handler(), "GET", "/api/admin/mail/masters?inventory=incomplete", nil, cookie, csrf)
	var page struct{ Total int }
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &page) != nil || page.Total != 0 {
		t.Fatalf("complete encrypted material matched incomplete filter: %s", list.Body.String())
	}
	if strings.Contains(list.Body.String(), "private-application-password") || strings.Contains(list.Body.String(), `"raw"`) {
		t.Fatal("master list exposed private material")
	}
	updated := request(t, a.Handler(), "PUT", fmt.Sprintf("/api/admin/mail/masters/%d", id), map[string]any{"raw": "master@example.com", "folder_id": 0}, cookie, csrf)
	if updated.Code != http.StatusOK {
		t.Fatalf("update master: %d %s", updated.Code, updated.Body.String())
	}
	if err := a.store.db.QueryRow("SELECT raw,raw_complete FROM mailboxes WHERE id=?", id).Scan(&stored, &complete); err != nil || complete || stored == "master@example.com" {
		t.Fatalf("updated material encryption/completeness: complete=%v err=%v", complete, err)
	}
	list = request(t, a.Handler(), "GET", "/api/admin/mail/masters?inventory=incomplete", nil, cookie, csrf)
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &page) != nil || page.Total != 1 {
		t.Fatalf("incomplete filter lost encrypted material: %s", list.Body.String())
	}
}

func TestMailInventoryLegacyMaterialMigration(t *testing.T) {
	a := testApp(t, "primary")
	raw := "legacy@example.com----legacy-secret"
	result, err := a.store.db.Exec("INSERT INTO mailboxes(email,kind,raw,created_at,updated_at) VALUES(?,'master',?,?,?)", "legacy@example.com", raw, stampNow(), stampNow())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	outbound, err := a.store.db.Exec("INSERT INTO mail_outbounds(order_no,mode,status,actor,created_at) VALUES('legacy-order','sell','sold','admin',?)", stampNow())
	if err != nil {
		t.Fatal(err)
	}
	outboundID, _ := outbound.LastInsertId()
	if _, err := a.store.db.Exec("INSERT INTO mail_outbound_masters(outbound_id,master_id,email,raw,position) VALUES(?,?,?,?,0)", outboundID, id, "legacy@example.com", raw); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.migrateMailRawEncryption(); err != nil {
			t.Fatal(err)
		}
	}
	var stored, snapshot string
	var complete bool
	if err := a.store.db.QueryRow("SELECT raw,raw_complete FROM mailboxes WHERE id=?", id).Scan(&stored, &complete); err != nil {
		t.Fatal(err)
	}
	if err := a.store.db.QueryRow("SELECT raw FROM mail_outbound_masters WHERE outbound_id=?", outboundID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	for _, ciphertext := range []string{stored, snapshot} {
		plain, err := unseal(a.key, ciphertext)
		if err != nil || string(plain) != raw || strings.Contains(ciphertext, "legacy-secret") {
			t.Fatalf("legacy material was not migrated: %v", err)
		}
	}
	if !complete {
		t.Fatal("migration did not preserve raw completeness")
	}
	if _, err := a.mailMasterRaw("broken-encrypted-value"); err == nil {
		t.Fatal("corrupt ciphertext was treated as legacy plaintext")
	}
}

func TestMailInventoryChildLinkEnrichmentIsAtomic(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	masterID := createInventoryMaster(t, a, cookie, csrf, "master@example.com----secret")
	result, err := a.store.db.Exec("INSERT INTO mailboxes(email,kind,parent_id,note,created_at,updated_at) VALUES(?,'child',?,'preserve',?,?)", "child@example.com", masterID, stampNow(), stampNow())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	path := fmt.Sprintf("/api/admin/mail/children/%d", id)
	link := "https://example.com/receive/child"
	w := request(t, a.Handler(), "PUT", path, map[string]any{"link": link}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("enrich child: %d %s", w.Code, w.Body.String())
	}
	var note, storedLink string
	if err := a.store.db.QueryRow("SELECT note,link FROM mailboxes WHERE id=?", id).Scan(&note, &storedLink); err != nil || note != "preserve" || storedLink != link {
		t.Fatalf("link enrichment changed note or missed link: %q %q %v", note, storedLink, err)
	}
	listed := request(t, a.Handler(), "GET", "/api/admin/mail/children?flag=sellable", nil, cookie, csrf)
	var page struct{ Items []MailChild }
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &page) != nil || len(page.Items) != 1 || !page.Items[0].Sellable {
		t.Fatalf("enriched child not sellable: %s", listed.Body.String())
	}
	for _, field := range []string{"parent_id", "parent_email", "after_sale", "created_at", "link_check", "inbox_check", "outbound_id"} {
		if !strings.Contains(listed.Body.String(), `"`+field+`"`) {
			t.Fatalf("missing snake_case field %s: %s", field, listed.Body.String())
		}
	}
	for _, status := range []string{"reserved", "sold"} {
		if _, err := a.store.db.Exec("UPDATE mailboxes SET status=? WHERE id=?", status, id); err != nil {
			t.Fatal(err)
		}
		w = request(t, a.Handler(), "PUT", path, map[string]any{"link": "https://example.com/changed", "note": "must rollback"}, cookie, csrf)
		if w.Code != http.StatusConflict {
			t.Fatalf("%s child link change: %d %s", status, w.Code, w.Body.String())
		}
		if err := a.store.db.QueryRow("SELECT note,link FROM mailboxes WHERE id=?", id).Scan(&note, &storedLink); err != nil || note != "preserve" || storedLink != link {
			t.Fatalf("rejected link change was not atomic: %q %q %v", note, storedLink, err)
		}
	}
	w = request(t, a.Handler(), "PUT", path, map[string]any{"note": "sold note"}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("note-only edit of sold child: %d %s", w.Code, w.Body.String())
	}
	var changes int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_history WHERE mailbox_id=? AND action='mail.child.updated'", id).Scan(&changes); err != nil || changes != 2 {
		t.Fatalf("child edit history mismatch: %d %v", changes, err)
	}
}

func TestMailInventoryRoutesRequireAdminAndCSRF(t *testing.T) {
	a := testApp(t, "primary")
	path := "/api/admin/mail/masters"
	body := map[string]any{"raw": "admin@example.com----secret", "folder_id": 0}
	if w := request(t, a.Handler(), "POST", path, body, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated inventory write: %d", w.Code)
	}
	cookie, _ := loginFixture(t, a)
	if w := request(t, a.Handler(), "POST", path, body, cookie, ""); w.Code != http.StatusForbidden {
		t.Fatalf("inventory write without CSRF: %d", w.Code)
	}
	replica := testApp(t, "replica")
	if w := request(t, replica.Handler(), "POST", path, body, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("replica inventory write: %d", w.Code)
	}
}
