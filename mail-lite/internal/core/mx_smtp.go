package core

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	smtp "github.com/emersion/go-smtp"
)

type mxListener struct {
	net.Listener
	slots  chan struct{}
	mu     sync.Mutex
	active map[*mxConn]bool
}
type mxConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *mxConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func (l *mxListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			wrapped := &mxConn{Conn: conn}
			wrapped.release = func() { <-l.slots; l.mu.Lock(); delete(l.active, wrapped); l.mu.Unlock() }
			l.mu.Lock()
			l.active[wrapped] = true
			l.mu.Unlock()
			return wrapped, nil
		default:
			conn.Close()
		}
	}
}
func (l *mxListener) closeConnections() {
	l.mu.Lock()
	connections := make([]*mxConn, 0, len(l.active))
	for conn := range l.active {
		connections = append(connections, conn)
	}
	l.mu.Unlock()
	for _, conn := range connections {
		conn.Close()
	}
}

func (a *App) prepareSMTP() error {
	if !a.cfg.SMTPEnabled {
		return nil
	}
	if a.cfg.Role != "primary" {
		return errors.New("SMTP 收信只允许在主节点启用")
	}
	hostname, err := mxDomain(a.cfg.SMTPHostname)
	if err != nil {
		return errors.New("SMTP_HOSTNAME 必须为有效收信主机名")
	}
	if a.cfg.SMTPListen == "" {
		a.cfg.SMTPListen = ":2525"
	}
	if (a.cfg.SMTPTLSCert == "") != (a.cfg.SMTPTLSKey == "") {
		return errors.New("SMTP TLS 证书和私钥必须同时配置")
	}
	srv := smtp.NewServer(smtp.BackendFunc(func(*smtp.Conn) (smtp.Session, error) {
		a.smtpSessions.Add(1)
		return &mxSMTPSession{app: a, tracked: true}, nil
	}))
	srv.Domain = hostname
	srv.MaxRecipients = 100
	srv.MaxMessageBytes = mxMaxMessageBytes
	srv.ReadTimeout = 60 * time.Second
	srv.WriteTimeout = 60 * time.Second
	if a.cfg.SMTPTLSCert != "" {
		certificate, err := tls.LoadX509KeyPair(a.cfg.SMTPTLSCert, a.cfg.SMTPTLSKey)
		if err != nil {
			return err
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	}
	listener, err := net.Listen("tcp", a.cfg.SMTPListen)
	if err != nil {
		return err
	}
	a.smtpListener = &mxListener{Listener: listener, slots: make(chan struct{}, 32), active: map[*mxConn]bool{}}
	a.smtpServer = srv
	return nil
}
func (a *App) startSMTP(ctx context.Context) {
	if a.smtpServer == nil {
		return
	}
	a.smtpRunning.Store(true)
	a.workers.Add(2)
	go func() {
		defer a.workers.Done()
		defer a.smtpRunning.Store(false)
		if err := a.smtpServer.Serve(a.smtpListener); err != nil && !errors.Is(err, smtp.ErrServerClosed) && ctx.Err() == nil {
			slog.Error("SMTP listener stopped", "error", err)
		}
	}()
	go func() {
		defer a.workers.Done()
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if a.smtpServer.Shutdown(shutdown) != nil {
			a.smtpListener.(*mxListener).closeConnections()
		}
		a.smtpListener.Close()
	}()
}

type mxSMTPSession struct {
	app        *App
	recipients []string
	tracked    bool
}

func (s *mxSMTPSession) Reset() { s.recipients = nil }
func (s *mxSMTPSession) Logout() error {
	s.Reset()
	if s.tracked {
		s.tracked = false
		s.app.smtpSessions.Done()
	}
	return nil
}
func (s *mxSMTPSession) Mail(from string, _ *smtp.MailOptions) error {
	s.Reset()
	if !mxSenderValid(from) {
		return &smtp.SMTPError{Code: 501, Message: "Invalid envelope sender"}
	}
	return nil
}
func (a *App) mxAccount(ctx context.Context, recipient string) (Account, error) {
	_, domain, ok := strings.Cut(recipient, "@")
	if !ok || address(recipient) == "" {
		return Account{}, ErrNotFound
	}
	record, err := a.store.Find(ctx, "kind='account' AND provider='mx' AND email=? COLLATE NOCASE", "postmaster@"+domain)
	if err != nil {
		return Account{}, err
	}
	account, err := a.store.Account(ctx, record.ID)
	if err != nil {
		return Account{}, err
	}
	if account.Status != "active" || !mxRecipient(account, recipient) {
		return Account{}, ErrNotFound
	}
	return account, nil
}
func (s *mxSMTPSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	to = strings.ToLower(strings.TrimSpace(to))
	_, err := s.app.mxAccount(ctx, to)
	if errors.Is(err, ErrNotFound) {
		return &smtp.SMTPError{Code: 550, Message: "Recipient domain is not accepted; relaying denied"}
	}
	if err != nil {
		return &smtp.SMTPError{Code: 451, Message: "Recipient lookup temporarily unavailable"}
	}
	for _, existing := range s.recipients {
		if existing == to {
			return nil
		}
	}
	s.recipients = append(s.recipients, to)
	return nil
}
func (s *mxSMTPSession) Data(reader io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(reader, mxMaxMessageBytes+1))
	if errors.Is(err, smtp.ErrDataTooLarge) {
		return smtp.ErrDataTooLarge
	}
	if len(raw) > mxMaxMessageBytes {
		return &smtp.SMTPError{Code: 552, Message: "Message exceeds 10 MiB"}
	}
	if err != nil {
		return &smtp.SMTPError{Code: 451, Message: "Message transfer interrupted"}
	}
	// Serialize delivery with account edits/deletions, and recheck all accepted recipients.
	s.app.mutations.Lock()
	defer s.app.mutations.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts := map[string]Account{}
	recipients := map[string][]string{}
	for _, recipient := range s.recipients {
		account, err := s.app.mxAccount(ctx, recipient)
		if err != nil {
			return &smtp.SMTPError{Code: 451, Message: "Recipient configuration changed; retry later"}
		}
		accounts[account.ID] = account
		recipients[account.ID] = append(recipients[account.ID], recipient)
	}
	if len(accounts) == 0 {
		return &smtp.SMTPError{Code: 554, Message: "No accepted recipients"}
	}
	if _, err = parseMX(raw, time.Now().UTC()); err != nil {
		return &smtp.SMTPError{Code: 554, Message: "Invalid or oversized MIME content"}
	}
	if err = s.app.mx.deliver(ctx, accounts, recipients, raw, time.Now().UTC()); err != nil {
		slog.Error("SMTP persistence failed", "error", err)
		return &smtp.SMTPError{Code: 451, Message: "Message storage temporarily unavailable; retry later"}
	}
	return nil
}
