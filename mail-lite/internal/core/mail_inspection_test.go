package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mail-lite/web"
)

func inspectionDecode[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP %d, want %d: %s", w.Code, status, w.Body.String())
	}
	var value T
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func inspectionStock(t *testing.T, a *App, cookie *http.Cookie, csrf, link string, count int) []int64 {
	t.Helper()
	master := createInventoryMaster(t, a, cookie, csrf, "inspection-master@example.com----material")
	ids := make([]int64, count)
	for i := range ids {
		res, err := a.store.db.Exec("INSERT INTO mailboxes(email,kind,parent_id,link,created_at,updated_at) VALUES(?,'child',?,?,?,?)", fmt.Sprintf("inspection-%d@example.com", i), master, link, stampNow(), stampNow())
		if err != nil {
			t.Fatal(err)
		}
		ids[i], err = res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func inspectionSender(t *testing.T, a *App, cookie *http.Cookie, csrf, email string, enabled bool) mailSenderResponse {
	t.Helper()
	return inspectionDecode[mailSenderResponse](t, request(t, a.Handler(), "POST", "/api/admin/mail/senders", mailSenderInput{Host: "smtp.example.com", Port: 465, From: email, Password: "private-smtp-password", Security: "ssl", Enabled: enabled}, cookie, csrf), http.StatusCreated)
}

func waitInspectionTask(t *testing.T, a *App, id int64) inspectionTask {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		task, err := scanInspection(a.store.db.QueryRow("SELECT "+inspectionColumns+" FROM mail_inspections WHERE id=?", id))
		if err != nil {
			t.Fatal(err)
		}
		a.inspectionMu.Lock()
		_, running := a.inspectionJobs[id]
		a.inspectionMu.Unlock()
		if !running && task.Status != "running" && task.Status != "queued" {
			return task
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("inspection worker did not finish")
	return inspectionTask{}
}

func inspectionTestOptions() inspectionOptions {
	return inspectionOptions{Timeout: 1, Concurrency: 1, Wait: 5, Interval: 200}
}

func TestMailInspectionInboxRetryNeverResendsAmbiguousDelivery(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	var sent atomic.Int32
	var tokenMu sync.Mutex
	token := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenMu.Lock()
		defer tokenMu.Unlock()
		fmt.Fprint(w, token)
	}))
	defer server.Close()
	a.inspectionHTTP = server.Client()
	a.inspectionSend = func(_ context.Context, _ smtpSettings, to, value string) error {
		sent.Add(1)
		tokenMu.Lock()
		token = value
		tokenMu.Unlock()
		return fmt.Errorf("delivery acknowledgement lost")
	}
	ids := inspectionStock(t, a, cookie, csrf, server.URL, 1)
	inspectionSender(t, a, cookie, csrf, "sender@example.com", true)
	in := inspectionInput{Mode: "inbox", Scope: "all", ConfirmSend: true, Options: inspectionTestOptions()}
	created := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/inspections", in, cookie, csrf), 201)
	if task := waitInspectionTask(t, a, created.ID); task.Pending != 1 || task.Processed != 1 {
		t.Fatalf("ambiguous send must stay pending: %+v", task)
	}
	var persisted string
	if err := a.store.db.QueryRow("SELECT test_token FROM mail_inspection_results WHERE inspection_id=?", created.ID).Scan(&persisted); err != nil || persisted == "" {
		t.Fatalf("token must be committed before SMTP: %q %v", persisted, err)
	}
	retryPath := fmt.Sprintf("/api/admin/mail/inspections/%d/retry", created.ID)
	retried := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", retryPath, nil, cookie, csrf), 201)
	if task := waitInspectionTask(t, a, retried.ID); task.Passed != 1 || task.Processed != 1 {
		t.Fatalf("retry failed to find the original token: %+v", task)
	}
	if sent.Load() != 1 {
		t.Fatalf("retry resent email: %d sends", sent.Load())
	}
	detail := request(t, a.Handler(), "GET", fmt.Sprintf("/api/admin/mail/inspections/%d", retried.ID), nil, cookie, csrf)
	if detail.Code != 200 || strings.Contains(detail.Body.String(), persisted) || !strings.Contains(detail.Body.String(), `"sender_email"`) {
		t.Fatalf("detail exposed token or lost sender: %s", detail.Body.String())
	}
	for _, change := range []struct {
		sql   string
		value any
	}{{"UPDATE mailboxes SET status=? WHERE id=?", "sold"}, {"UPDATE mailboxes SET status='available',link=? WHERE id=?", server.URL + "/changed"}} {
		if _, err := a.store.db.Exec(change.sql, change.value, ids[0]); err != nil {
			t.Fatal(err)
		}
		if w := request(t, a.Handler(), "POST", retryPath, nil, cookie, csrf); w.Code != 400 {
			t.Fatalf("retry accepted sold or changed stock: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestMailInspectionStopCancelsSMTPAndQueuedItems(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	inspectionStock(t, a, cookie, csrf, "https://receive.example.com/check", 2)
	inspectionSender(t, a, cookie, csrf, "sender@example.com", true)
	entered := make(chan struct{}, 1)
	var sent atomic.Int32
	a.inspectionSend = func(ctx context.Context, _ smtpSettings, _, _ string) error {
		sent.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	created := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/inspections", inspectionInput{Mode: "inbox", Scope: "all", ConfirmSend: true, Options: inspectionTestOptions()}, cookie, csrf), 201)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP mock never started")
	}
	stopPath := fmt.Sprintf("/api/admin/mail/inspections/%d/stop", created.ID)
	inspectionDecode[map[string]bool](t, request(t, a.Handler(), "POST", stopPath, nil, cookie, csrf), 200)
	if task := waitInspectionTask(t, a, created.ID); task.Status != "stopped" || task.Processed != 0 {
		t.Fatalf("stop was overwritten: %+v", task)
	}
	if sent.Load() != 1 {
		t.Fatalf("queued emails sent after stop: %d", sent.Load())
	}
	var interrupted int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_inspection_results WHERE inspection_id=? AND phase='interrupted'", created.ID).Scan(&interrupted); err != nil || interrupted != 2 {
		t.Fatalf("unfinished stages not interrupted: %d %v", interrupted, err)
	}
	if w := request(t, a.Handler(), "POST", stopPath, nil, cookie, csrf); w.Code != 409 {
		t.Fatalf("finished stop must conflict: %d %s", w.Code, w.Body.String())
	}
}

func TestMailInspectionContextCancellationStopsSchedulerAndWorkers(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	inspectionStock(t, a, cookie, csrf, "https://receive.example.com/check", 1)
	inspectionSender(t, a, cookie, csrf, "sender@example.com", true)
	entered := make(chan struct{}, 1)
	a.inspectionSend = func(ctx context.Context, _ smtpSettings, _, _ string) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.startInspectionScheduler(ctx)
	created := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/inspections", inspectionInput{Mode: "inbox", Scope: "all", ConfirmSend: true, Options: inspectionTestOptions()}, cookie, csrf), 201)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP mock never started")
	}
	cancel()
	if task := waitInspectionTask(t, a, created.ID); task.Status != "failed" {
		t.Fatalf("canceled context left worker running: %+v", task)
	}
	done := make(chan struct{})
	go func() { a.schedulerWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler ignored parent cancellation")
	}
	if id, err := a.runScheduledInspection(time.Now(), true); id != 0 || err == nil {
		t.Fatal("scheduler accepted work after cancellation", id, err)
	}
}

