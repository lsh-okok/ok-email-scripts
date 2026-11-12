package core

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"mail-lite/web"
)

// Opt-in fixture: an isolated SMTP/HTTP service with no external mail accounts.
func TestMXBrowserFixture(t *testing.T) {
	address := os.Getenv("MAIL_LITE_QA_MX_ADDR")
	if address == "" {
		t.Skip("MX browser fixture disabled")
	}
	smtpAddress := os.Getenv("MAIL_LITE_QA_MX_SMTP_ADDR")
	if smtpAddress == "" {
		smtpAddress = "127.0.0.1:18526"
	}
	a, err := NewApp(Config{Role: "primary", DataDir: t.TempDir(), AdminPassword: "test-password-12345", SMTPEnabled: true, SMTPListen: smtpAddress, SMTPHostname: "mx.qa.test"}, web.Assets)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	a.Start(ctx)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__qa/mx/stop" && r.Method == "POST" {
			respond(w, 200, map[string]bool{"ok": true})
			cancel()
			return
		}
		handler.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Printf("MX browser fixture: HTTP http://%s SMTP %s\n", listener.Addr(), a.smtpListener.Addr())
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	server.Shutdown(shutdown)
	if err := <-done; err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
