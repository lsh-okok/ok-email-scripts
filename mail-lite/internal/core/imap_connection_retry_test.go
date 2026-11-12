package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/client"
)

// Exercise both hooks: an injected connector must have the same retry behavior
// as the production connector, including when there is only one IMAP host.
func connectionRetryBackend(hook string, dial imapHostConnector) *liveBackend {
	backend := &liveBackend{}
	if hook == "host" {
		backend.imapDialHost = dial
	} else {
		backend.imapDial = func(ctx context.Context, account Account, proxies []string) (*client.Client, func(), error) {
			return dial(ctx, account, proxies, "imap.example.test")
		}
	}
	return backend
}

type imapRetryTimeoutError struct{}

func (imapRetryTimeoutError) Error() string   { return "temporary transport timeout" }
func (imapRetryTimeoutError) Timeout() bool   { return true }
func (imapRetryTimeoutError) Temporary() bool { return true }

func TestIMAPConnectionRetriesTransientFailures(t *testing.T) {
	for _, hook := range []string{"host", "legacy"} {
		for _, failure := range []struct {
			name string
			err  error
		}{
			{"eof", io.EOF},
			{"unexpected-eof", fmt.Errorf("IMAP greeting: %w", io.ErrUnexpectedEOF)},
			{"network-operation", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}},
			{"transport-timeout", imapRetryTimeoutError{}},
			{"sanitized-transport-cause", &imapConnectionError{cause: fmt.Errorf("IMAP greeting: %w", io.EOF)}},
			{"imap-authentication-disconnected", errors.New("imap: connection closed")},
			{"sanitized-authentication-disconnected", &imapConnectionError{cause: fmt.Errorf("IMAP authentication connection: %w", errors.New("imap: connection closed"))}},
		} {
			t.Run(hook+"/"+failure.name, func(t *testing.T) {
				wire := &imapWire{validity: 42}
				success := wire.connector(t)
				attempts := 0
				account := Account{ID: "retry-account", Provider: "icloud"}
				backend := connectionRetryBackend(hook, func(ctx context.Context, got Account, proxies []string, host string) (*client.Client, func(), error) {
					attempts++
					if got.ID != account.ID || len(proxies) != 1 || proxies[0] != "mock-proxy" || host != "imap.example.test" {
						t.Fatalf("retry changed connection parameters: account=%s proxies=%v host=%s", got.ID, proxies, host)
					}
					if attempts == 1 {
						return nil, nil, failure.err
					}
					return success(ctx, got, proxies)
				})
				connection, close, err := backend.imapConnectForHost(context.Background(), account, []string{"mock-proxy"}, "imap.example.test")
				if err != nil || connection == nil || close == nil {
					t.Fatalf("transient failure was not recovered in the same request: connection=%v attempts=%d error=%v", connection != nil, attempts, err)
				}
				close()
				if attempts != 2 {
					t.Fatalf("got %d connection attempts, want 2", attempts)
				}
			})
		}
	}
}

func TestIMAPConnectionRetryIsBounded(t *testing.T) {
	for _, hook := range []string{"host", "legacy"} {
		t.Run(hook, func(t *testing.T) {
			attempts := 0
			backend := connectionRetryBackend(hook, func(context.Context, Account, []string, string) (*client.Client, func(), error) {
				attempts++
				return nil, nil, io.EOF
			})
			connection, close, err := backend.imapConnectForHost(context.Background(), Account{}, nil, "imap.example.test")
			if connection != nil || close != nil || !errors.Is(err, io.EOF) || attempts != 2 {
				t.Fatalf("persistent failure must stop after two attempts: connection=%v close=%v attempts=%d error=%v", connection != nil, close != nil, attempts, err)
			}
		})
	}
}

func TestIMAPConnectionDoesNotRetryPermanentFailures(t *testing.T) {
	for _, hook := range []string{"host", "legacy"} {
		for _, failure := range []struct {
			name string
			err  error
		}{
			{"certificate", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}},
			{"wrapped-certificate", fmt.Errorf("TLS handshake: %w", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}})},
			{"sanitized-certificate", &imapConnectionError{cause: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}},
			{"authorization", errors.New("IMAP 授权失败，请检查应用专用密码或重新授权")},
			{"oauth-rejection", errors.New("IMAP OAuth authentication rejected")},
			{"invalid-configuration", errors.New("invalid proxy configuration")},
		} {
			t.Run(hook+"/"+failure.name, func(t *testing.T) {
				attempts := 0
				backend := connectionRetryBackend(hook, func(context.Context, Account, []string, string) (*client.Client, func(), error) {
					attempts++
					return nil, nil, failure.err
				})
				_, _, err := backend.imapConnectForHost(context.Background(), Account{}, nil, "imap.example.test")
				if !errors.Is(err, failure.err) || attempts != 1 {
					t.Fatalf("permanent failure was retried or replaced: attempts=%d error=%v", attempts, err)
				}
			})
		}
	}
}

