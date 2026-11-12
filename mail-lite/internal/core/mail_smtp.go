package core

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Decrypted credentials live only in task memory and are never returned.
type smtpSettings struct {
	ID       int64  `json:"-"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	From     string `json:"from"`
	Password string `json:"password"`
	Security string `json:"security"`
}

type mailSMTPResponse struct {
	Configured  bool   `json:"configured"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	From        string `json:"from"`
	Security    string `json:"security"`
	PasswordSet bool   `json:"password_set"`
}

type mailSMTPInput struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	From          string `json:"from"`
	Security      string `json:"security"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clear_password"`
}

type mailSenderResponse struct {
	ID          int64   `json:"id"`
	Host        string  `json:"host"`
	Port        int     `json:"port"`
	From        string  `json:"from"`
	Security    string  `json:"security"`
	Enabled     bool    `json:"enabled"`
	PasswordSet bool    `json:"password_set"`
	LastUsedAt  *string `json:"last_used_at"`
}

type mailSenderInput struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	From     string `json:"from"`
	Security string `json:"security"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
}

func smtpSenderID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("发件账号编号无效")
	}
	return id, nil
}

func (a *App) encryptSMTPPassword(password string) (string, error) {
	return seal(a.key, []byte(password))
}

func (a *App) decryptSMTPPassword(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	plain, err := unseal(a.key, encoded)
	if err != nil {
		return "", fmt.Errorf("SMTP 授权码无法解密")
	}
	return string(plain), nil
}

func validateSMTPFields(s smtpSettings) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil || from.Address != s.From || s.Host == "" || len(s.Host) > 253 || strings.ContainsAny(s.Host, "/\\\r\n\t:@ ") || s.Port < 1 || s.Port > 65535 || (s.Security != "ssl" && s.Security != "starttls") {
		return fmt.Errorf("请填写有效 SMTP 服务器、端口和发件邮箱，并选择 SSL 或 STARTTLS")
	}
	if ip := net.ParseIP(s.Host); ip != nil && !publicIP(ip) {
		return fmt.Errorf("SMTP 服务器需使用公网地址")
	}
	return nil
}

func validateSMTP(s smtpSettings) error {
	if err := validateSMTPFields(s); err != nil {
		return err
	}
	if s.Password == "" || len(s.Password) > 500 {
		return fmt.Errorf("请填写有效 SMTP 授权码")
	}
	return nil
}

func (a *App) mailSMTPSettings() (smtpSettings, bool, error) {
	var host, from, security, encrypted string
	var port int
	err := a.store.db.QueryRow("SELECT host,port,from_email,security,password_ciphertext FROM mail_smtp_settings WHERE id=1").Scan(&host, &port, &from, &security, &encrypted)
	if err == sql.ErrNoRows {
		return smtpSettings{}, false, nil
	}
	if err != nil {
		return smtpSettings{}, false, err
	}
	password, err := a.decryptSMTPPassword(encrypted)
	if err != nil {
		return smtpSettings{}, false, err
	}
	s := smtpSettings{Host: host, Port: port, From: from, Password: password, Security: security}
	return s, password != "" && validateSMTP(s) == nil, nil
}

func (a *App) getMailSMTPSettings(w http.ResponseWriter, r *http.Request) {
	var host, from, security, encrypted string
	port := 465
	err := a.store.db.QueryRow("SELECT host,port,from_email,security,password_ciphertext FROM mail_smtp_settings WHERE id=1").Scan(&host, &port, &from, &security, &encrypted)
	if err != nil && err != sql.ErrNoRows {
		fail(w, err)
		return
	}
	if err == sql.ErrNoRows {
		security = "ssl"
	}
	respond(w, 200, mailSMTPResponse{Configured: encrypted != "" && host != "" && from != "", Host: host, Port: port, From: from, Security: security, PasswordSet: encrypted != ""})
}

func (a *App) saveMailSMTPSettings(w http.ResponseWriter, r *http.Request) {
	if err := a.inspectionPrimary(); err != nil {
		mailError(w, err)
		return
	}
	var in mailSMTPInput
	if !readJSON(w, r, &in) {
		return
	}
	in.Host = strings.TrimSpace(in.Host)
	in.From = strings.TrimSpace(in.From)
	in.Security = strings.TrimSpace(strings.ToLower(in.Security))
	if err := validateSMTPFields(smtpSettings{Host: in.Host, Port: in.Port, From: in.From, Security: in.Security}); err != nil {
		fail(w, err)
		return
	}
	if len(in.Password) > 500 {
		fail(w, fmt.Errorf("SMTP 授权码不能超过 500 个字符"))
		return
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	var oldCipher string
	err := a.store.db.QueryRow("SELECT password_ciphertext FROM mail_smtp_settings WHERE id=1").Scan(&oldCipher)
	if err != nil && err != sql.ErrNoRows {
		fail(w, err)
		return
	}
	ciphertext := oldCipher
	if in.ClearPassword {
		ciphertext = ""
	} else if in.Password != "" {
		ciphertext, err = a.encryptSMTPPassword(in.Password)
		if err != nil {
			fail(w, err)
			return
		}
	} else if ciphertext == "" {
		fail(w, fmt.Errorf("首次配置必须填写 SMTP 授权码；如需移除请勾选清除授权码"))
		return
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO mail_smtp_settings(id,host,port,from_email,security,password_ciphertext,updated_at)
VALUES(1,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET host=excluded.host,port=excluded.port,from_email=excluded.from_email,security=excluded.security,password_ciphertext=excluded.password_ciphertext,updated_at=excluded.updated_at,migrated_to_senders=0`, in.Host, in.Port, in.From, in.Security, ciphertext, stampNow())
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, mailSMTPResponse{Configured: ciphertext != "", Host: in.Host, Port: in.Port, From: in.From, Security: in.Security, PasswordSet: ciphertext != ""})
}

