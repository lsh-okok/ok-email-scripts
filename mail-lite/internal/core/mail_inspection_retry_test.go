package core

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"mail-lite/web"
)

// Seed a finished task with original SMTP tokens; retries must only read them.
func pendingInspectionFixture(t *testing.T, a *App, cookie *http.Cookie, csrf string, count int) (int64, []int64) {
	t.Helper()
	ids := inspectionStock(t, a, cookie, csrf, "https://receive.example.com/check", count)
	tx, err := a.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	items, err := a.inspectionCandidates(tx, inspectionInput{Scope: "all"}, "https://mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		items[i].token = fmt.Sprintf("original-test-token-%d", *items[i].MailboxID)
		items[i].SenderEmail = "sender@example.com"
	}
	id, err := insertInspection(tx, "inbox", "all", 0, inspectionTestOptions(), items, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE mail_inspections SET status='completed',processed=total,pending=total WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE mail_inspection_results SET processed=1,phase='finished' WHERE inspection_id=?", id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id, ids
}

func TestMailInspectionRetryAccountsForSoldItemAndPersistsExplanation(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	id, ids := pendingInspectionFixture(t, a, cookie, csrf, 10)
	if _, err := a.store.db.Exec("UPDATE mailboxes SET status='sold' WHERE id=?", ids[0]); err != nil {
		t.Fatal(err)
	}
	var reads, sends atomic.Int32
	allTokens := ""
	for _, mailboxID := range ids {
		allTokens += fmt.Sprintf("original-test-token-%d\n", mailboxID)
	}
	a.inspectionHTTP = &http.Client{Transport: inspectionRoundTripper(func(r *http.Request) (*http.Response, error) {
		reads.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(allTokens)), Header: http.Header{}, Request: r}, nil
	})}
	a.inspectionSend = func(context.Context, smtpSettings, string, string) error { sends.Add(1); return nil }
	created := inspectionDecode[struct {
		ID        int64               `json:"id"`
		RetryInfo inspectionRetryInfo `json:"retry_info"`
	}](t, request(t, a.Handler(), "POST", fmt.Sprintf("/api/admin/mail/inspections/%d/retry", id), nil, cookie, csrf), 201)
	info := created.RetryInfo
	if info.SourceID != id || info.Requested != 10 || info.Retried != 9 || len(info.Skipped) != 1 || info.Skipped[0].Reason != "库存已售" || info.Skipped[0].ParentEmail != "inspection-master@example.com" {
		t.Fatalf("retry did not account for all original results: %+v", info)
	}
	if task := waitInspectionTask(t, a, created.ID); task.Total != 9 || task.Passed != 9 || reads.Load() != 9 || sends.Load() != 0 {
		t.Fatalf("retry ran the wrong items or resent SMTP: %+v reads=%d sends=%d", task, reads.Load(), sends.Load())
	}
	var reused int
	if err := a.store.db.QueryRow(`SELECT COUNT(*) FROM mail_inspection_results n JOIN mail_inspection_results old
 ON n.mailbox_id=old.mailbox_id AND n.test_token=old.test_token
 WHERE old.inspection_id=? AND n.inspection_id=?`, id, created.ID).Scan(&reused); err != nil || reused != 9 {
		t.Fatalf("retry did not preserve original tokens: %d %v", reused, err)
	}
	// The summary is a snapshot, including the reason at task creation time.
	if _, err := a.store.db.Exec("UPDATE mailboxes SET status='available' WHERE id=?", ids[0]); err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg
	a.Close()
	reopened, err := NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cookie, csrf = loginFixture(t, reopened)
	detail := inspectionDecode[struct {
		Results   []inspectionResult  `json:"results"`
		RetryInfo inspectionRetryInfo `json:"retry_info"`
	}](t, request(t, reopened.Handler(), "GET", fmt.Sprintf("/api/admin/mail/inspections/%d", created.ID), nil, cookie, csrf), 200)
	if detail.RetryInfo.Requested != 10 || detail.RetryInfo.Retried != 9 || len(detail.RetryInfo.Skipped) != 1 || detail.RetryInfo.Skipped[0].Reason != "库存已售" {
		t.Fatalf("retry summary changed or disappeared after restart: %+v", detail.RetryInfo)
	}
	for _, result := range detail.Results {
		if result.ParentEmail != "inspection-master@example.com" {
			t.Fatalf("detail lost the parent mailbox: %+v", result)
		}
	}
}

