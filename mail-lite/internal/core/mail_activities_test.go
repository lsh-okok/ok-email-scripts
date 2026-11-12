package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestMailActivitiesSurviveInventoryDeletion(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	master := createInventoryMaster(t, a, cookie, csrf, "audit@example.com----audit-private-secret")
	child := insertOutboundTestChild(t, a, master, "audit-child@example.com", "https://receive.example/audit-private-link")
	for _, export := range []struct {
		id     int64
		kind   string
		format string
	}{{master, "master", "txt"}, {child, "child", "csv"}} {
		w := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/export", map[string]any{
			"ids": []int64{export.id, export.id}, "kind": export.kind, "format": export.format,
		}, cookie, csrf)
		if w.Code != http.StatusOK {
			t.Fatalf("%s export: %d %s", export.kind, w.Code, w.Body.String())
		}
	}
	preview := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{
		"ids": []int64{master}, "action": "preview_delete",
	}, cookie, csrf)
	var impact mailDeleteImpact
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &impact) != nil || impact.Version == "" {
		t.Fatalf("preview delete: %d %s", preview.Code, preview.Body.String())
	}
	stale := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{
		"ids": []int64{master}, "action": "delete", "version": "stale",
	}, cookie, csrf)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale delete: %d %s", stale.Code, stale.Body.String())
	}
	deleted := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{
		"ids": []int64{master}, "action": "delete", "version": impact.Version,
	}, cookie, csrf)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	for _, table := range []string{"mailboxes", "mail_history"} {
		var count int
		if err := a.store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s remained after deletion: count=%d err=%v", table, count, err)
		}
	}
	w := request(t, a.Handler(), http.MethodGet, "/api/admin/mail/activities?limit=2", nil, cookie, csrf)
	var page struct {
		Items []MailActivity `json:"items"`
		Total int            `json:"total"`
		Page  int            `json:"page"`
		Limit int            `json:"limit"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 3 || page.Page != 1 || page.Limit != 2 || len(page.Items) != 2 {
		t.Fatalf("activities after deletion: %d %s", w.Code, w.Body.String())
	}
	deletedActivity, childExport := page.Items[0], page.Items[1]
	if deletedActivity.Action != "mail.masters.deleted" || deletedActivity.Scope != "master" || deletedActivity.Format != "" || deletedActivity.SelectionCount != 1 || deletedActivity.MasterCount != 1 || deletedActivity.ChildCount != 1 || deletedActivity.Actor != "admin" || deletedActivity.CreatedAt == "" {
		t.Fatalf("incorrect delete activity: %+v", deletedActivity)
	}
	if childExport.Action != "mail.exported" || childExport.Scope != "child" || childExport.Format != "csv" || childExport.SelectionCount != 1 || childExport.MasterCount != 0 || childExport.ChildCount != 1 || childExport.ID >= deletedActivity.ID {
		t.Fatalf("incorrect child export activity: %+v", childExport)
	}
	for _, secret := range []string{"audit@example.com", "audit-child@example.com", "audit-private-secret", "audit-private-link", "https://receive.example"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("activity exposed inventory material: %s", secret)
		}
	}
	w = request(t, a.Handler(), http.MethodGet, "/api/admin/mail/activities?page=2&limit=2", nil, cookie, csrf)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 3 || page.Page != 2 || len(page.Items) != 1 {
		t.Fatalf("second activity page: %d %s", w.Code, w.Body.String())
	}
	masterExport := page.Items[0]
	if masterExport.Action != "mail.exported" || masterExport.Scope != "master" || masterExport.Format != "txt" || masterExport.SelectionCount != 1 || masterExport.MasterCount != 1 || masterExport.ChildCount != 1 || masterExport.ID >= childExport.ID {
		t.Fatalf("incorrect master export activity: %+v", masterExport)
	}
}

func TestMailActivitiesRollbackWithOperations(t *testing.T) {
	t.Run("delete failure rolls back its audit", func(t *testing.T) {
		a := testApp(t, "primary")
		cookie, csrf := loginFixture(t, a)
		master := createInventoryMaster(t, a, cookie, csrf, "rollback@example.com----private-secret")
		insertOutboundTestChild(t, a, master, "rollback-child@example.com", "https://receive.example/rollback")
		preview := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{
			"ids": []int64{master}, "action": "preview_delete",
		}, cookie, csrf)
		var impact mailDeleteImpact
		if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &impact) != nil || impact.Version == "" {
			t.Fatalf("preview delete: %d %s", preview.Code, preview.Body.String())
		}
		if _, err := a.store.db.Exec(`CREATE TRIGGER reject_test_delete BEFORE DELETE ON mailboxes
		 BEGIN SELECT RAISE(ABORT,'test delete failure'); END`); err != nil {
			t.Fatal(err)
		}
		w := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{
			"ids": []int64{master}, "action": "delete", "version": impact.Version,
		}, cookie, csrf)
		if w.Code == http.StatusOK {
			t.Fatalf("failed deletion reported success: %s", w.Body.String())
		}
		var activities, inventory int
		if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_activities").Scan(&activities); err != nil || activities != 0 {
			t.Fatalf("failed deletion persisted audit: count=%d err=%v", activities, err)
		}
		if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mailboxes").Scan(&inventory); err != nil || inventory != 2 {
			t.Fatalf("failed deletion changed inventory: count=%d err=%v", inventory, err)
		}
	})
	t.Run("export requires an audit", func(t *testing.T) {
		a := testApp(t, "primary")
		cookie, csrf := loginFixture(t, a)
		master := createInventoryMaster(t, a, cookie, csrf, "noaudit@example.com----export-private-secret")
		if _, err := a.store.db.Exec(`CREATE TRIGGER reject_test_audit BEFORE INSERT ON mail_activities
		 BEGIN SELECT RAISE(ABORT,'test audit failure'); END`); err != nil {
			t.Fatal(err)
		}
		w := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/export", map[string]any{
			"ids": []int64{master}, "kind": "master", "format": "txt",
		}, cookie, csrf)
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "export-private-secret") || strings.Contains(w.Body.String(), `"content"`) {
			t.Fatalf("export returned material without an audit: %d %s", w.Code, w.Body.String())
		}
	})
}

func TestMailActivitiesRequireAdminSession(t *testing.T) {
	a := testApp(t, "primary")
	w := request(t, a.Handler(), http.MethodGet, "/api/admin/mail/activities", nil, nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated activities: %d %s", w.Code, w.Body.String())
	}
}

func mailActivityCountForTest(t *testing.T, a *App, action string) int {
	t.Helper()
	var count int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_activities WHERE ?='' OR action=?", action, action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertMailActivityCounts(t *testing.T, a *App, action, scope string, selection, masters, children int) {
	t.Helper()
	var actualScope string
	var actualSelection, actualMasters, actualChildren int
	if err := a.store.db.QueryRow(`SELECT scope,selection_count,master_count,child_count FROM mail_activities
	 WHERE action=? ORDER BY id DESC LIMIT 1`, action).Scan(&actualScope, &actualSelection, &actualMasters, &actualChildren); err != nil {
		t.Fatal(err)
	}
	if actualScope != scope || actualSelection != selection || actualMasters != masters || actualChildren != children {
		t.Fatalf("%s summary: scope=%s selection=%d masters=%d children=%d", action, actualScope, actualSelection, actualMasters, actualChildren)
	}
}

func rejectMailActivityForTest(t *testing.T, a *App, action string) {
	t.Helper()
	if _, err := a.store.db.Exec(fmt.Sprintf(`CREATE TRIGGER reject_test_activity BEFORE INSERT ON mail_activities
	 WHEN NEW.action='%s' BEGIN SELECT RAISE(ABORT,'test audit failure'); END`, action)); err != nil {
		t.Fatal(err)
	}
}

func allowMailActivityForTest(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.store.db.Exec("DROP TRIGGER reject_test_activity"); err != nil {
		t.Fatal(err)
	}
}

func TestMailActivitiesImportAndMoveAreAtomic(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	existing := createInventoryMaster(t, a, cookie, csrf, "existing-audit@example.com")
	if _, err := a.store.db.Exec(`INSERT INTO mailboxes(email,kind,link,status,abnormal,created_at,updated_at)
	 VALUES('orphan-audit@example.com','child','https://receive.example/old','sold',1,?,?)`, stampNow(), stampNow()); err != nil {
		t.Fatal(err)
	}
	preview := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/import/preview", map[string]any{"files": []map[string]string{
		{"name": "first.txt", "content": "new-audit@example.com"},
		{"name": "second.txt", "content": "new-audit@example.com----import-private-secret\nnew-audit-child@example.com----https://receive.example/new-private-link\norphan-audit@example.com----https://receive.example/restored-private-link"},
		{"name": "third.txt", "content": "existing-audit@example.com----completed-private-secret"},
	}}, cookie, csrf)
	var p struct {
		Token string `json:"token"`
	}
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &p) != nil || p.Token == "" {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	if count := mailActivityCountForTest(t, a, ""); count != 0 {
		t.Fatalf("preview wrote activities: %d", count)
	}
	rejectMailActivityForTest(t, a, "mail.imported")
	w := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/import/commit", map[string]string{"token": p.Token}, cookie, csrf)
	if w.Code == http.StatusOK {
		t.Fatalf("import succeeded without audit: %s", w.Body.String())
	}
	var inventory, tokens int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mailboxes").Scan(&inventory); err != nil || inventory != 2 {
		t.Fatalf("failed import changed inventory: count=%d err=%v", inventory, err)
	}
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_import_previews WHERE token=?", digest(p.Token)).Scan(&tokens); err != nil || tokens != 1 {
		t.Fatalf("failed import consumed preview token: count=%d err=%v", tokens, err)
	}
	if count := mailActivityCountForTest(t, a, ""); count != 0 {
		t.Fatalf("failed import left audit: %d", count)
	}
	allowMailActivityForTest(t, a)
	w = mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/import/commit", map[string]string{"token": p.Token}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("retry import: %d %s", w.Code, w.Body.String())
	}
	assertMailActivityCounts(t, a, "mail.imported", "master", 3, 2, 2)
	w = mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/import/commit", map[string]string{"token": p.Token}, cookie, csrf)
	if w.Code != http.StatusConflict || mailActivityCountForTest(t, a, "mail.imported") != 1 {
		t.Fatalf("repeated commit duplicated audit: %d %s", w.Code, w.Body.String())
	}
	folderResponse := request(t, a.Handler(), http.MethodPost, "/api/admin/mail/folders", map[string]string{"name": "Audit destination"}, cookie, csrf)
	var folder MailFolder
	if folderResponse.Code != http.StatusCreated || json.Unmarshal(folderResponse.Body.Bytes(), &folder) != nil {
		t.Fatalf("create folder: %d %s", folderResponse.Code, folderResponse.Body.String())
	}
	move := map[string]any{"ids": []int64{existing, existing}, "action": "move", "folder_id": folder.ID}
	rejectMailActivityForTest(t, a, "mail.masters.moved")
	w = mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", move, cookie, csrf)
	if w.Code == http.StatusOK {
		t.Fatalf("move succeeded without audit: %s", w.Body.String())
	}
	var folderID, movedHistory int64
	if err := a.store.db.QueryRow("SELECT COALESCE(folder_id,0) FROM mailboxes WHERE id=?", existing).Scan(&folderID); err != nil || folderID != 0 {
		t.Fatalf("failed move changed folder: id=%d err=%v", folderID, err)
	}
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_history WHERE action='mail.masters.moved'").Scan(&movedHistory); err != nil || movedHistory != 0 {
		t.Fatalf("failed move left mailbox history: count=%d err=%v", movedHistory, err)
	}
	allowMailActivityForTest(t, a)
	w = mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", move, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("retry move: %d %s", w.Code, w.Body.String())
	}
	assertMailActivityCounts(t, a, "mail.masters.moved", "master", 1, 1, 0)
	if mailActivityCountForTest(t, a, "") != 2 {
		t.Fatal("failed or repeated operations added extra activities")
	}
	w = request(t, a.Handler(), http.MethodGet, "/api/admin/mail/activities", nil, cookie, csrf)
	for _, material := range []string{"@example.com", "private-secret", "private-link", "receive.example", "Audit destination"} {
		if strings.Contains(w.Body.String(), material) {
			t.Fatalf("activity exposed import or move details: %s", material)
		}
	}
}

func TestMailActivitiesOutboundLifecycleIsAtomicAndIdempotent(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	master := createInventoryMaster(t, a, cookie, csrf, "outbound-audit@example.com----outbound-private-secret")
	child := insertOutboundTestChild(t, a, master, "outbound-audit-1@example.com", "https://receive.example/outbound-private-link1")
	insertOutboundTestChild(t, a, master, "outbound-audit-2@example.com", "https://receive.example/outbound-private-link2")
	body := map[string]any{"master_ids": []int64{master}, "quantity": 2, "mode": "reserve", "selection": "random", "request_key": "audit-lifecycle-order", "customer": "private-customer", "note": "private-note"}
	rejectMailActivityForTest(t, a, "mail.outbound.created")
	w := outboundRequest(t, a, http.MethodPost, "/api/admin/mail/outbounds", body, cookie, csrf)
	if w.Code == http.StatusCreated {
		t.Fatalf("create succeeded without audit: %s", w.Body.String())
	}
	var orders, unavailable int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_outbounds").Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("failed creation left order: count=%d err=%v", orders, err)
	}
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE status<>'available'").Scan(&unavailable); err != nil || unavailable != 0 {
		t.Fatalf("failed creation reserved inventory: count=%d err=%v", unavailable, err)
	}
	allowMailActivityForTest(t, a)
	w = outboundRequest(t, a, http.MethodPost, "/api/admin/mail/outbounds", body, cookie, csrf)
	var created struct{ ID int64 }
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.ID == 0 {
		t.Fatalf("retry create: %d %s", w.Code, w.Body.String())
	}
	assertMailActivityCounts(t, a, "mail.outbound.created", "child", 2, 1, 2)
	w = outboundRequest(t, a, http.MethodPost, "/api/admin/mail/outbounds", body, cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reused":true`) || mailActivityCountForTest(t, a, "mail.outbound.created") != 1 {
		t.Fatalf("idempotent creation duplicated audit: %d %s", w.Code, w.Body.String())
	}
	body["quantity"] = 1
	w = outboundRequest(t, a, http.MethodPost, "/api/admin/mail/outbounds", body, cookie, csrf)
	if w.Code != http.StatusConflict || mailActivityCountForTest(t, a, "mail.outbound.created") != 1 {
		t.Fatalf("conflicting creation duplicated audit: %d %s", w.Code, w.Body.String())
	}
	var firstItem, secondItem int64
	if err := a.store.db.QueryRow("SELECT MIN(id),MAX(id) FROM mail_outbound_items WHERE outbound_id=?", created.ID).Scan(&firstItem, &secondItem); err != nil {
		t.Fatal(err)
	}
	checkState := func(id int64, expectedStatus string, expectedReturns, expectedHistory int) {
		t.Helper()
		var status string
		var returns, history int
		if err := a.store.db.QueryRow(`SELECT status,
		 (SELECT COUNT(*) FROM mail_outbound_items WHERE outbound_id=? AND returned_at IS NOT NULL),
		 (SELECT COUNT(*) FROM mail_outbound_history WHERE outbound_id=?) FROM mail_outbounds WHERE id=?`, id, id, id).Scan(&status, &returns, &history); err != nil {
			t.Fatal(err)
		}
		if status != expectedStatus || returns != expectedReturns || history != expectedHistory {
			t.Fatalf("order state: status=%s returns=%d history=%d", status, returns, history)
		}
	}
	for i, step := range []struct {
		action, before, after              string
		ids                                []int64
		beforeReturns, afterReturns, count int
	}{
		{"deliver", "reserved", "sold", nil, 0, 0, 2},
		{"return", "sold", "partial_return", []int64{firstItem}, 0, 1, 1},
		{"return", "partial_return", "returned", []int64{secondItem}, 1, 2, 1},
	} {
		payload := map[string]any{"action": step.action, "reason": "private-reason", "item_ids": step.ids}
		path := fmt.Sprintf("/api/admin/mail/outbounds/%d/action", created.ID)
		beforeCount := mailActivityCountForTest(t, a, "")
		rejectMailActivityForTest(t, a, "mail.outbound."+step.action)
		w = outboundRequest(t, a, http.MethodPost, path, payload, cookie, csrf)
		if w.Code == http.StatusOK || mailActivityCountForTest(t, a, "") != beforeCount {
			t.Fatalf("failed %s left an activity: %d %s", step.action, w.Code, w.Body.String())
		}
		checkState(created.ID, step.before, step.beforeReturns, i+1)
		allowMailActivityForTest(t, a)
		w = outboundRequest(t, a, http.MethodPost, path, payload, cookie, csrf)
		if w.Code != http.StatusOK {
			t.Fatalf("retry %s: %d %s", step.action, w.Code, w.Body.String())
		}
		checkState(created.ID, step.after, step.afterReturns, i+2)
		assertMailActivityCounts(t, a, "mail.outbound."+step.action, "child", step.count, 1, step.count)
		w = outboundRequest(t, a, http.MethodPost, path, payload, cookie, csrf)
		if w.Code != http.StatusConflict || mailActivityCountForTest(t, a, "") != beforeCount+1 {
			t.Fatalf("repeated %s duplicated audit: %d %s", step.action, w.Code, w.Body.String())
		}
	}
	cancelBody := map[string]any{"ids": []int64{child}, "quantity": 1, "mode": "reserve", "request_key": "audit-cancel-order"}
	w = outboundRequest(t, a, http.MethodPost, "/api/admin/mail/outbounds", cancelBody, cookie, csrf)
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("create cancel order: %d %s", w.Code, w.Body.String())
	}
	path := fmt.Sprintf("/api/admin/mail/outbounds/%d/action", created.ID)
	cancel := map[string]any{"action": "cancel", "reason": "private-reason"}
	rejectMailActivityForTest(t, a, "mail.outbound.cancel")
	w = outboundRequest(t, a, http.MethodPost, path, cancel, cookie, csrf)
	if w.Code == http.StatusOK || mailActivityCountForTest(t, a, "mail.outbound.cancel") != 0 {
		t.Fatalf("failed cancel left an activity: %d %s", w.Code, w.Body.String())
	}
	checkState(created.ID, "reserved", 0, 1)
	allowMailActivityForTest(t, a)
	w = outboundRequest(t, a, http.MethodPost, path, cancel, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("retry cancel: %d %s", w.Code, w.Body.String())
	}
	checkState(created.ID, "cancelled", 0, 2)
	assertMailActivityCounts(t, a, "mail.outbound.cancel", "child", 1, 1, 1)
	w = outboundRequest(t, a, http.MethodPost, path, cancel, cookie, csrf)
	if w.Code != http.StatusConflict || mailActivityCountForTest(t, a, "") != 6 {
		t.Fatalf("repeated cancel duplicated audit: %d %s", w.Code, w.Body.String())
	}
	w = request(t, a.Handler(), http.MethodGet, "/api/admin/mail/activities", nil, cookie, csrf)
	for _, material := range []string{"@example.com", "private-secret", "private-link", "private-customer", "private-note", "private-reason"} {
		if strings.Contains(w.Body.String(), material) {
			t.Fatalf("activity exposed outbound details: %s", material)
		}
	}
}