func TestMailInspectionDailyClaimAndSenderRotationSurviveRestart(t *testing.T) {
	cfg := Config{Role: "primary", DataDir: t.TempDir(), AdminPassword: "test-password-12345"}
	a, err := NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if a != nil {
			a.Close()
		}
	}()
	cookie, csrf := loginFixture(t, a)
	var mu sync.Mutex
	tokens := ""
	used := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); defer mu.Unlock(); fmt.Fprint(w, tokens) }))
	defer server.Close()
	install := func() {
		a.inspectionHTTP = server.Client()
		a.inspectionSend = func(_ context.Context, sender smtpSettings, _, token string) error {
			mu.Lock()
			defer mu.Unlock()
			used = append(used, sender.From)
			tokens += token + "\n"
			return nil
		}
	}
	install()
	inspectionStock(t, a, cookie, csrf, server.URL, 1)
	inspectionSender(t, a, cookie, csrf, "a@example.com", true)
	inspectionSender(t, a, cookie, csrf, "disabled@example.com", false)
	inspectionSender(t, a, cookie, csrf, "b@example.com", true)
	inspectionDecode[inspectionSchedule](t, request(t, a.Handler(), "PUT", "/api/admin/mail/inspection-schedule", inspectionScheduleInput{Enabled: true, RunAt: "23:59", Scope: "all", Options: inspectionTestOptions()}, cookie, csrf), 200)
	now, _ := time.Parse(time.RFC3339, "2040-06-01T15:59:00Z")
	if id, err := a.runScheduledInspection(now.Add(-time.Minute), false); id != 0 || err != nil {
		t.Fatal("ran before Shanghai daily time", id, err)
	}
	var wg sync.WaitGroup
	var created []int64
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := a.runScheduledInspection(now, false)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Error(err)
			}
			if id != 0 {
				created = append(created, id)
			}
		}()
	}
	wg.Wait()
	if len(created) != 1 {
		t.Fatalf("daily slot created %d tasks", len(created))
	}
	if task := waitInspectionTask(t, a, created[0]); task.Passed != 1 {
		t.Fatal(task)
	}
	a.Close()
	a, err = NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	install()
	if id, err := a.runScheduledInspection(now, false); id != 0 || err != nil {
		t.Fatal("restart repeated the daily claim", id, err)
	}
	id, err := a.runScheduledInspection(now, true)
	if err != nil || id == 0 {
		t.Fatal("manual run failed", id, err)
	}
	if task := waitInspectionTask(t, a, id); task.Passed != 1 {
		t.Fatal(task)
	}
	mu.Lock()
	got := strings.Join(used, ",")
	mu.Unlock()
	if got != "a@example.com,b@example.com" {
		t.Fatal("rotation lost position or included disabled sender", got)
	}
}

