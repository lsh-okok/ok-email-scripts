package core

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"mail-lite/web"
)

func TestShortLinkMigrationStabilityAndLegacyCompatibility(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Role: "primary", DataDir: t.TempDir(), AdminPassword: "test-password-12345"}
	a, err := NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	main := accountFixture(t, a, randomID())
	link := linkFixture(t, a, main.ID, "recipient000@example.net")
	// Reproduce the schema and encrypted account payload from before short codes.
	main.PublicCode = ""
	raw, _ := json.Marshal(main)
	encrypted, err := seal(a.key, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.db.Exec("UPDATE records SET data=?,public_code='' WHERE kind='account' AND id=?", encrypted, main.ID); err != nil {
		t.Fatal(err)
	}
	// This view also did not exist before short codes; migration recreates it.
	if _, err = a.store.db.Exec("DROP VIEW mail_source_links; DROP INDEX account_public_code; ALTER TABLE records DROP COLUMN public_code"); err != nil {
		t.Fatal(err)
	}
	beforeMigration := a.store.Cursor()
	a.Close()
	a, err = NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	installFake(a)
	main, err = a.store.Account(ctx, main.ID)
	if err != nil || len(main.PublicCode) != 10 || main.PublicCode == main.ID || a.store.Cursor() != beforeMigration+1 {
		t.Fatalf("existing account was not assigned a replicated short code: %v", err)
	}
	code := main.PublicCode
	cookie, csrf := loginFixture(t, a)
	view := readReadableLink(t, a, cookie, link)
	if !strings.HasSuffix(view.URL, "/show/p0/"+code+"/recipient000") || strings.Contains(view.URL, "@") {
		t.Fatalf("unexpected short URL: %s", view.URL)
	}
	for _, path := range []string{
		readablePath(t, view.URL), readablePath(t, view.JSONURL),
		"/m/p0/" + main.ID + "/" + link.Recipient,
		"/api/mailboxes/p0/" + main.ID + "/" + link.Recipient + "/messages",
		"/m/" + link.Token, "/api/mailboxes/" + link.Token + "/messages",
	} {
		if w := request(t, a.Handler(), "GET", path, nil, nil, ""); w.Code != 200 {
			t.Fatalf("new or legacy route failed: %s (%d)", path, w.Code)
		}
	}
	w := request(t, a.Handler(), "GET", "/api/admin/links/export?q="+url.QueryEscape(view.URL), nil, cookie, "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != link.Recipient+"----"+view.URL {
		t.Fatalf("export did not use canonical short URL: %d %s", w.Code, w.Body.String())
	}
	stats := readAccessStats(t, a, cookie, "q="+url.QueryEscape(code))
	if stats.ItemCount != 1 || len(stats.Items) != 1 || stats.Items[0].PageCount != 3 || stats.Items[0].JSONCount != 3 || stats.Items[0].AccountCode != code || stats.Items[0].URL != view.URL {
		t.Fatalf("short and legacy routes lost shared statistics: %+v", stats)
	}
	main.Note, main.PublicCode = "edited", "replace-code"
	w = request(t, a.Handler(), "POST", "/api/admin/accounts", main, cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("editing account failed: %d %s", w.Code, w.Body.String())
	}
	job := Job{ID: randomID(), Kind: "import_accounts", Status: "running"}
	a.runImport(ctx, &job, ImportRequest{Kind: "accounts", Files: []ImportFile{{Name: "accounts.txt", Text: main.Email + "----new-password----client----new-refresh"}}})
	if job.Failed != 0 {
		t.Fatal("reimport failed")
	}
	main, err = a.store.Account(ctx, main.ID)
	if err != nil || main.PublicCode != code || main.RefreshToken != "new-refresh" {
		t.Fatalf("editing or reimport changed code: %v", err)
	}
	// Background refreshes can save an account value without the new field.
	main.PublicCode, main.RefreshToken = "", "rotated-refresh"
	if _, err = a.store.Save(ctx, main, &main.Version); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = NewApp(cfg, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	main, err = a.store.Account(ctx, main.ID)
	if err != nil || main.PublicCode != code || main.RefreshToken != "rotated-refresh" {
		t.Fatalf("restart or credential rotation changed code: %v", err)
	}
	if got := readReadableLink(t, a, cookie, link); got.URL != view.URL {
		t.Fatalf("public URL changed after restart: %s", got.URL)
	}
}

func TestShortRecipientNeverChoosesAnAmbiguousDomain(t *testing.T) {
	a := testApp(t, "primary")
	main := accountFixture(t, a, randomID())
	installFake(a)
	first := linkFixture(t, a, main.ID, "recipient000@example.net")
	second := linkFixture(t, a, main.ID, "recipient000@example.org")
	for _, path := range []string{
		"/show/p0/" + main.PublicCode + "/recipient000",
		"/api/mailboxes/p0/" + main.PublicCode + "/recipient000/messages",
		"/show/p0/" + main.PublicCode + "/unregistered",
		"/show/p0/unknown123/recipient000",
	} {
		if w := request(t, a.Handler(), "GET", path, nil, nil, ""); w.Code != 404 {
			t.Fatalf("ambiguous or unknown recipient returned %d: %s", w.Code, path)
		}
	}
	for _, link := range []Link{first, second} {
		path := "/api/mailboxes/p0/" + main.ID + "/" + link.Recipient + "/messages"
		w := request(t, a.Handler(), "GET", path, nil, nil, "")
		var payload struct{ Recipient string }
		if err := json.Unmarshal(w.Body.Bytes(), &payload); w.Code != 200 || err != nil || payload.Recipient != link.Recipient {
			t.Fatalf("legacy full address did not select its exact recipient: %d", w.Code)
		}
	}
}
