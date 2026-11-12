package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/mail"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

type xoauth struct{ email, token string }
type imapConnector func(context.Context, Account, []string) (*client.Client, func(), error)
type imapHostConnector func(context.Context, Account, []string, string) (*client.Client, func(), error)

var outlookIMAPHosts = []string{"outlook.live.com", "outlook.office365.com"}

// Keep the transport cause for retry decisions without exposing proxy addresses
// or credentials in the public error message.
type imapConnectionError struct{ cause error }

func (e *imapConnectionError) Error() string { return "无法连接 IMAP，请检查网络及代理" }
func (e *imapConnectionError) Unwrap() error { return e.cause }

func retryableIMAPConnection(err error) bool {
	if err == nil {
		return false
	}
	var certificate *tls.CertificateVerificationError
	var dns *net.DNSError
	if errors.Is(err, context.Canceled) || errors.As(err, &certificate) ||
		errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.Errno(10013)) ||
		errors.As(err, &dns) && dns.IsNotFound {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// go-imap v1 reports a disconnected authentication exchange using this
	// unexported sentinel instead of the underlying EOF.
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if cause.Error() == "imap: connection closed" {
			return true
		}
	}
	var network net.Error
	return errors.As(err, &network)
}

func imapHosts(account Account) []string {
	if account.Provider == "outlook" {
		return outlookIMAPHosts
	}
	return []string{"imap.mail.me.com"}
}

// Prefer a recently successful read host while retaining every original
// fallback. Never return a partial read as complete when a command fails.
func readIMAPHosts[T any](ctx context.Context, b *liveBackend, account Account, read func(string) (T, error)) (T, error) {
	var zero T
	var failures []error
	for _, host := range b.imapHostPreferences.hosts(account, time.Now()) {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		value, err := read(host)
		if err == nil {
			b.imapHostPreferences.remember(account, host, time.Now())
			return value, nil
		}
		if ctx.Err() == nil {
			b.imapHostPreferences.forget(account, host)
		}
		failures = append(failures, fmt.Errorf("%s：%w", host, err))
	}
	return zero, errors.Join(failures...)
}