type inspectionRoundTripper func(*http.Request) (*http.Response, error)

func (f inspectionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMailInspectionHTTPClassificationAndSSRFProtection(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.18.0.1", "240.0.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "2001:db8::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if publicIP(net.ParseIP(ip)) {
			t.Errorf("nonpublic IP accepted: %s", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(net.ParseIP(ip)) {
			t.Errorf("public IP rejected: %s", ip)
		}
	}
	if conn, err := publicDial(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		conn.Close()
		t.Fatal("public dial permitted loopback")
	}
	for _, raw := range []string{"file:///tmp/private", "http://user:pass@example.com", "http://127.0.0.1", "http://[::1]"} {
		u, _ := url.Parse(raw)
		if inspectURL(u) == nil {
			t.Errorf("unsafe URL accepted: %s", raw)
		}
	}
	var localHits atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { localHits.Add(1); fmt.Fprint(w, "secret") }))
	defer local.Close()
	client := newInspectionHTTPClient(1)
	defer client.CloseIdleConnections()
	if result, _, _ := checkInspectionLink(context.Background(), client, local.URL, 1, ""); result != "pending" || localHits.Load() != 0 {
		t.Fatal("inspection reached a loopback server", result, localHits.Load())
	}
	var redirected atomic.Int32
	client.Transport = inspectionRoundTripper(func(r *http.Request) (*http.Response, error) {
		redirected.Add(1)
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{local.URL}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	if _, err := client.Get("https://public.example.com"); err == nil || redirected.Load() != 1 {
		t.Fatal("redirect escaped address check", err, redirected.Load())
	}
	for _, tc := range []struct {
		code              int
		token, body, want string
	}{{200, "", "ok", "passed"}, {403, "", "denied", "failed"}, {429, "", "later", "pending"}, {503, "", "later", "pending"}, {200, "test-token", "test-token", "passed"}, {200, "test-token", "older mail", "pending"}} {
		mock := &http.Client{Transport: inspectionRoundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
		})}
		if result, code, _ := checkInspectionLink(context.Background(), mock, "https://public.example.com", 1, tc.token); result != tc.want || code != tc.code {
			t.Errorf("HTTP %d classified as %s/%d, want %s", tc.code, result, code, tc.want)
		}
	}
}

func TestMailInspectionValidationAndPrimaryOnly(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	if w := request(t, a.Handler(), "POST", "/api/admin/mail/inspections", inspectionInput{Mode: "inbox", Scope: "all"}, cookie, csrf); w.Code != 400 {
		t.Fatal("inbox send accepted without confirmation", w.Code)
	}
	if w := request(t, a.Handler(), "GET", "/api/admin/mail/inspections/not-an-id", nil, cookie, csrf); w.Code != 400 {
		t.Fatal("invalid inspection id accepted", w.Code)
	}
	if w := request(t, a.Handler(), "POST", "/api/admin/mail/senders", mailSenderInput{}, cookie, ""); w.Code != 403 {
		t.Fatal("sender mutation accepted without CSRF", w.Code)
	}
	for _, methodPath := range [][2]string{{"POST", "/api/admin/mail/inspections"}, {"POST", "/api/admin/mail/senders"}, {"PUT", "/api/admin/mail/inspection-schedule"}} {
		if w := request(t, a.Handler(), methodPath[0], methodPath[1], map[string]any{}, nil, ""); w.Code != 401 {
			t.Fatal("unauthenticated sales API allowed", methodPath, w.Code)
		}
	}
	replica := testApp(t, "replica")
	if _, err := replica.runScheduledInspection(time.Now(), true); err == nil {
		t.Fatal("replica scheduled a sales inspection")
	}
	if w := request(t, replica.Handler(), "POST", "/api/admin/mail/inspections", map[string]any{}, nil, ""); w.Code != 403 {
		t.Fatal("replica exposed inspection writes", w.Code)
	}
	for _, handler := range []http.HandlerFunc{replica.createMailInspection, replica.saveMailSender, replica.saveInspectionSchedule} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", "/", strings.NewReader("{}")))
		if w.Code != 403 {
			t.Fatal("direct writer bypassed primary guard", w.Code)
		}
	}
}

