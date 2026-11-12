package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in local fixture. Never built into the production executable.
func TestBrowserFixture(t *testing.T) {
	address := os.Getenv("MAIL_LITE_QA_ADDR")
	if address == "" {
		t.Skip("browser fixture disabled")
	}
	a := testApp(t, "primary")
	personal := accountFixture(t, a, "personal")
	personal.Note = "QA Outlook detail"
	personal.Proxies = []string{"http://proxy-user:proxy-pass@localhost:8080"}
	if record, err := a.store.Save(context.Background(), personal, &personal.Version); err != nil {
		t.Fatal(err)
	} else {
		personal.Version = record.Version
	}
	accountFixture(t, a, "work")
	for i := 1; i <= 8; i++ {
		accountFixture(t, a, fmt.Sprintf("team-%02d", i))
	}
	icloud := Account{ID: "icloud", Email: "direct@icloud.com", Provider: "icloud", Status: "active", Password: "qa-app-password"}
	if _, err := a.store.Save(context.Background(), icloud, nil); err != nil {
		t.Fatal(err)
	}
	a.tokens.values = map[string]AccessToken{"personal:graph": {Token: "qa-access-" + strings.Repeat("test-access-", 200), ExpiresAt: time.Now().Add(time.Hour)}}
	linkFixture(t, a, "personal", "recipient000@example.net")
	if os.Getenv("MAIL_LITE_QA_SOURCE_LINKS") == "1" {
		for i := 1; i <= 4; i++ {
			linkFixture(t, a, "personal", fmt.Sprintf("recipient%03d@example.net", i))
		}
		cookie, csrf := loginFixture(t, a)
		w := request(t, a.Handler(), "POST", "/api/admin/mail/mapping", map[string]any{"include_links": true}, cookie, csrf)
		if w.Code != 200 {
			t.Fatalf("seed source mappings: %s", w.Body.String())
		}
		if _, err := a.store.db.Exec(`UPDATE mailboxes SET link_mode='custom',link='' WHERE kind='child' AND email IN ('recipient001@example.net','recipient002@example.net');
 UPDATE mailboxes SET link_mode='custom',link='https://custom.example.net/existing' WHERE email='recipient003@example.net';
 UPDATE mailboxes SET link_mode='custom',link='',status='reserved' WHERE email='recipient004@example.net';`); err != nil {
			t.Fatal(err)
		}
	}
	fake := installFake(a)
	fake.delay = 500 * time.Millisecond
	if os.Getenv("MAIL_LITE_QA_INSPECTION_RETRY") == "1" {
		cookie, csrf := loginFixture(t, a)
		_, ids := pendingInspectionFixture(t, a, cookie, csrf, 10)
		if _, err := a.store.db.Exec("UPDATE mailboxes SET status='sold' WHERE id=?", ids[0]); err != nil {
			t.Fatal(err)
		}
		// Local browser verification reads original test tokens without SMTP.
		a.inspectionHTTP = &http.Client{Transport: inspectionRoundTripper(func(r *http.Request) (*http.Response, error) {
			var tokens strings.Builder
			for _, id := range ids {
				fmt.Fprintf(&tokens, "original-test-token-%d\n", id)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tokens.String())), Header: http.Header{}, Request: r}, nil
		})}
		a.inspectionSend = func(context.Context, smtpSettings, string, string) error {
			t.Error("browser retry attempted to send SMTP")
			return fmt.Errorf("SMTP is disabled in the retry fixture")
		}
	}
	a.Start(context.Background())
	// Count only endpoint names, never credentials, message refs or cursors. The
	// opt-in fixture lets browser QA prove that returning to a mailbox restores
	// its cached list and body without another request.
	var statsMu sync.Mutex
	stats := map[string]int{}
	handler := a.Handler()
	qaHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__qa/stats" {
			statsMu.Lock()
			defer statsMu.Unlock()
			respond(w, http.StatusOK, stats)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/accounts/") && (strings.HasSuffix(r.URL.Path, "/messages") || strings.HasSuffix(r.URL.Path, "/message")) {
			key := r.URL.Path
			if strings.HasSuffix(key, "/messages") {
				key += "?order=" + r.URL.Query().Get("order") + "&folder=" + r.URL.Query().Get("folder")
				if r.URL.Query().Get("cursor") != "" {
					key += "&continued=1"
				}
			}
			statsMu.Lock()
			stats[key]++
			statsMu.Unlock()
		}
		handler.ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: address, Handler: qaHandler, ReadHeaderTimeout: 5 * time.Second}
	timer := time.AfterFunc(15*time.Minute, func() { srv.Close() })
	defer timer.Stop()
	t.Log("browser fixture", address)
	if e := srv.ListenAndServe(); e != http.ErrServerClosed {
		t.Fatal(e)
	}
}
