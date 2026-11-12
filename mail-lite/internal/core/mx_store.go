package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
	"golang.org/x/net/idna"
)

const mxMaxMessageBytes = 10 << 20

func mxDays(a Account) int {
	if a.MXRetentionDays == nil {
		return 30
	}
	return *a.MXRetentionDays
}
func mxDomain(value string) (string, error) {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	domain, err := idna.Lookup.ToASCII(value)
	if err != nil || len(domain) > 253 || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
		return "", errors.New("收信域名格式不正确")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("收信域名格式不正确")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", errors.New("收信域名格式不正确")
			}
		}
	}
	return domain, nil
}
func validateMX(a *Account) error {
	domain := a.MXDomain
	if domain == "" {
		_, domain, _ = strings.Cut(a.Email, "@")
	}
	var err error
	a.MXDomain, err = mxDomain(domain)
	if err != nil {
		return err
	}
	// A canonical real address keeps existing account and sales associations stable.
	a.Email = "postmaster@" + a.MXDomain
	if a.MXRetentionDays == nil {
		days := 30
		a.MXRetentionDays = &days
	}
	if mxDays(*a) < 0 || mxDays(*a) > 36500 {
		return errors.New("邮件保留天数应为 0–36500，0 表示永久保存")
	}
	a.ClientID, a.Password, a.RefreshToken = "", "", ""
	a.Proxies = nil
	a.RefreshError = ""
	a.LastRefresh = ""
	return nil
}
func mxRecipient(account Account, recipient string) bool {
	local, domain, ok := strings.Cut(recipient, "@")
	return ok && local != "" && strings.EqualFold(domain, account.MXDomain) && address(recipient) != ""
}

type mxStore struct {
	db  *sql.DB
	key []byte
}