func TestMailInspectionResultsAreIdempotentAndStoppedQueueDoesNotRestart(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	ids := inspectionStock(t, a, cookie, csrf, "https://receive.example.com/check", 1)
	items := []inspectionResult{{MailboxID: &ids[0], Email: "inspection-0@example.com", Link: "https://receive.example.com/check"}}
	tx, err := a.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	id, err := insertInspection(tx, "link", "all", 0, inspectionTestOptions(), items, "admin")
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE mail_inspections SET status='running' WHERE id=?", id); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.saveInspectionResult(id, items[0], "passed", 200, "ok"); err != nil {
			t.Fatal(err)
		}
	}
	task, err := scanInspection(a.store.db.QueryRow("SELECT "+inspectionColumns+" FROM mail_inspections WHERE id=?", id))
	if err != nil || task.Processed != 1 || task.Passed != 1 {
		t.Fatalf("duplicate result incremented counters: %+v %v", task, err)
	}
	inspectionDecode[map[string]bool](t, request(t, a.Handler(), "POST", fmt.Sprintf("/api/admin/mail/inspections/%d/stop", id), nil, cookie, csrf), 200)
	var requests atomic.Int32
	a.inspectionHTTP = &http.Client{Transport: inspectionRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("unexpected request")
	})}
	a.inspectionMu.Lock()
	a.launchInspectionWithSenders(id, "link", inspectionTestOptions(), nil, items, false)
	a.inspectionMu.Unlock()
	if task := waitInspectionTask(t, a, id); task.Status != "stopped" || requests.Load() != 0 {
		t.Fatalf("stopped task restarted: %+v requests=%d", task, requests.Load())
	}
}

func TestMailSMTPPasswordsRemainEncryptedAndUpdatesPreserveCredentials(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	sender := inspectionSender(t, a, cookie, csrf, "sender@example.com", true)
	var ciphertext string
	if err := a.store.db.QueryRow("SELECT password_ciphertext FROM mail_smtp_senders WHERE id=?", sender.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	plain, err := unseal(a.key, ciphertext)
	if err != nil || string(plain) != "private-smtp-password" || strings.Contains(ciphertext, "private-smtp-password") {
		t.Fatal("sender password did not use parent encryption", err)
	}
	updated := mailSenderInput{Host: sender.Host, Port: sender.Port, From: sender.From, Security: sender.Security, Enabled: false}
	inspectionDecode[mailSenderResponse](t, request(t, a.Handler(), "PUT", fmt.Sprintf("/api/admin/mail/senders/%d", sender.ID), updated, cookie, csrf), 200)
	if _, err := a.smtpSendersForInspection(smtpSettings{}); err == nil {
		t.Fatal("disabled sender remained eligible")
	}
	updated.Enabled = true
	inspectionDecode[mailSenderResponse](t, request(t, a.Handler(), "PUT", fmt.Sprintf("/api/admin/mail/senders/%d", sender.ID), updated, cookie, csrf), 200)
	pool, err := a.smtpSendersForInspection(smtpSettings{})
	if err != nil || len(pool) != 1 || pool[0].Password != "private-smtp-password" {
		t.Fatal("blank update lost saved password", err)
	}
	if w := request(t, a.Handler(), "GET", "/api/admin/mail/senders", nil, cookie, csrf); strings.Contains(w.Body.String(), "private-smtp-password") || strings.Contains(w.Body.String(), ciphertext) {
		t.Fatal("sender list exposed credentials")
	}
	for _, bad := range []smtpSettings{{Host: "127.0.0.1", Port: 465, From: "sender@example.com", Security: "ssl", Password: "secret"}, {Host: "100.64.0.1", Port: 465, From: "sender@example.com", Security: "ssl", Password: "secret"}, {Host: "smtp.example.com", Port: 25, From: "sender@example.com", Security: "none", Password: "secret"}} {
		if err := validateSMTP(bad); err == nil {
			t.Fatal("unsafe SMTP configuration accepted", bad.Host, bad.Security)
		}
	}
	if err := sendInspectionMail(context.Background(), smtpSettings{Host: "smtp.example.com", Port: 465, From: "sender@example.com", Security: "ssl", Password: "secret"}, "receiver@example.com", "token\r\nBcc:other@example.com"); err == nil {
		t.Fatal("SMTP header injection accepted")
	}
}
