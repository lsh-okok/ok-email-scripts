package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func mappingPreviewForTest(t *testing.T, a *App, query string) mailMappingPreview {
	t.Helper()
	w := httptest.NewRecorder()
	a.mailMappingPreview(w, httptest.NewRequest("GET", "/api/admin/mail/mapping/preview"+query, nil))
	var out mailMappingPreview
	if w.Code != 200 {
		t.Fatalf("preview status %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mappingPreviewLinkFixture(t *testing.T, a *App, id, account, recipient, platform string) Link {
	t.Helper()
	link := Link{ID: id, AccountID: account, Recipient: recipient, PlatformID: platform, TokenHash: digest(id)}
	if _, err := a.store.Save(context.Background(), link, nil); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestMailMappingPreviewGlobalConflictsAcrossPages(t *testing.T) {
	a := testApp(t, "primary")
	first := accountFixture(t, a, "a-first")
	blocked := accountFixture(t, a, "m-blocked")
	last := accountFixture(t, a, "z-last")
	for _, id := range []string{"p1", "p2"} {
		if _, err := a.store.Save(context.Background(), Platform{ID: id, Name: id}, nil); err != nil {
			t.Fatal(err)
		}
	}
	mappingPreviewLinkFixture(t, a, "a-01", first.ID, "duplicate@example.com", "p1")
	mappingPreviewLinkFixture(t, a, "a-02", first.ID, "DUPLICATE@example.com", "p2")
	// A later parent's address is reserved even before that parent is processed.
	mappingPreviewLinkFixture(t, a, "a-03", first.ID, last.Email, "")
	mappingPreviewLinkFixture(t, a, "a-04", first.ID, " \t", "")
	mappingPreviewLinkFixture(t, a, "m-01", blocked.ID, "blocked-child@example.com", "")
	mappingPreviewLinkFixture(t, a, "z-01", last.ID, "last-child@example.com", "")
	if _, err := a.store.db.Exec(`INSERT INTO mailboxes(email,kind,created_at,updated_at) VALUES(?,'master',?,?)`, blocked.Email, stampNow(), stampNow()); err != nil {
		t.Fatal(err)
	}
	disabled := accountFixture(t, a, "disabled")
	disabled.Status = "disabled"
	if _, err := a.store.Save(context.Background(), disabled, nil); err != nil {
		t.Fatal(err)
	}
	mappingPreviewLinkFixture(t, a, "disabled-link", disabled.ID, "disabled-child@example.com", "")

	full := mappingPreviewForTest(t, a, "")
	if full.Summary.ActiveAccounts != 3 || full.Summary.Links != 5 || full.Summary.Conflicts != 4 || full.Total != 5 || len(full.Links) != 5 {
		t.Fatalf("wrong global preview: %+v", full)
	}
	wantIDs := []string{"a-01", "a-02", "a-03", "m-01", "z-01"}
	for index, id := range wantIDs {
		page := mappingPreviewForTest(t, a, fmt.Sprintf("?page=%d&limit=1&issues_only=1", index+1))
		if page.Summary != full.Summary || len(page.Accounts) != 3 || page.Total != 5 || len(page.Links) != 1 || page.Links[0].SourceID != id {
			t.Fatalf("page %d lost global information or order: %+v", index+1, page)
		}
		if page.Links[0] != full.Links[index] {
			t.Fatalf("page %d changed conflict evaluation: %+v != %+v", index+1, page.Links[0], full.Links[index])
		}
	}
	if full.Links[0].Conflict != "" || full.Links[1].Conflict == "" || full.Links[2].Conflict == "" || full.Links[3].Conflict != "主邮箱存在映射冲突" || full.Links[4].Conflict != "" {
		t.Fatalf("wrong duplicate or parent conflict priority: %+v", full.Links)
	}

	for _, test := range []struct {
		query       string
		page, limit int
		id          string
	}{
		{"?page=-10&limit=-2", 1, 1, "a-01"},
		{"?page=999999999&limit=2", 3, 2, "z-01"},
		{"?page=999999999999999999999999999999999&limit=1", 1, 1, "a-01"},
		{"?page=1&limit=9999", 1, 200, "a-01"},
	} {
		page := mappingPreviewForTest(t, a, test.query)
		if page.Page != test.page || page.Limit != test.limit || page.Total != 5 || page.Links[0].SourceID != test.id {
			t.Fatalf("unexpected bounds for %s: %+v", test.query, page)
		}
	}
}

func TestMailMappingPreviewSourceStatusAndFilteredPages(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "source-status")
	if _, err := a.store.Save(context.Background(), Platform{ID: "service", Name: "Service"}, nil); err != nil {
		t.Fatal(err)
	}
	ready := mappingPreviewLinkFixture(t, a, "01-ready", account.ID, "ready@example.com", "")
	expired := mappingPreviewLinkFixture(t, a, "02-expired", account.ID, "expired@example.com", "")
	policy := mappingPreviewLinkFixture(t, a, "03-policy", account.ID, "policy@example.com", "service")
	custom := mappingPreviewLinkFixture(t, a, "04-custom", account.ID, "custom@example.com", "")
	empty := mappingPreviewLinkFixture(t, a, "05-empty", account.ID, "empty@example.com", "")
	w := httptest.NewRecorder()
	a.mailMappingCommit(w, httptest.NewRequest("POST", "/", newJSONBody(t, mailMappingCommitRequest{})))
	if w.Code != 200 {
		t.Fatalf("commit status %d: %s", w.Code, w.Body.String())
	}
	for _, link := range []*Link{&expired, &policy} {
		link.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		if _, err := a.store.Save(context.Background(), *link, nil); err != nil {
			t.Fatal(err)
		}
	}
	// The unrestricted alternative takes precedence over the expired platform link.
	unrestricted := mappingPreviewLinkFixture(t, a, "06-unrestricted", account.ID, policy.Recipient, "")
	for _, value := range []struct{ id, link string }{{custom.ID, "https://example.com/mail"}, {empty.ID, ""}} {
		if _, err := a.store.db.Exec(`UPDATE mailboxes SET link_mode='custom',link=? WHERE id=(SELECT mailbox_id FROM mail_source_mappings WHERE source_kind='link' AND source_id=?)`, value.link, value.id); err != nil {
			t.Fatal(err)
		}
	}
	var parentID int64
	if err := a.store.db.QueryRow("SELECT mailbox_id FROM mail_source_mappings WHERE source_kind='account' AND source_id=?", account.ID).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	// A changed inventory parent must not replace the source account's mapping ID.
	res, err := a.store.db.Exec(`INSERT INTO mailboxes(email,kind,created_at,updated_at) VALUES('other-parent@example.com','master',?,?)`, stampNow(), stampNow())
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.db.Exec(`UPDATE mailboxes SET parent_id=? WHERE id=(SELECT mailbox_id FROM mail_source_mappings WHERE source_kind='link' AND source_id=?)`, otherID, custom.ID); err != nil {
		t.Fatal(err)
	}

	full := mappingPreviewForTest(t, a, "")
	if full.Summary.MappedAccounts != 1 || full.Summary.MappedLinks != 5 || full.Summary.Links != 6 || full.Summary.Conflicts != 1 {
		t.Fatalf("wrong mapped summary: %+v", full)
	}
	byID := map[string]mailMappingLink{}
	for _, item := range full.Links {
		byID[item.SourceID] = item
		if item.Mapped && item.ParentID != parentID {
			t.Fatalf("source parent mapping lost: %+v", item)
		}
	}
	for _, link := range []Link{ready, policy, custom} {
		if item := byID[link.ID]; !item.Mapped || item.NeedsDetectionLink || item.LinkReason != "" {
			t.Fatalf("ready/custom/effective policy reported as issue: %+v", item)
		}
	}
	if item := byID[expired.ID]; !item.Mapped || !item.NeedsDetectionLink || item.LinkReason != "取信链接已过期" {
		t.Fatalf("expired source not reported: %+v", item)
	}
	if item := byID[empty.ID]; !item.Mapped || !item.NeedsDetectionLink || item.LinkReason != "" {
		t.Fatalf("empty custom link not reported: %+v", item)
	}
	if item := byID[unrestricted.ID]; item.Mapped || item.Conflict == "" {
		t.Fatalf("unmapped duplicate not reported: %+v", item)
	}
	wantIssues := []string{expired.ID, empty.ID, unrestricted.ID}
	for i, id := range wantIssues {
		page := mappingPreviewForTest(t, a, fmt.Sprintf("?issues_only=1&page=%d&limit=1", i+1))
		if page.Total != 3 || page.Summary != full.Summary || len(page.Links) != 1 || page.Links[0].SourceID != id {
			t.Fatalf("wrong filtered page %d: %+v", i+1, page)
		}
	}
}

func TestMailMappingPreviewEmptyPage(t *testing.T) {
	a := testApp(t, "primary")
	for _, query := range []string{"", "?page=999&limit=50&issues_only=1", "?page=1&limit=99999999999999999999999999999999"} {
		page := mappingPreviewForTest(t, a, query)
		if page.Page != 1 || page.Total != 0 || page.Summary.ActiveAccounts != 0 || page.Summary.Links != 0 || page.Accounts == nil || page.Links == nil || len(page.Accounts) != 0 || len(page.Links) != 0 {
			t.Fatalf("unexpected empty response for %s: %+v", query, page)
		}
	}
}