func TestMailInspectionRetryReportsEveryEligibilityExclusion(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	id, ids := pendingInspectionFixture(t, a, cookie, csrf, 5)
	for i, change := range []string{
		"UPDATE mailboxes SET status='reserved' WHERE id=?",
		"DELETE FROM mailboxes WHERE id=?",
		"UPDATE mailboxes SET link='' WHERE id=?",
		"UPDATE mailboxes SET link='https://changed.example.com' WHERE id=?",
	} {
		if _, err := a.store.db.Exec(change, ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	// No tokens may be generated or SMTP sent for the remaining read-only retry.
	a.inspectionHTTP = &http.Client{Transport: inspectionRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf("original-test-token-%d", ids[4]))), Header: http.Header{}, Request: r}, nil
	})}
	created := inspectionDecode[struct {
		ID        int64               `json:"id"`
		RetryInfo inspectionRetryInfo `json:"retry_info"`
	}](t, request(t, a.Handler(), "POST", fmt.Sprintf("/api/admin/mail/inspections/%d/retry", id), nil, cookie, csrf), 201)
	if created.RetryInfo.Requested != 5 || created.RetryInfo.Retried != 1 || len(created.RetryInfo.Skipped) != 4 {
		t.Fatalf("retry silently lost results: %+v", created.RetryInfo)
	}
	want := map[string]bool{"库存已预留": true, "库存已删除": true, "接码链接不可用或待补全": true, "链接或来源策略已变化，请新建检测": true}
	for _, skipped := range created.RetryInfo.Skipped {
		if !want[skipped.Reason] || skipped.ParentEmail != "inspection-master@example.com" {
			t.Fatalf("incorrect exclusion or lost parent snapshot: %+v", skipped)
		}
		delete(want, skipped.Reason)
	}
	if len(want) != 0 {
		t.Fatalf("exclusions not explained: %+v", want)
	}
	if task := waitInspectionTask(t, a, created.ID); task.Passed != 1 {
		t.Fatal(task)
	}
}

func TestMailInspectionParentMailboxHistoryAndCSV(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	id, ids := pendingInspectionFixture(t, a, cookie, csrf, 2)
	// An old row has no parent snapshot; recover its parent with the current relation.
	if _, err := a.store.db.Exec("UPDATE mail_inspection_results SET parent_email='' WHERE mailbox_id=?", ids[0]); err != nil {
		t.Fatal(err)
	}
	check := func() {
		detail := inspectionDecode[struct {
			Results []inspectionResult `json:"results"`
		}](t, request(t, a.Handler(), "GET", fmt.Sprintf("/api/admin/mail/inspections/%d", id), nil, cookie, csrf), 200)
		for _, result := range detail.Results {
			if result.ParentEmail != "inspection-master@example.com" {
				t.Fatalf("parent mailbox missing: %+v", result)
			}
		}
		export := inspectionDecode[struct{ Content string }](t, request(t, a.Handler(), "GET", fmt.Sprintf("/api/admin/mail/inspections/%d/export", id), nil, cookie, csrf), 200)
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(export.Content, "\ufeff"))).ReadAll()
		if err != nil || len(rows) != 3 || rows[0][1] != "parent_email" || rows[1][1] != "inspection-master@example.com" || rows[2][1] != "inspection-master@example.com" {
			t.Fatalf("report omitted parent mailboxes: %v %v", rows, err)
		}
	}
	check()
	// New snapshots survive deletion of inventory; old rows still use the fallback.
	if _, err := a.store.db.Exec("DELETE FROM mailboxes WHERE id=?", ids[1]); err != nil {
		t.Fatal(err)
	}
	check()
}
