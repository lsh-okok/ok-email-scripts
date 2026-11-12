package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerificationScheduleSynchronizesNewSourcesBeforeEachDailyRun(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	account := accountFixture(t, a, "verification-schedule")
	linkFixture(t, a, account.ID, "first@example.net")
	expired := linkFixture(t, a, account.ID, "expired@example.net")
	expired.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := a.store.Save(context.Background(), expired, nil); err != nil {
		t.Fatal(err)
	}
	inspectionSender(t, a, cookie, csrf, "sender@example.net", true)
	var sent atomic.Int32
	a.inspectionSend = func(context.Context, smtpSettings, string, string) error {
		sent.Add(1)
		return errors.New("test delivery acknowledgement unavailable")
	}
	inspectionDecode[inspectionSchedule](t, request(t, a.Handler(), http.MethodPut, "/api/admin/mail/inspection-schedule", inspectionScheduleInput{
		Enabled: true, RunAt: "09:00", Scope: "all", Sample: 0, Options: inspectionTestOptions(),
	}, cookie, csrf), http.StatusOK)
	now := time.Now().UTC().AddDate(1, 0, 0)
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	local := now.In(shanghai)
	now = time.Date(local.Year(), local.Month(), local.Day(), 9, 1, 0, 0, shanghai)
	id, err := a.runScheduledInspection(now, false)
	if err != nil || id == 0 {
		t.Fatalf("schedule did not import initial sources: id=%d err=%v", id, err)
	}
	if task := waitInspectionTask(t, a, id); task.Total != 1 || task.Pending != 1 || sent.Load() != 1 {
		t.Fatalf("unexpected first run: %+v, sends=%d", task, sent.Load())
	}
	linkFixture(t, a, account.ID, "newly-added@example.net")
	id, err = a.runScheduledInspection(now.AddDate(0, 0, 1), false)
	if err != nil || id == 0 {
		t.Fatalf("schedule did not import later sources: id=%d err=%v", id, err)
	}
	if task := waitInspectionTask(t, a, id); task.Total != 2 || task.Pending != 2 || sent.Load() != 3 {
		t.Fatalf("new child absent or expired child included: %+v, sends=%d", task, sent.Load())
	}
	var newlyMapped int
	if err := a.store.db.QueryRow("SELECT count(*) FROM mail_source_mappings sm JOIN mailboxes c ON c.id=sm.mailbox_id WHERE sm.source_kind='link' AND c.email='newly-added@example.net'").Scan(&newlyMapped); err != nil || newlyMapped != 1 {
		t.Fatalf("new source mapping absent: count=%d err=%v", newlyMapped, err)
	}
	account.Status = "disabled"
	if _, err := a.store.Save(context.Background(), account, nil); err != nil {
		t.Fatal(err)
	}
	if id, err = a.runScheduledInspection(now.AddDate(0, 0, 2), false); id != 0 || err == nil {
		t.Fatalf("disabled account remained in schedule: id=%d err=%v", id, err)
	}
	if sent.Load() != 3 {
		t.Fatalf("disabled source received mail: sends=%d", sent.Load())
	}
}

func TestVerificationSourceSynchronizationRepairsOnlyEmptyAvailableLinks(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "verification-repair")
	for i := range 3 {
		linkFixture(t, a, account.ID, fmt.Sprintf("repair-%d@example.net", i))
	}
	synchronize := func() mailMappingCommitResponse {
		t.Helper()
		a.store.write.Lock()
		defer a.store.write.Unlock()
		result, err := a.commitMailMappingLocked(context.Background(), mailMappingCommitRequest{}, "https://mail.example.net")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	synchronize()
	if _, err := a.store.db.Exec(`UPDATE mailboxes SET link_mode='custom',link='' WHERE kind='child';
 UPDATE mailboxes SET link='https://custom.example.net/mail' WHERE email='repair-1@example.net';
 UPDATE mailboxes SET status='reserved' WHERE email='repair-2@example.net';`); err != nil {
		t.Fatal(err)
	}
	result := synchronize()
	if result.AssociatedLinks != 1 || result.CreatedLinks != 0 {
		t.Fatalf("unexpected synchronization: %+v", result)
	}
	for _, tc := range []struct{ email, mode, link string }{
		{"repair-0@example.net", "source", ""},
		{"repair-1@example.net", "custom", "https://custom.example.net/mail"},
		{"repair-2@example.net", "custom", ""},
	} {
		var mode, link string
		if err := a.store.db.QueryRow("SELECT link_mode,link FROM mailboxes WHERE email=?", tc.email).Scan(&mode, &link); err != nil {
			t.Fatal(err)
		}
		if mode != tc.mode || link != tc.link {
			t.Errorf("%s changed unexpectedly: mode=%s link=%s", tc.email, mode, link)
		}
	}
	if again := synchronize(); again.AssociatedLinks != 0 {
		t.Fatalf("synchronization repeated existing work: %+v", again)
	}
}