func (a *App) smtpForInspection(legacy smtpSettings) (smtpSettings, error) {
	s, configured, err := a.mailSMTPSettings()
	if err != nil {
		return smtpSettings{}, err
	}
	if configured {
		return s, nil
	}
	// Backwards compatibility for API clients from before global settings. The
	// web UI never sends this field, and it is never persisted.
	if legacy.Host != "" || legacy.From != "" || legacy.Password != "" {
		if err := validateSMTP(legacy); err != nil {
			return smtpSettings{}, err
		}
		return legacy, nil
	}
	return smtpSettings{}, fmt.Errorf("请先在邮箱配置中填写 SMTP 服务器和授权码")
}

func (a *App) smtpSendersForInspection(legacy smtpSettings) ([]smtpSettings, error) {
	rows, err := a.store.db.Query("SELECT id,host,port,from_email,security,password_ciphertext,enabled FROM mail_smtp_senders ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []smtpSettings{}
	total := 0
	for rows.Next() {
		total++
		var host, from, security, encrypted string
		var port int
		var id int64
		var enabled bool
		if err = rows.Scan(&id, &host, &port, &from, &security, &encrypted, &enabled); err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		password, e := a.decryptSMTPPassword(encrypted)
		if e != nil {
			return nil, e
		}
		s := smtpSettings{ID: id, Host: host, Port: port, From: from, Password: password, Security: security}
		if password != "" && validateSMTP(s) == nil {
			items = append(items, s)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) > 0 {
		return items, nil
	}
	if total > 0 {
		return nil, fmt.Errorf("没有启用且可用的发件邮箱，请先配置或启用账号")
	}
	rows.Close()
	var migrated bool
	err = a.store.db.QueryRow("SELECT migrated_to_senders FROM mail_smtp_settings WHERE id=1").Scan(&migrated)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if migrated {
		if legacy.Host != "" {
			if err := validateSMTP(legacy); err != nil {
				return nil, err
			}
			return []smtpSettings{legacy}, nil
		}
		return nil, fmt.Errorf("请先配置并启用发件邮箱")
	}
	s, err := a.smtpForInspection(legacy)
	if err != nil {
		return nil, err
	}
	return []smtpSettings{s}, nil
}

func (a *App) mailSenders(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.Query("SELECT id,host,port,from_email,security,enabled,password_ciphertext,last_used_at FROM mail_smtp_senders ORDER BY id")
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	items := []mailSenderResponse{}
	for rows.Next() {
		var v mailSenderResponse
		var enabled bool
		var encrypted string
		if err = rows.Scan(&v.ID, &v.Host, &v.Port, &v.From, &v.Security, &enabled, &encrypted, &v.LastUsedAt); err != nil {
			fail(w, err)
			return
		}
		v.Enabled, v.PasswordSet = enabled, encrypted != ""
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) saveMailSender(w http.ResponseWriter, r *http.Request) {
	if err := a.inspectionPrimary(); err != nil {
		mailError(w, err)
		return
	}
	var in mailSenderInput
	if !readJSON(w, r, &in) {
		return
	}
	in.Host, in.From, in.Security = strings.TrimSpace(in.Host), strings.ToLower(strings.TrimSpace(in.From)), strings.ToLower(strings.TrimSpace(in.Security))
	fields := smtpSettings{Host: in.Host, Port: in.Port, From: in.From, Security: in.Security}
	if err := validateSMTPFields(fields); err != nil {
		fail(w, err)
		return
	}
	if len(in.Password) > 500 {
		fail(w, fmt.Errorf("SMTP 授权码不能超过 500 个字符"))
		return
	}
	id := int64(0)
	if r.Method != http.MethodPost {
		var e error
		id, e = smtpSenderID(r)
		if e != nil {
			fail(w, e)
			return
		}
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	var oldCipher string
	var err error
	if id > 0 {
		if err = a.store.db.QueryRow("SELECT password_ciphertext FROM mail_smtp_senders WHERE id=?", id).Scan(&oldCipher); err != nil {
			mailError(w, err)
			return
		}
	}
	if in.Password != "" {
		oldCipher, err = a.encryptSMTPPassword(in.Password)
		if err != nil {
			fail(w, err)
			return
		}
	} else if oldCipher == "" {
		fail(w, fmt.Errorf("首次配置必须填写 SMTP 授权码"))
		return
	}
	now := stampNow()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err == nil {
		err = func(tx *sql.Tx) error {
			if id == 0 {
				res, e := tx.Exec("INSERT INTO mail_smtp_senders(host,port,from_email,security,password_ciphertext,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", in.Host, in.Port, in.From, in.Security, oldCipher, in.Enabled, now, now)
				if e != nil {
					return e
				}
				id, err = res.LastInsertId()
				if err != nil {
					return err
				}
			} else {
				res, e := tx.Exec("UPDATE mail_smtp_senders SET host=?,port=?,from_email=?,security=?,password_ciphertext=?,enabled=?,updated_at=? WHERE id=?", in.Host, in.Port, in.From, in.Security, oldCipher, in.Enabled, now, id)
				if e != nil {
					return e
				}
				n, _ := res.RowsAffected()
				if n == 0 {
					return sql.ErrNoRows
				}
			}
			_, e := tx.Exec("UPDATE mail_smtp_settings SET migrated_to_senders=1")
			return e
		}(tx)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		tx.Rollback()
	}
	if err != nil {
		mailError(w, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	respond(w, status, mailSenderResponse{ID: id, Host: in.Host, Port: in.Port, From: in.From, Security: in.Security, Enabled: in.Enabled, PasswordSet: oldCipher != ""})
}

func (a *App) deleteMailSender(w http.ResponseWriter, r *http.Request) {
	if err := a.inspectionPrimary(); err != nil {
		mailError(w, err)
		return
	}
	id, err := smtpSenderID(r)
	if err != nil {
		mailError(w, err)
		return
	}
	a.store.write.Lock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err == nil {
		err = func(tx *sql.Tx) error {
			res, err := tx.Exec("DELETE FROM mail_smtp_senders WHERE id=?", id)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return sql.ErrNoRows
			}
			return nil
		}(tx)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		tx.Rollback()
	}
	a.store.write.Unlock()
	if err != nil {
		mailError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func sendInspectionMail(ctx context.Context, s smtpSettings, to, token string) error {
	if err := validateSMTP(s); err != nil {
		return err
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil || recipient.Address != to || token == "" || len(token) > 1024 || strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("测试邮件地址或标识无效")
	}
	conn, err := publicDial(ctx, "tcp", net.JoinHostPort(s.Host, strconv.Itoa(s.Port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	rawConn := conn
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	config := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if s.Security == "ssl" {
		secured := tls.Client(conn, config)
		if err = secured.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secured
	}
	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if s.Security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP requires STARTTLS")
		}
		if err = client.StartTLS(config); err != nil {
			return err
		}
	}
	if err = client.Auth(smtp.PlainAuth("", s.From, s.Password, s.Host)); err != nil {
		return err
	}
	if err = client.Mail(s.From); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := "From: " + s.From + "\r\nTo: " + to + "\r\nSubject: Luma inbox check " + token + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nLuma inbox verification: " + token + "\r\n"
	if _, err = writer.Write([]byte(message)); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