func (x xoauth) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + x.email + "\x01auth=Bearer " + x.token + "\x01\x01"), nil
}
func (x xoauth) Next([]byte) ([]byte, error) {
	return nil, errors.New("IMAP OAuth authentication rejected")
}
func (b *liveBackend) imapConnect(ctx context.Context, a Account, p []string) (*client.Client, func(), error) {
	var failures []error
	for _, host := range imapHosts(a) {
		c, close, err := b.imapConnectForHost(ctx, a, p, host)
		if err == nil {
			return c, close, nil
		}
		failures = append(failures, fmt.Errorf("%s：%w", host, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, nil, errors.Join(failures...)
}
func (b *liveBackend) imapConnectForHost(ctx context.Context, a Account, p []string, host string) (*client.Client, func(), error) {
	// A public page reads the list and then opens a new connection for the
	// selected body. Recover a dropped cold connection in this request, rather
	// than requiring the visitor to refresh. Never rotate credentials here.
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		c, close, err := b.imapConnectForHostOnce(ctx, a, p, host)
		if err == nil {
			return c, close, nil
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if attempt == 1 || !retryableIMAPConnection(err) {
			return nil, nil, err
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *liveBackend) imapConnectForHostOnce(ctx context.Context, a Account, p []string, host string) (*client.Client, func(), error) {
	if b.imapDialHost != nil {
		return b.imapDialHost(ctx, a, p, host)
	}
	if b.imapDial != nil {
		return b.imapDial(ctx, a, p)
	}
	password := a.Password
	if a.Provider == "outlook" {
		t, e := b.app.tokens.Get(ctx, a.ID, "imap", false)
		if e != nil {
			return nil, nil, e
		}
		password = t.Token
	}
	var failures []error
	for _, pr := range p {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		conn, e := proxyDial(ctx, host+":993", pr)
		if e != nil {
			failures = append(failures, fmt.Errorf("dial: %w", e))
			continue
		}
		tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		deadline := time.Now().Add(40 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		tc.SetDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { tc.Close() })
		if e = tc.HandshakeContext(ctx); e != nil {
			stop()
			tc.Close()
			failures = append(failures, fmt.Errorf("TLS handshake: %w", e))
			continue
		}
		c, e := client.New(tc)
		if e != nil {
			stop()
			tc.Close()
			failures = append(failures, fmt.Errorf("IMAP greeting: %w", e))
			continue
		}
		c.ErrorLog = log.New(io.Discard, "", 0)
		// Bound each command, rather than the whole multi-folder connection.
		// The context callback still enforces the caller's overall time budget.
		tc.SetDeadline(time.Time{})
		c.Timeout = 40 * time.Second
		if a.Provider == "outlook" {
			e = c.Authenticate(xoauth{email: a.Email, token: password})
		} else {
			e = c.Login(a.Email, password)
		}
		if e != nil {
			stop()
			c.Terminate()
			if retryableIMAPConnection(e) {
				return nil, nil, &imapConnectionError{cause: fmt.Errorf("IMAP authentication connection: %w", e)}
			}
			return nil, nil, errors.New("IMAP 授权失败，请检查应用专用密码或重新授权")
		}
		return c, func() { stop(); c.Terminate() }, nil
	}
	return nil, nil, &imapConnectionError{cause: errors.Join(failures...)}
}
func listMailboxes(c *client.Client) ([]*imap.MailboxInfo, error) {
	ch := make(chan *imap.MailboxInfo, 32)
	done := make(chan error, 1)
	go func() { done <- c.List("", "*", ch) }()
	out := []*imap.MailboxInfo{}
	for v := range ch {
		out = append(out, v)
	}
	return out, <-done
}
func mailboxFor(boxes []*imap.MailboxInfo, folder string) string {
	if folder == "inbox" {
		return "INBOX"
	}
	attr := "\\Junk"
	candidates := []string{"Junk", "Junk Email", "Junk E-mail", "Spam", "垃圾邮件"}
	if folder == "trash" {
		attr = "\\Trash"
		candidates = []string{"Deleted Messages", "Deleted Items", "Trash", "废纸篓", "已删除邮件"}
	}
	for _, box := range boxes {
		for _, v := range box.Attributes {
			if strings.EqualFold(v, attr) {
				return box.Name
			}
		}
	}
	for _, candidate := range candidates {
		for _, box := range boxes {
			if strings.EqualFold(box.Name, candidate) {
				return box.Name
			}
		}
	}
	return ""
}
func fetchIMAPOnce(c *client.Client, set *imap.SeqSet, items []imap.FetchItem, uid bool) ([]*imap.Message, error) {
	ch := make(chan *imap.Message, 64)
	done := make(chan error, 1)
	go func() {
		if uid {
			done <- c.UidFetch(set, items, ch)
		} else {
			done <- c.Fetch(set, items, ch)
		}
	}()
	out := []*imap.Message{}
	for v := range ch {
		out = append(out, v)
	}
	return out, <-done
}

func fetchIMAP(c *client.Client, set *imap.SeqSet, items []imap.FetchItem, uid bool) ([]*imap.Message, error) {
	if set.Dynamic() {
		return fetchIMAPOnce(c, set, items, uid)
	}
	batchSize := imapFetchBatchSize(items)
	var batches []*imap.SeqSet
	batch, count := new(imap.SeqSet), 0
	for _, span := range set.Set {
		for number := span.Start; ; number++ {
			batch.AddNum(number)
			count++
			if count == batchSize {
				batches = append(batches, batch)
				batch, count = new(imap.SeqSet), 0
			}
			if number == span.Stop {
				break
			}
		}
	}
	if count > 0 {
		batches = append(batches, batch)
	}
	out := []*imap.Message{}
	for _, batch := range batches {
		messages, err := fetchIMAPOnce(c, batch, items, uid)
		if err == nil {
			out = append(out, messages...)
			continue
		}
		// Some Microsoft IMAP servers reject a multi-message FETCH while a
		// single-message request works. Match the original per-message fetch
		// fallback, keeping every failure visible instead of skipping a mail.
		if c.State() != imap.SelectedState || len(batch.Set) == 1 && batch.Set[0].Start == batch.Set[0].Stop {
			return nil, err
		}
		for _, span := range batch.Set {
			for number := span.Start; ; number++ {
				one := new(imap.SeqSet)
				one.AddNum(number)
				messages, singleErr := fetchIMAPOnce(c, one, items, uid)
				if singleErr != nil {
					return nil, errors.Join(err, singleErr)
				}
				out = append(out, messages...)
				if number == span.Stop {
					break
				}
			}
		}
	}
	return out, nil
}

func imapFetchBatchSize(items []imap.FetchItem) int {
	for _, item := range items {
		name := strings.ToUpper(string(item))
		if strings.HasPrefix(name, "BODY") || strings.HasPrefix(name, "RFC822") {
			return 50
		}
	}
	// UID and INTERNALDATE snapshots are small. Keep large metadata batches
	// efficient while bounding the much larger header and body responses.
	return 1000
}
func headerMessage(raw io.Reader, account, folder, mailbox string, uid, validity uint32, date time.Time) (Message, error) {
	v, e := mail.ReadMessage(io.LimitReader(raw, 128<<10))
	if e != nil {
		return Message{}, e
	}
	decoder := &mime.WordDecoder{CharsetReader: charset.Reader}
	decode := func(s string) string {
		x, e := decoder.DecodeHeader(s)
		if e != nil {
			return s
		}
		return x
	}
	h := map[string][]string{}
	for k, vs := range v.Header {
		h[strings.ToLower(k)] = vs
	}
	if date.IsZero() {
		date, _ = mail.ParseDate(v.Header.Get("Date"))
	}
	return Message{Subject: decode(v.Header.Get("Subject")), From: decode(v.Header.Get("From")), To: decode(v.Header.Get("To")), ReceivedAt: date, Folder: folder, Headers: h, Source: MessageRef{AccountID: account, Protocol: "imap", Folder: folder, Mailbox: mailbox, UID: uid, UIDValidity: validity}}, nil
}
func (b *liveBackend) imapList(ctx context.Context, a Account, p []string, n int) ([]Message, error) {
	return b.imapListOrder(ctx, a, p, n, "latest")
}
func (b *liveBackend) imapListOrder(ctx context.Context, a Account, p []string, n int, order string) ([]Message, error) {
	return readIMAPHosts(ctx, b, a, func(host string) ([]Message, error) {
		return b.imapListOrderForHost(ctx, a, p, n, order, host)
	})
}
func (b *liveBackend) imapListOrderForHost(ctx context.Context, a Account, p []string, n int, order, host string) ([]Message, error) {
	c, close, e := b.imapConnectForHost(ctx, a, p, host)
	if e != nil {
		return nil, e
	}
	defer close()
	boxes, e := listMailboxes(c)
	if e != nil {
		return nil, e
	}
	out := []Message{}
	for _, folder := range []string{"inbox", "junkemail"} {
		name := mailboxFor(boxes, folder)
		if name == "" {
			continue
		}
		status, e := c.Select(name, true)
		if e != nil {
			return nil, errors.New("IMAP 文件夹不可访问")
		}
		if status.UidValidity == 0 {
			return nil, errors.New("IMAP server did not provide UIDVALIDITY")
		}
		if status.Messages == 0 {
			continue
		}
		first, last := uint32(1), status.Messages
		if status.Messages > uint32(n) {
			if order == "oldest" {
				last = uint32(n)
			} else {
				first = status.Messages - uint32(n) + 1
			}
		}
		set := new(imap.SeqSet)
		set.AddRange(first, last)
		section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier}, Peek: true}
		messages, e := fetchIMAP(c, set, []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate, section.FetchItem()}, false)
		if e != nil {
			return nil, fmt.Errorf("IMAP %s 邮件列表读取失败：%w", folder, e)
		}
		for _, msg := range messages {
			raw := msg.GetBody(section)
			if raw == nil || msg.Uid == 0 {
				return nil, errors.New("IMAP response omitted a message UID or header")
			}
			v, e := headerMessage(raw, a.ID, folder, name, msg.Uid, status.UidValidity, msg.InternalDate)
			if e != nil {
				return nil, errors.New("IMAP 邮件头解析失败")
			}
			out = append(out, v)
		}
	}
	if order == "oldest" {
		sort.SliceStable(out, func(i, j int) bool { return messageBefore(out[i], out[j]) })
	} else {
		sort.SliceStable(out, func(i, j int) bool { return messageAfter(out[i], out[j]) })
	}
	return out, nil
}
func (b *liveBackend) imapListRecipient(ctx context.Context, a Account, p []string, n int, recipient string) ([]Message, error) {
	return b.imapListRecipientOrder(ctx, a, p, n, recipient, "latest")
}
func (b *liveBackend) imapListRecipientOrder(ctx context.Context, a Account, p []string, n int, recipient, order string) ([]Message, error) {
	return readIMAPHosts(ctx, b, a, func(host string) ([]Message, error) {
		return b.imapListRecipientOrderForHost(ctx, a, p, n, recipient, order, host)
	})
}
func (b *liveBackend) imapListRecipientOrderForHost(ctx context.Context, a Account, p []string, n int, recipient, order, host string) ([]Message, error) {
	c, close, e := b.imapConnectForHost(ctx, a, p, host)
	if e != nil {
		return nil, e
	}
	defer close()
	boxes, e := listMailboxes(c)
	if e != nil {
		return nil, e
	}
	out := []Message{}
	for _, folder := range []string{"inbox", "junkemail"} {
		name := mailboxFor(boxes, folder)
		if name == "" {
			continue
		}
		status, e := c.Select(name, true)
		if e != nil {
			return nil, errors.New("IMAP 文件夹不可访问")
		}
		if status.UidValidity == 0 {
			return nil, errors.New("IMAP server did not provide UIDVALIDITY")
		}
		criteria := imap.NewSearchCriteria()
		criteria.Header.Add("To", recipient)
		uids, e := c.UidSearch(criteria)
		if e != nil {
			return nil, errors.New("IMAP 收件人查询失败")
		}
		if len(uids) == 0 {
			continue
		}
		set := new(imap.SeqSet)
		for _, uid := range uids {
			set.AddNum(uid)
		}
		section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier}, Peek: true}
		messages, e := fetchIMAP(c, set, []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate, section.FetchItem()}, true)
		if e != nil {
			return nil, fmt.Errorf("IMAP %s 邮件列表读取失败：%w", folder, e)
		}
		for _, msg := range messages {
			raw := msg.GetBody(section)
			if raw == nil || msg.Uid == 0 {
				return nil, errors.New("IMAP response omitted a message UID or header")
			}
			v, e := headerMessage(raw, a.ID, folder, name, msg.Uid, status.UidValidity, msg.InternalDate)
			if e != nil {
				return nil, errors.New("IMAP 邮件头解析失败")
			}
			if matchesAccountRecipient(a, v, recipient) {
				out = append(out, v)
			}
		}
	}
	if order == "oldest" {
		sort.SliceStable(out, func(i, j int) bool { return messageBefore(out[i], out[j]) })
	} else {
		sort.SliceStable(out, func(i, j int) bool { return messageAfter(out[i], out[j]) })
	}
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
func (b *liveBackend) imapDetail(ctx context.Context, a Account, p []string, r MessageRef) (Message, error) {
	return readIMAPHosts(ctx, b, a, func(host string) (Message, error) {
		return b.imapDetailForHost(ctx, a, p, r, host)
	})
}
func (b *liveBackend) imapDetailForHost(ctx context.Context, a Account, p []string, r MessageRef, host string) (Message, error) {
	c, close, e := b.imapConnectForHost(ctx, a, p, host)
	if e != nil {
		return Message{}, e
	}
	defer close()
	return imapDetailWithClient(c, a, r)
}