func openMXStore(dir string, key []byte) (*mxStore, error) {
	db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(dir, "mx-mail.db"))+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS mx_messages(seq INTEGER PRIMARY KEY AUTOINCREMENT,account_id TEXT NOT NULL,received INTEGER NOT NULL,data TEXT NOT NULL,raw TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS mx_account_time ON mx_messages(account_id,seq);
 CREATE INDEX IF NOT EXISTS mx_expiry ON mx_messages(account_id,received);
 CREATE TABLE IF NOT EXISTS mx_recipients(message_seq INTEGER NOT NULL REFERENCES mx_messages(seq) ON DELETE CASCADE,recipient TEXT NOT NULL,PRIMARY KEY(message_seq,recipient));
 CREATE INDEX IF NOT EXISTS mx_recipient ON mx_recipients(recipient,message_seq);
 CREATE TABLE IF NOT EXISTS mx_summaries(message_seq INTEGER PRIMARY KEY REFERENCES mx_messages(seq) ON DELETE CASCADE,data TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &mxStore{db, key}, nil
}
func parseMX(raw []byte, at time.Time) (Message, error) {
	reader, err := gomail.CreateReader(strings.NewReader(string(raw)))
	if reader == nil {
		return Message{}, fmt.Errorf("邮件 MIME 格式不正确: %w", err)
	}
	defer reader.Close()
	m := Message{From: reader.Header.Get("From"), To: reader.Header.Get("To"), Subject: reader.Header.Get("Subject"), ReceivedAt: at, Folder: "inbox"}
	if len(m.Subject)+len(m.From)+len(m.To) > 64<<10 {
		return Message{}, errors.New("邮件头过大")
	}
	decoder := mime.WordDecoder{CharsetReader: charset.Reader}
	for _, v := range []*string{&m.Subject, &m.From, &m.To} {
		if s, e := decoder.DecodeHeader(*v); e == nil {
			*v = s
		}
	}
	for count := 0; ; count++ {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if part == nil {
			return Message{}, errors.New("邮件正文解析失败")
		}
		if count >= 100 {
			return Message{}, errors.New("邮件 MIME 分段过多")
		}
		h, ok := part.Header.(*gomail.InlineHeader)
		if !ok {
			continue
		}
		content, _, _ := h.ContentType()
		if content != "text/plain" && content != "text/html" {
			continue
		}
		body, e := io.ReadAll(io.LimitReader(part.Body, (2<<20)+1))
		if e != nil || len(body) > 2<<20 {
			return Message{}, errors.New("邮件正文超过 2 MiB")
		}
		if content == "text/plain" && m.Text == "" {
			m.Text = string(body)
		}
		if content == "text/html" && m.HTML == "" {
			m.HTML = string(body)
		}
	}
	m.HTML = sanitizeEmail(m.HTML)
	m.Preview = m.Text
	if m.Preview == "" {
		m.Preview = m.Subject
	}
	if len([]rune(m.Preview)) > 200 {
		m.Preview = string([]rune(m.Preview)[:200])
	}
	return m, nil
}
func (s *mxStore) deliver(ctx context.Context, accounts map[string]Account, recipients map[string][]string, raw []byte, at time.Time) error {
	parsed, err := parseMX(raw, at)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	encryptedRaw, err := seal(s.key, raw)
	if err != nil {
		return err
	}
	summary := parsed
	summary.Text, summary.HTML = "", ""
	summaryBytes, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	encryptedSummary, err := seal(s.key, summaryBytes)
	if err != nil {
		return err
	}
	for id := range accounts {
		b, err := json.Marshal(parsed)
		if err != nil {
			return err
		}
		encrypted, err := seal(s.key, b)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO mx_messages(account_id,received,data,raw) VALUES(?,?,?,?)", id, at.UnixMicro(), encrypted, encryptedRaw)
		if err != nil {
			return err
		}
		seq, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO mx_summaries(message_seq,data) VALUES(?,?)", seq, encryptedSummary); err != nil {
			return err
		}
		for _, recipient := range recipients[id] {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO mx_recipients VALUES(?,?)", seq, recipient); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func mxCutoff(account Account) int64 {
	if mxDays(account) == 0 {
		return 0
	}
	return time.Now().Add(-time.Duration(mxDays(account)) * 24 * time.Hour).UnixMicro()
}
func (s *mxStore) load(ctx context.Context, account Account, seq int64) (Message, error) {
	return s.read(ctx, account, seq, false)
}
func (s *mxStore) read(ctx context.Context, account Account, seq int64, summary bool) (Message, error) {
	var data string
	column := "data"
	if summary {
		column = "COALESCE((SELECT s.data FROM mx_summaries s WHERE s.message_seq=mx_messages.seq),data)"
	}
	err := s.db.QueryRowContext(ctx, "SELECT "+column+" FROM mx_messages WHERE account_id=? AND seq=? AND received>=?", account.ID, seq, mxCutoff(account)).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	b, err := unseal(s.key, data)
	if err != nil {
		return Message{}, err
	}
	var m Message
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT recipient FROM mx_recipients WHERE message_seq=? ORDER BY recipient", seq)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var recipient string
		if err = rows.Scan(&recipient); err != nil {
			return m, err
		}
		m.EnvelopeRecipients = append(m.EnvelopeRecipients, recipient)
	}
	m.Source = MessageRef{AccountID: account.ID, Protocol: "mx", ID: strconv.FormatInt(seq, 10), Folder: "inbox"}
	return m, rows.Err()
}

// The upper sequence fixes a pagination snapshot without storing the mailbox in a cursor.
type mxPosition struct {
	Upper int64 `json:"upper"`
	After int64 `json:"after"`
}
type mxReadRequest struct {
	AccountID  string     `json:"account_id"`
	Recipient  string     `json:"recipient,omitempty"`
	PlatformID string     `json:"platform_id,omitempty"`
	Order      string     `json:"order"`
	Limit      int        `json:"limit"`
	Position   mxPosition `json:"position"`
	MessageID  string     `json:"message_id,omitempty"`
}

// Explicit internal DTO: public Message intentionally omits envelope and source.
type mxWireMessage struct {
	Message    Message    `json:"message"`
	Source     MessageRef `json:"source"`
	Recipients []string   `json:"recipients"`
}
type mxReadResponse struct {
	Items    []mxWireMessage `json:"items"`
	Position mxPosition      `json:"position"`
	HasMore  bool            `json:"has_more"`
}

func (s *mxStore) page(ctx context.Context, account Account, q mxReadRequest, platform *Platform) (mxReadResponse, error) {
	out := mxReadResponse{Items: []mxWireMessage{}, Position: q.Position}
	if out.Position.Upper == 0 {
		if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(max(seq),0) FROM mx_messages WHERE account_id=?", account.ID).Scan(&out.Position.Upper); err != nil {
			return out, err
		}
	}
	if out.Position.Upper == 0 {
		return out, nil
	}
	where := "m.account_id=? AND m.received>=? AND m.seq<=?"
	args := []any{account.ID, mxCutoff(account), out.Position.Upper}
	if q.Recipient != "" {
		where += " AND EXISTS(SELECT 1 FROM mx_recipients r WHERE r.message_seq=m.seq AND r.recipient=?)"
		args = append(args, strings.ToLower(q.Recipient))
	}
	direction, compare := "DESC", "<"
	if q.Order == "oldest" {
		direction, compare = "ASC", ">"
	}
	// Scan bounded batches. Platform filtering occurs before applying the page limit.
	position := out.Position.After
	for {
		filter := where
		params := append([]any{}, args...)
		if position > 0 {
			filter += " AND m.seq" + compare + "?"
			params = append(params, position)
		}
		rows, err := s.db.QueryContext(ctx, "SELECT m.seq FROM mx_messages m WHERE "+filter+" ORDER BY m.seq "+direction+" LIMIT 128", params...)
		if err != nil {
			return out, err
		}
		var ids []int64
		for rows.Next() {
			var seq int64
			if err = rows.Scan(&seq); err != nil {
				rows.Close()
				return out, err
			}
			ids = append(ids, seq)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		for _, seq := range ids {
			position = seq
			m, err := s.read(ctx, account, seq, true)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return out, err
			}
			if platform != nil && !platformMatches(m, *platform) {
				continue
			}
			if len(out.Items) == q.Limit {
				out.HasMore = true
				return out, nil
			}
			m.Text, m.HTML = "", ""
			out.Items = append(out.Items, mxWireMessage{m, m.Source, m.EnvelopeRecipients})
			out.Position.After = seq
		}
		if len(ids) < 128 {
			return out, nil
		}
	}
}
func (a *App) cleanMX(ctx context.Context) error {
	if a.mx == nil {
		return nil
	}
	a.mutations.Lock()
	defer a.mutations.Unlock()
	records, err := a.store.All(ctx, "account")
	if err != nil {
		return err
	}
	known := map[string]Account{}
	for _, r := range records {
		if r.Provider == "mx" {
			var account Account
			if err = json.Unmarshal(r.Data, &account); err != nil {
				return err
			}
			known[r.ID] = account
		}
	}
	// Also remove orphaned rows after an interrupted account deletion.
	rows, err := a.mx.db.QueryContext(ctx, "SELECT DISTINCT account_id FROM mx_messages")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		account, ok := known[id]
		cutoff := mxCutoff(account)
		if !ok {
			cutoff = 1<<63 - 1
		}
		if ok && mxDays(account) == 0 {
			continue
		}
		for {
			r, e := a.mx.db.ExecContext(ctx, "DELETE FROM mx_messages WHERE seq IN (SELECT seq FROM mx_messages WHERE account_id=? AND received<? LIMIT 500)", id, cutoff)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n < 500 {
				break
			}
		}
	}
	return nil
}
func mxSourceID(ref MessageRef) (int64, error) {
	seq, err := strconv.ParseInt(ref.ID, 10, 64)
	if err != nil || seq <= 0 || ref.Protocol != "mx" || ref.Folder != "inbox" {
		return 0, ErrNotFound
	}
	return seq, nil
}
func mxSenderValid(value string) bool {
	if value == "" {
		return true
	}
	_, err := mail.ParseAddress(value)
	return err == nil
}