func TestIMAPConnectionRetryHonorsCancellationDuringBackoff(t *testing.T) {
	for _, hook := range []string{"host", "legacy"} {
		t.Run(hook, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			firstAttempt := make(chan struct{})
			var attempts atomic.Int64
			backend := connectionRetryBackend(hook, func(context.Context, Account, []string, string) (*client.Client, func(), error) {
				if attempts.Add(1) == 1 {
					close(firstAttempt)
				}
				return nil, nil, io.EOF
			})
			done := make(chan error, 1)
			go func() {
				_, _, err := backend.imapConnectForHost(ctx, Account{}, nil, "imap.example.test")
				done <- err
			}()
			select {
			case <-firstAttempt:
				cancel()
			case <-time.After(2 * time.Second):
				t.Fatal("initial connection attempt did not start")
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || attempts.Load() != 1 {
					t.Fatalf("canceled backoff retried or lost cancellation: attempts=%d error=%v", attempts.Load(), err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation did not interrupt connection retry")
			}
		})
	}
}

func TestIMAPConnectionRetryHonorsParentDeadline(t *testing.T) {
	for _, hook := range []string{"host", "legacy"} {
		t.Run(hook, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			attempts := 0
			backend := connectionRetryBackend(hook, func(context.Context, Account, []string, string) (*client.Client, func(), error) {
				attempts++
				return nil, nil, io.ErrUnexpectedEOF
			})
			_, _, err := backend.imapConnectForHost(ctx, Account{}, nil, "imap.example.test")
			if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
				t.Fatalf("parent deadline did not stop backoff: attempts=%d error=%v", attempts, err)
			}
		})
	}
}

type latestIMAPRetryMail struct {
	live    *liveBackend
	lists   atomic.Int64
	details atomic.Int64
}

func (b *latestIMAPRetryMail) List(ctx context.Context, account Account, proxies []string, limit int) ([]Message, error) {
	return b.ListRecipient(ctx, account, proxies, limit, "child@example.net")
}

func (b *latestIMAPRetryMail) ListRecipient(_ context.Context, account Account, _ []string, _ int, recipient string) ([]Message, error) {
	b.lists.Add(1)
	return []Message{{Subject: "newest IMAP message", From: "sender@example.net", To: recipient, Folder: "inbox", ReceivedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), Source: MessageRef{AccountID: account.ID, Protocol: "imap", Folder: "inbox", Mailbox: "INBOX", UID: 10, UIDValidity: 42}}}, nil
}

func (b *latestIMAPRetryMail) Detail(ctx context.Context, account Account, proxies []string, source MessageRef) (Message, error) {
	b.details.Add(1)
	return b.live.imapDetail(ctx, account, proxies, source)
}

func (*latestIMAPRetryMail) Delete(context.Context, Account, []string, []MessageRef) ([]DeleteResult, error) {
	return nil, errors.New("unexpected delete")
}

func TestPublicLatestHTMLRecoversFirstDetailConnectionFailure(t *testing.T) {
	for _, route := range []string{"token", "readable"} {
		t.Run(route, func(t *testing.T) {
			a := testApp(t, "primary")
			account := accountFixture(t, a, "first-detail-retry")
			link := linkFixture(t, a, account.ID, "child@example.net")
			wire := &imapWire{validity: 42, body: "From: sender@example.net\r\nTo: child@example.net\r\nSubject: newest IMAP message\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nRecovered body on the first page request"}
			success := wire.connector(t)
			var primaryAttempts, fallbackAttempts atomic.Int64
			live := &liveBackend{app: a, imapDialHost: func(ctx context.Context, account Account, proxies []string, host string) (*client.Client, func(), error) {
				attempts := &primaryAttempts
				if host != outlookIMAPHosts[0] {
					attempts = &fallbackAttempts
				}
				// Fail the initial connection to each host. Host fallback alone
				// must not hide the missing same-host connection retry.
				if attempts.Add(1) == 1 {
					return nil, nil, io.EOF
				}
				return success(ctx, account, proxies)
			}}
			backend := &latestIMAPRetryMail{live: live}
			a.mail.backend = backend
			path := "/m/" + link.Token
			if route == "readable" {
				path = "/show/p0/" + account.PublicCode + "/child"
			}
			w := request(t, a.Handler(), http.MethodGet, path, nil, nil, "")
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Recovered body on the first page request") || strings.Contains(w.Body.String(), "无法连接 IMAP") {
				t.Fatalf("first page request did not render the recovered body: %d %s", w.Code, w.Body.String())
			}
			if backend.lists.Load() != 1 || backend.details.Load() != 1 || primaryAttempts.Load() != 2 || fallbackAttempts.Load() != 0 {
				t.Fatalf("request should list once and recover one detail on its original host: lists=%d details=%d primary=%d fallback=%d", backend.lists.Load(), backend.details.Load(), primaryAttempts.Load(), fallbackAttempts.Load())
			}
			if !strings.Contains(wire.all(), "UID FETCH 10") {
				t.Fatalf("the recovered request did not fetch the selected message: %s", wire.all())
			}
		})
	}
}

func TestPublicLatestHTMLHidesIMAPTransportCause(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "private-transport-cause")
	link := linkFixture(t, a, account.ID, "child@example.net")
	transportCause := fmt.Errorf("proxy https://private-user:private-password@proxy.example.test:8443: %w", io.EOF)
	connectionError := &imapConnectionError{cause: transportCause}
	if !errors.Is(connectionError, io.EOF) {
		t.Fatal("sanitized connection error lost the transport cause needed for retry")
	}
	var attempts atomic.Int64
	a.mail.backend = &latestIMAPRetryMail{live: &liveBackend{app: a, imapDialHost: func(context.Context, Account, []string, string) (*client.Client, func(), error) {
		attempts.Add(1)
		return nil, nil, connectionError
	}}}
	w := request(t, a.Handler(), http.MethodGet, "/m/"+link.Token, nil, nil, "")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "无法连接 IMAP，请检查网络及代理") {
		t.Fatalf("permanent connection outage should render the short connection notice: %d %s", w.Code, w.Body.String())
	}
	for _, private := range []string{"private-user", "private-password", "proxy.example.test", "8443", "EOF"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("public connection notice exposed transport details: %s", private)
		}
	}
	if attempts.Load() != 4 {
		t.Fatalf("persistent Outlook outage must stop after two attempts per host: attempts=%d", attempts.Load())
	}
}