// Keep selection, UID validation and bounded body parsing shared with the
// latest-message path, which already owns an authenticated read connection.
func imapDetailWithClient(c *client.Client, a Account, r MessageRef) (Message, error) {
	status, e := c.Select(r.Mailbox, true)
	if e != nil {
		return Message{}, e
	}
	if status.UidValidity != r.UIDValidity {
		return Message{}, errors.New("邮箱 UID 已变化，请刷新邮件列表")
	}
	set := new(imap.SeqSet)
	set.AddNum(r.UID)
	section := &imap.BodySectionName{Peek: true, Partial: []int{0, 2 << 20}}
	messages, e := fetchIMAP(c, set, []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate, section.FetchItem()}, true)
	if e != nil {
		return Message{}, e
	}
	if len(messages) != 1 || messages[0].Uid != r.UID {
		return Message{}, ErrNotFound
	}
	raw := messages[0].GetBody(section)
	if raw == nil {
		return Message{}, errors.New("IMAP 邮件正文为空")
	}
	reader, e := gomail.CreateReader(io.LimitReader(raw, 2<<20))
	if reader == nil {
		return Message{}, e
	}
	defer reader.Close()
	headers := map[string][]string{}
	fields := reader.Header.Fields()
	for fields.Next() {
		key := strings.ToLower(fields.Key())
		headers[key] = append(headers[key], fields.Value())
	}
	m := Message{Subject: reader.Header.Get("Subject"), From: reader.Header.Get("From"), To: reader.Header.Get("To"), Folder: r.Folder, ReceivedAt: messages[0].InternalDate, Headers: headers, Source: r}
	decoder := &mime.WordDecoder{CharsetReader: charset.Reader}
	if v, e := decoder.DecodeHeader(m.Subject); e == nil {
		m.Subject = v
	}
	for parts := 0; parts < 100; parts++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if part == nil {
			if err != nil {
				return m, errors.New("邮件正文解析失败")
			}
			break
		}
		h, ok := part.Header.(*gomail.InlineHeader)
		if !ok {
			continue
		}
		media, _, _ := h.ContentType()
		if media != "text/plain" && media != "text/html" {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(part.Body, 1<<20))
		if media == "text/html" && m.HTML == "" {
			m.HTML = string(body)
		}
		if media == "text/plain" && m.Text == "" {
			m.Text = string(body)
		}
	}
	return m, nil
}

