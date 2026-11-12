package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	stdsmtp "net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	smtp "github.com/emersion/go-smtp"
	"mail-lite/web"
)

func mxFixture(t *testing.T, a *App, domain string) Account {
	t.Helper()
	v := Account{ID: randomID(), Provider: "mx", MXDomain: domain, Status: "active"}
	if err := validateAccount(&v); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Save(context.Background(), v, nil); err != nil {
		t.Fatal(err)
	}
	v, err := a.store.Account(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mxSMTPApp(t *testing.T) *App {
	t.Helper()
	a, err := NewApp(Config{Role: "primary", DataDir: t.TempDir(), AdminPassword: "test-password-12345", SMTPEnabled: true, SMTPListen: "127.0.0.1:0", SMTPHostname: "mx.example.test"}, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Start(context.Background())
	return a
}
func mxRaw(subject string) []byte {
	return []byte("From: Sender <verify@sender.test>\r\nTo: forged@example.test\r\nDelivered-To: forged@example.test\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n验证码 123456\r\n")
}
func mxDeliver(t *testing.T, a *App, account Account, recipient, subject string, at time.Time) {
	t.Helper()
	if err := a.mx.deliver(context.Background(), map[string]Account{account.ID: account}, map[string][]string{account.ID: {recipient}}, mxRaw(subject), at); err != nil {
		t.Fatal(err)
	}
}
func mxClient(t *testing.T, a *App) *stdsmtp.Client {
	t.Helper()
	conn, err := net.DialTimeout("tcp", a.smtpListener.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	client, err := stdsmtp.NewClient(conn, "mx.example.test")
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}
func mxSend(t *testing.T, a *App, raw []byte, recipients ...string) {
	t.Helper()
	client := mxClient(t, a)
	if err := client.Mail("sender@sender.test"); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	client.Quit()
}

type mxAPIPage struct {
	Items      []Message `json:"items"`
	NextCursor string    `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
	Recipient  string    `json:"recipient"`
	Order      string    `json:"order"`
}

func mxAPI(t *testing.T, a *App, token, query string) mxAPIPage {
	t.Helper()
	w := request(t, a.Handler(), "GET", "/api/mailboxes/"+token+"/messages"+query, nil, nil, "")
	if w.Code != 200 {
		t.Fatalf("API %d: %s", w.Code, w.Body.String())
	}
	var page mxAPIPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "envelope") || strings.Contains(w.Body.String(), "recipients\"") {
		t.Fatal("envelope recipients exposed")
	}
	return page
}

func TestMXSMTPAndRecipientIsolation(t *testing.T) {
	a := mxSMTPApp(t)
	account := mxFixture(t, a, "Example.Test.")
	hidden := linkFixture(t, a, account.ID, "hidden+tag@example.test")
	forged := linkFixture(t, a, account.ID, "forged@example.test")
	second := linkFixture(t, a, account.ID, "another@example.test")
	if len(mxAPI(t, a, hidden.Token, "?details=1").Items) != 0 {
		t.Fatal("new link must be empty")
	}
	mxSend(t, a, mxRaw("=?UTF-8?B?6aqM6K+B56CB?="), hidden.Recipient, second.Recipient, hidden.Recipient)
	page := mxAPI(t, a, hidden.Token, "?limit=1&details=1")
	if len(page.Items) != 1 || page.Items[0].Subject != "验证码" || !strings.Contains(page.Items[0].Text, "123456") {
		t.Fatalf("message: %+v", page)
	}
	if len(mxAPI(t, a, forged.Token, "?details=1").Items) != 0 {
		t.Fatal("forged To/Delivered-To leaked mail")
	}
	if len(mxAPI(t, a, second.Token, "?details=1").Items) != 1 {
		t.Fatal("multi-recipient delivery missing")
	}
	rawRef := page.Items[0].Ref
	ref, err := parseRef(a.key, rawRef, account.ID)
	if err != nil || ref.Protocol != "mx" {
		t.Fatal(ref, err)
	}
	if w := request(t, a.Handler(), "GET", "/api/mailboxes/"+forged.Token+"/messages?ref="+url.QueryEscape(rawRef), nil, nil, ""); w.Code != 404 {
		t.Fatalf("cross-recipient ref returned %d", w.Code)
	}
	if got := mxAPI(t, a, forged.Token, "?recipient="+hidden.Recipient); len(got.Items) != 0 {
		t.Fatal("query overrode fixed recipient")
	}
	if w := request(t, a.Handler(), "GET", "/api/mailboxes/p0/"+account.PublicCode+"/hidden+tag/messages?details=1", nil, nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "123456") {
		t.Fatal(w.Code, w.Body.String())
	}
	client := mxClient(t, a)
	if err := client.Mail(""); err != nil {
		t.Fatal("null sender must be accepted", err)
	}
	if err := client.Rcpt("user@outside.test"); err == nil || !strings.Contains(err.Error(), "550") {
		t.Fatal("relay not rejected", err)
	}
	client.Close()
	if _, err = a.store.Save(context.Background(), Link{ID: randomID(), AccountID: account.ID, Recipient: "other@outside.test", TokenHash: randomToken()}, nil); err == nil {
		t.Fatal("cross-domain link accepted")
	}
	account.Status = "disabled"
	if _, err = a.store.Save(context.Background(), account, &account.Version); err != nil {
		t.Fatal(err)
	}
	client = mxClient(t, a)
	client.Mail("sender@sender.test")
	if err := client.Rcpt(hidden.Recipient); err == nil {
		t.Fatal("disabled domain accepted mail")
	}
	client.Close()
	if w := request(t, a.Handler(), "GET", "/api/mailboxes/"+hidden.Token+"/messages", nil, nil, ""); w.Code == 200 {
		t.Fatal("disabled domain allowed reads")
	}
}
func TestMXPaginationRetentionAndPlatform(t *testing.T) {
	a := testApp(t, "primary")
	account := mxFixture(t, a, "pages.test")
	link := linkFixture(t, a, account.ID, "inbox@pages.test")
	for i := 0; i < 25; i++ {
		mxDeliver(t, a, account, link.Recipient, fmt.Sprintf("mail-%02d", i), time.Now())
	}
	page := mxAPI(t, a, link.Token, "?limit=20")
	if len(page.Items) != 20 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first page %+v", page)
	}
	mxDeliver(t, a, account, link.Recipient, "new arrival", time.Now())
	next := mxAPI(t, a, link.Token, "?limit=20&cursor="+url.QueryEscape(page.NextCursor))
	if len(next.Items) != 5 || next.HasMore {
		t.Fatalf("snapshot moved: %+v", next)
	}
	seen := map[string]bool{}
	for _, m := range append(page.Items, next.Items...) {
		if seen[m.MessageID] || m.Subject == "new arrival" {
			t.Fatal("duplicate/new mail in snapshot")
		}
		seen[m.MessageID] = true
	}
	oldest := mxAPI(t, a, link.Token, "?limit=1&order=oldest")
	if oldest.Items[0].Subject != "mail-00" {
		t.Fatal(oldest)
	}
	cookie, csrf := loginFixture(t, a)
	admin := request(t, a.Handler(), "GET", "/api/admin/accounts/"+account.ID+"/messages?limit=20", nil, cookie, csrf)
	if admin.Code != 200 {
		t.Fatal(admin.Body.String())
	}
	var ap adminMailPage
	json.Unmarshal(admin.Body.Bytes(), &ap)
	if len(ap.Items) != 20 || !ap.HasMore {
		t.Fatal("admin paging", admin.Body.String())
	}
	admin = request(t, a.Handler(), "GET", "/api/admin/accounts/"+account.ID+"/messages?cursor="+url.QueryEscape(ap.NextCursor), nil, cookie, csrf)
	json.Unmarshal(admin.Body.Bytes(), &ap)
	if admin.Code != 200 || len(ap.Items) != 6 {
		t.Fatal("admin next", admin.Body.String())
	}
	junk := request(t, a.Handler(), "GET", "/api/admin/accounts/"+account.ID+"/messages?folder=junkemail", nil, cookie, csrf)
	if junk.Code != 200 || !strings.Contains(junk.Body.String(), `"items":[]`) {
		t.Fatal(junk.Body.String())
	}
	platform := Platform{ID: randomID(), Name: "验证码", Domains: []string{"sender.test"}, Keywords: []string{"mail-00"}}
	if _, err := a.store.Save(context.Background(), platform, nil); err != nil {
		t.Fatal(err)
	}
	filtered := linkFixture(t, a, account.ID, "filtered@pages.test")
	filtered.PlatformID = platform.ID
	if _, err := a.store.Save(context.Background(), filtered, nil); err != nil {
		t.Fatal(err)
	}
	mxDeliver(t, a, account, filtered.Recipient, "mail-00", time.Now())
	for i := 0; i < 25; i++ {
		mxDeliver(t, a, account, filtered.Recipient, "other", time.Now())
	}
	if got := mxAPI(t, a, filtered.Token, "?limit=1&details=1"); len(got.Items) != 1 || got.Items[0].Subject != "mail-00" {
		t.Fatal("filter applied after limit", got)
	}
	expired := linkFixture(t, a, account.ID, "old@pages.test")
	mxDeliver(t, a, account, expired.Recipient, "expired", time.Now().Add(-31*24*time.Hour))
	if len(mxAPI(t, a, expired.Token, "").Items) != 0 {
		t.Fatal("expired mail visible before cleanup")
	}
	if err := a.cleanMX(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	a.mx.db.QueryRow("SELECT count(*) FROM mx_messages WHERE received<?", time.Now().Add(-30*24*time.Hour).UnixMicro()).Scan(&n)
	if n != 0 {
		t.Fatal("retention did not clean")
	}
	forever := 0
	account.MXRetentionDays = &forever
	if _, err := a.store.Save(context.Background(), account, &account.Version); err != nil {
		t.Fatal(err)
	}
	mxDeliver(t, a, account, expired.Recipient, "forever", time.Now().Add(-100*24*time.Hour))
	if len(mxAPI(t, a, expired.Token, "").Items) != 1 {
		t.Fatal("permanent mail expired")
	}
	link.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.store.Save(context.Background(), link, nil); err != nil {
		t.Fatal(err)
	}
	if w := request(t, a.Handler(), "GET", "/api/mailboxes/"+link.Token+"/messages", nil, nil, ""); w.Code != 410 {
		t.Fatal("expired link", w.Code)
	}
}
func TestMXPersistenceEncryptionAndCleanup(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Role: "primary", DataDir: dir, AdminPassword: "test-password-12345"}
	a, err := NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	account := mxFixture(t, a, "persist.test")
	link := linkFixture(t, a, account.ID, "any@persist.test")
	mxDeliver(t, a, account, link.Recipient, "private-subject", time.Now())
	var raw, data string
	if err = a.mx.db.QueryRow("SELECT raw,data FROM mx_messages").Scan(&raw, &data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "123456") || strings.Contains(data, "private-subject") {
		t.Fatal("plaintext persisted")
	}
	plain, err := unseal(a.key, raw)
	if err != nil || !bytes.Contains(plain, []byte("123456")) {
		t.Fatal("raw not recoverable", err)
	}
	a.Close()
	a, err = NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got := mxAPI(t, a, link.Token, "?details=1")
	if len(got.Items) != 1 {
		t.Fatal("mail lost on restart")
	}
	if err = a.store.Delete(context.Background(), "account", []string{account.ID}); err != nil {
		t.Fatal(err)
	}
	if err = a.cleanMX(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	a.mx.db.QueryRow("SELECT count(*) FROM mx_messages").Scan(&n)
	if n != 0 {
		t.Fatal("orphan mail not removed")
	}
}
func TestMXAdminImportDeleteAndSales(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	w := request(t, a.Handler(), "POST", "/api/admin/accounts", map[string]any{"provider": "mx", "mx_domain": "Admin.Test"}, cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var account Account
	json.Unmarshal(w.Body.Bytes(), &account)
	if account.Email != "postmaster@admin.test" || mxDays(account) != 30 {
		t.Fatal(account)
	}
	duplicate := request(t, a.Handler(), "POST", "/api/admin/accounts", map[string]any{"provider": "mx", "mx_domain": "admin.test."}, cookie, csrf)
	if duplicate.Code == 200 {
		t.Fatal("duplicate domain accepted")
	}
	bad := account
	bad.MXDomain = "changed.test"
	if request(t, a.Handler(), "POST", "/api/admin/accounts", bad, cookie, csrf).Code == 200 {
		t.Fatal("domain changed")
	}
	line := mailAccountRaw(account)
	parsed, err := parseAccountLine(line)
	if err != nil || parsed.MXDomain != account.MXDomain || mxDays(parsed) != 30 {
		t.Fatal(line, parsed, err)
	}
	export := request(t, a.Handler(), "POST", "/api/admin/accounts/export", map[string]any{}, cookie, csrf)
	if export.Code != 200 || !strings.Contains(export.Body.String(), line) {
		t.Fatal(export.Body.String())
	}
	job := Job{}
	a.runLinksImport(context.Background(), &job, ImportRequest{AccountID: account.ID, Files: []ImportFile{{Name: "aliases", Text: "first@admin.test\nwrong@outside.test\nsecond@admin.test"}}})
	if job.Failed != 1 {
		t.Fatal(job)
	}
	var linkCount int
	a.store.db.QueryRow("SELECT count(*) FROM records WHERE kind='link' AND account_id=?", account.ID).Scan(&linkCount)
	if linkCount != 2 {
		t.Fatal("link import count", linkCount)
	}
	link := linkFixture(t, a, account.ID, "delete@admin.test")
	mxDeliver(t, a, account, link.Recipient, "delete me", time.Now())
	items := mxAPI(t, a, link.Token, "?details=1").Items
	mixed := []string{items[0].Ref, signRef(a.key, MessageRef{AccountID: account.ID, Protocol: "graph", ID: "fake", Folder: "inbox"})}
	if _, err := a.mail.Delete(context.Background(), account, mixed); err == nil {
		t.Fatal("mixed protocol accepted")
	}
	if len(mxAPI(t, a, link.Token, "").Items) != 1 {
		t.Fatal("invalid request partially deleted mail")
	}
	deletion := request(t, a.Handler(), "POST", "/api/admin/accounts/"+account.ID+"/messages/delete", map[string]any{"refs": []string{items[0].Ref}}, cookie, csrf)
	if deletion.Code != 200 {
		t.Fatal(deletion.Body.String())
	}
	if len(mxAPI(t, a, link.Token, "").Items) != 0 {
		t.Fatal("delete did not take effect")
	}
	mapping := request(t, a.Handler(), "POST", "/api/admin/mail/mapping", map[string]any{"account_ids": []string{account.ID}, "include_links": true}, cookie, csrf)
	if mapping.Code != 200 {
		t.Fatal(mapping.Body.String())
	}
	var mapped int
	a.store.db.QueryRow("SELECT count(*) FROM mail_source_mappings WHERE source_kind='link'").Scan(&mapped)
	if mapped != 3 {
		t.Fatal("sales link mappings", mapped, mapping.Body.String())
	}
	overview := request(t, a.Handler(), "GET", "/api/admin/accounts/"+account.ID+"/mx", nil, cookie, csrf)
	if overview.Code != 200 || !strings.Contains(overview.Body.String(), `"domain":"admin.test"`) {
		t.Fatal(overview.Body.String())
	}
}
func TestMXReplicaReadAndRevocation(t *testing.T) {
	primary := testApp(t, "primary")
	account := mxFixture(t, primary, "nodes.test")
	link := linkFixture(t, primary, account.ID, "user@nodes.test")
	mxDeliver(t, primary, account, link.Recipient, "node mail", time.Now())
	node := Node{ID: "mx-node", Token: randomToken()}
	if err := primary.store.AuxPut("node", node.ID, node); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(primary.Handler())
	defer server.Close()
	replica := testApp(t, "replica")
	replica.cfg.MasterURL = server.URL
	replica.cfg.NodeID = node.ID
	replica.cfg.NodeToken = node.Token
	if err := replica.syncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := mxAPI(t, replica, link.Token, "?details=1")
	if len(got.Items) != 1 || got.Items[0].Subject != "node mail" {
		t.Fatal(got)
	}
	if _, err := parseRef(replica.key, got.Items[0].Ref, account.ID); err != nil {
		t.Fatal("ref not signed by replica", err)
	}
	if _, err := parseRef(primary.key, got.Items[0].Ref, account.ID); err == nil {
		t.Fatal("master ref leaked")
	}
	if _, err := os.Stat(filepath.Join(replica.cfg.DataDir, "mx-mail.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("replica stored mail", err)
	}
	mxDeliver(t, primary, account, link.Recipient, "immediate arrival", time.Now())
	if mxAPI(t, replica, link.Token, "?limit=1").Items[0].Subject != "immediate arrival" {
		t.Fatal("stale replica cache")
	}
	node.Revoked = true
	primary.store.AuxPut("node", node.ID, node)
	w := request(t, replica.Handler(), "GET", "/api/mailboxes/"+link.Token+"/messages", nil, nil, "")
	if w.Code != 503 {
		t.Fatal("revoked node read", w.Code, w.Body.String())
	}
	// Restore and resync, then ensure a disconnected primary cannot return cached mail.
	node.Revoked = false
	primary.store.AuxPut("node", node.ID, node)
	if err := replica.syncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if w = request(t, replica.Handler(), "GET", "/api/mailboxes/"+link.Token+"/messages", nil, nil, ""); w.Code != 503 {
		t.Fatal("disconnected primary", w.Code, w.Body.String())
	}
}
func TestMXSMTPFailuresAndMIME(t *testing.T) {
	a := testApp(t, "primary")
	account := mxFixture(t, a, "failure.test")
	session := &mxSMTPSession{app: a}
	session.Mail("sender@sender.test", nil)
	if err := session.Rcpt("user@failure.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := session.Data(bytes.NewReader(bytes.Repeat([]byte("x"), mxMaxMessageBytes+1))); err == nil {
		t.Fatal("oversize accepted")
	} else {
		var smtpErr *smtp.SMTPError
		if !errors.As(err, &smtpErr) || smtpErr.Code != 552 {
			t.Fatal(err)
		}
	}
	raw := []byte("From: verify@sender.test\r\nSubject: MIME\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=test\r\n\r\n--test\r\nContent-Type: text/plain; charset=gbk\r\nContent-Transfer-Encoding: base64\r\n\r\n0enWpMK7IDEyMzQ1Ng==\r\n--test\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>123456</p><script>alert(1)</script>\r\n--test\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=x.bin\r\n\r\nattachment\r\n--test--\r\n")
	parsed, err := parseMX(raw, time.Now())
	if err != nil || !strings.Contains(parsed.Text, "123456") || strings.Contains(parsed.HTML, "<script") {
		t.Fatal(parsed, err)
	}
	if err = session.Data(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.mx.db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	// Force every SQLite connection read-only so no successful SMTP ACK can precede persistence.
	a.mx.db.SetMaxOpenConns(1)
	err = session.Data(bytes.NewReader(mxRaw("must fail")))
	var smtpErr *smtp.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 451 {
		t.Fatal("storage failure was acknowledged", err)
	}
	if _, err = a.mx.db.Exec("PRAGMA query_only=OFF"); err != nil {
		t.Fatal(err)
	}
	var n int
	a.mx.db.QueryRow("SELECT count(*) FROM mx_messages WHERE account_id=?", account.ID).Scan(&n)
	if n != 1 {
		t.Fatal("failed delivery persisted", n)
	}
}
func TestMXSTARTTLS(t *testing.T) {
	// Reuse httptest's generated certificate without adding persistent test secrets.
	source := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer source.Close()
	certificate := source.TLS.Certificates[0]
	a := mxSMTPApp(t)
	a.smtpServer.TLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	mxFixture(t, a, "tls.test")
	roots := x509.NewCertPool()
	cert, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(cert)
	client := mxClient(t, a)
	if ok, _ := client.Extension("STARTTLS"); !ok {
		t.Fatal("STARTTLS not advertised")
	}
	if err := client.StartTLS(&tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12}); err != nil {
		t.Fatal(err)
	}
	if err := client.Mail("sender@sender.test"); err != nil {
		t.Fatal(err)
	}
	if err := client.Rcpt("user@tls.test"); err != nil {
		t.Fatal(err)
	}
	client.Quit()
}

func TestMXLimitsConfigurationAndSharedService(t *testing.T) {
	a := mxSMTPApp(t)
	account := mxFixture(t, a, "service.test")
	mxFixture(t, a, "second.test")
	mxSend(t, a, mxRaw("two domains"), "first@service.test", "second@second.test")
	listed, err := a.mail.List(context.Background(), account)
	if err != nil || len(listed) != 1 {
		t.Fatal("shared list used upstream", listed, err)
	}
	latest, err := a.mail.LatestRecipient(context.Background(), account, "first@service.test")
	if err != nil || len(latest) != 1 || latest[0].Text == "" {
		t.Fatal("shared latest", latest, err)
	}
	client := mxClient(t, a)
	if err := client.Hello("limits.test"); err != nil {
		t.Fatal(err)
	}
	if err := client.Text.PrintfLine("MAIL FROM:<sender@sender.test> SIZE=%d", mxMaxMessageBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Text.ReadResponse(552); err != nil {
		t.Fatal("oversize SIZE must be permanent rejection", err)
	}
	client.Close()
	for _, domain := range []string{"127.0.0.1", "example..test", "-example.test", "http://example.test"} {
		if _, err := mxDomain(domain); err == nil {
			t.Fatal("invalid domain", domain)
		}
	}
	for _, line := range []string{"postmaster@example.test----MX-----1", "postmaster@example.test----MX----bad"} {
		if _, err := parseAccountLine(line); err == nil {
			t.Fatal("bad retention imported", line)
		}
	}
	t.Setenv("NODE_ROLE", "primary")
	t.Setenv("APP_ORIGIN", "")
	t.Setenv("SMTP_ENABLED", "true")
	t.Setenv("SMTP_HOSTNAME", "mx.example.test")
	t.Setenv("SMTP_LISTEN_ADDR", "127.0.0.1:2525")
	t.Setenv("SMTP_TLS_CERT", "")
	t.Setenv("SMTP_TLS_KEY", "")
	cfg, err := LoadConfig()
	if err != nil || !cfg.SMTPEnabled || cfg.SMTPListen != "127.0.0.1:2525" {
		t.Fatal(cfg, err)
	}
	t.Setenv("SMTP_TLS_CERT", "certificate.pem")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("unpaired SMTP certificate accepted")
	}
	t.Setenv("SMTP_TLS_CERT", "")
	t.Setenv("SMTP_HOSTNAME", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("empty SMTP hostname accepted")
	}
}