type uidExpunge struct{ set *imap.SeqSet }

func (c uidExpunge) Command() *imap.Command {
	return &imap.Command{Name: "UID EXPUNGE", Arguments: []interface{}{c.set}}
}
func moveUID(c *client.Client, set *imap.SeqSet, trash string) error {
	move, e := c.Support("MOVE")
	if e != nil {
		return e
	}
	if move {
		return c.UidMove(set, trash)
	}
	plus, e := c.Support("UIDPLUS")
	if e != nil {
		return e
	}
	if !plus {
		return errors.New("邮箱不支持安全的逐封移动，请在邮箱服务商页面操作")
	}
	if e = c.UidCopy(set, trash); e != nil {
		return e
	}
	if e = c.UidStore(set, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.DeletedFlag}, nil); e != nil {
		return e
	}
	status, e := c.Execute(uidExpunge{set}, nil)
	if e != nil {
		return e
	}
	return status.Err()
}
func (b *liveBackend) imapDelete(ctx context.Context, a Account, p []string, refs []MessageRef) ([]DeleteResult, error) {
	c, close, e := b.imapConnect(ctx, a, p)
	if e != nil {
		return nil, e
	}
	defer close()
	boxes, e := listMailboxes(c)
	if e != nil {
		return nil, e
	}
	trash := mailboxFor(boxes, "trash")
	if trash == "" {
		return nil, errors.New("未找到废纸篓，无法移动邮件")
	}
	out := []DeleteResult{}
	for _, r := range refs {
		v := DeleteResult{Ref: signRef(b.app.key, r)}
		status, e := c.Select(r.Mailbox, false)
		if e != nil {
			v.Error = "无法打开源文件夹"
		} else if status.UidValidity != r.UIDValidity {
			v.Error = "邮箱 UID 已变化，请刷新列表后重试"
		} else if strings.EqualFold(r.Mailbox, trash) {
			v.Error = "不支持从废纸篓永久清除"
		} else {
			set := new(imap.SeqSet)
			set.AddNum(r.UID)
			messages, err := fetchIMAP(c, set, []imap.FetchItem{imap.FetchUid}, true)
			if err != nil {
				v.Error = "无法验证邮件 UID"
			} else if len(messages) == 0 {
				v.Success = true
			} else if len(messages) != 1 || messages[0].Uid != r.UID {
				v.Error = "邮件 UID 不匹配"
			} else if err = moveUID(c, set, trash); err != nil {
				v.Error = "移动到废纸篓失败；请刷新列表核对结果"
			} else {
				v.Success = true
			}
		}
		out = append(out, v)
	}
	return out, nil
}
