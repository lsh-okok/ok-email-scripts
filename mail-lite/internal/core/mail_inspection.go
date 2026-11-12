package core

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type inspectionTask struct {
	ID         int64   `json:"id"`
	Mode       string  `json:"mode"`
	Scope      string  `json:"scope"`
	FolderID   int64   `json:"folder_id"`
	Status     string  `json:"status"`
	Total      int     `json:"total"`
	Processed  int     `json:"processed"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	Pending    int     `json:"pending"`
	Actor      string  `json:"actor"`
	CreatedAt  string  `json:"created_at"`
	FinishedAt *string `json:"finished_at"`
}
type inspectionResult struct {
	ID          int64   `json:"id"`
	MailboxID   *int64  `json:"mailbox_id"`
	Email       string  `json:"email"`
	ParentEmail string  `json:"parent_email"`
	Link        string  `json:"link,omitempty"`
	Result      string  `json:"result"`
	StatusCode  int     `json:"status_code"`
	Message     string  `json:"message"`
	CheckedAt   string  `json:"checked_at"`
	Processed   bool    `json:"processed"`
	SenderEmail string  `json:"sender_email,omitempty"`
	Phase       string  `json:"phase"`
	StartedAt   *string `json:"started_at"`
	SentAt      *string `json:"sent_at"`
	LinkMode    string  `json:"-"`
	PolicyKey   string  `json:"-"`
	token       string
}
type inspectionRetrySkipped struct {
	Email       string `json:"email"`
	ParentEmail string `json:"parent_email"`
	Reason      string `json:"reason"`
}
type inspectionRetryInfo struct {
	SourceID  int64                    `json:"source_id"`
	Requested int                      `json:"requested"`
	Retried   int                      `json:"retried"`
	Skipped   []inspectionRetrySkipped `json:"skipped"`
}
type inspectionOptions struct {
	Timeout     int `json:"timeout"`
	Concurrency int `json:"concurrency"`
	Wait        int `json:"wait"`
	Interval    int `json:"interval"`
}
type inspectionInput struct {
	IDs         []int64           `json:"ids"`
	MasterIDs   []int64           `json:"master_ids"`
	FolderID    int64             `json:"folder_id"`
	Scope       string            `json:"scope"`
	Sample      int               `json:"sample"`
	Mode        string            `json:"mode"`
	IncludeSold bool              `json:"include_sold"`
	ConfirmSend bool              `json:"confirm_send"`
	Options     inspectionOptions `json:"options"`
	SMTP        smtpSettings      `json:"smtp"`
}

func inspectionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		mailError(w, badMail("检测任务编号无效"))
		return 0, false
	}
	return id, true
}

func (a *App) inspectionPrimary() error {
	if a.cfg.Role != "primary" {
		return mailFailure{http.StatusForbidden, "销售检测仅可在主节点运行"}
	}
	return nil
}

// Every inspection write shares the parent's single writer lock.  Network
// requests and SMTP waits must never hold this lock.
func (a *App) inspectionExec(query string, args ...any) (sql.Result, error) {
	if err := a.inspectionPrimary(); err != nil {
		return nil, err
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	return a.store.db.Exec(query, args...)
}

const inspectionColumns = `id,mode,scope,COALESCE(folder_id,0),status,total,processed,passed,failed,pending,actor,created_at,finished_at`

func scanInspection(row scanner) (inspectionTask, error) {
	var v inspectionTask
	err := row.Scan(&v.ID, &v.Mode, &v.Scope, &v.FolderID, &v.Status, &v.Total, &v.Processed, &v.Passed, &v.Failed, &v.Pending, &v.Actor, &v.CreatedAt, &v.FinishedAt)
	return v, err
}
func normalizeInspectionOptions(v *inspectionOptions) error {
	if v.Timeout == 0 {
		v.Timeout = 10
	}
	if v.Concurrency == 0 {
		v.Concurrency = 5
	}
	if v.Wait == 0 {
		v.Wait = 60
	}
	if v.Interval == 0 {
		v.Interval = 1000
	}
	if v.Timeout < 1 || v.Timeout > 60 || v.Concurrency < 1 || v.Concurrency > 10 || v.Wait < 5 || v.Wait > 300 || v.Interval < 200 || v.Interval > 10000 {
		return fmt.Errorf("超时需 1–60 秒，并发需 1–10，收信等待需 5–300 秒，发送间隔需 200–10000 毫秒")
	}
	return nil
}
func (a *App) mailInspectionList(w http.ResponseWriter, r *http.Request) {
	page, limit := mailInventoryPaging(r)
	var total int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_inspections").Scan(&total); err != nil {
		mailError(w, err)
		return
	}
	rows, err := a.store.db.Query("SELECT "+inspectionColumns+" FROM mail_inspections ORDER BY id DESC LIMIT ? OFFSET ?", limit, (page-1)*limit)
	if err != nil {
		mailError(w, err)
		return
	}
	defer rows.Close()
	items := []inspectionTask{}
	for rows.Next() {
		v, e := scanInspection(rows)
		if e != nil {
			fail(w, e)
			return
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		mailError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "total": total, "page": page, "limit": limit})
}
func (a *App) inspectionCandidates(tx *sql.Tx, in inspectionInput, base string) ([]inspectionResult, error) {
	where, args := "c.kind='child' AND "+mailLinkReadySQL, []any{}
	if !in.IncludeSold {
		where += " AND c.status='available'"
	}
	switch in.Scope {
	case "selected", "master":
		ids, kind, col := in.IDs, "child", "c.id"
		if in.Scope == "master" {
			ids, kind, col = in.MasterIDs, "master", "c.parent_id"
		}
		ids, err := uniqueMailIDs(ids)
		if err != nil {
			return nil, err
		}
		if err = requireMailIDs(tx, ids, kind); err != nil {
			return nil, err
		}
		ph, p := marks(ids)
		where += " AND " + col + " IN (" + ph + ")"
		args = append(args, p...)
	case "folder":
		if in.FolderID < 0 {
			return nil, fmt.Errorf("文件夹无效")
		}
		if err := mailCheckFolder(context.Background(), tx, in.FolderID); err != nil {
			return nil, err
		}
		where += " AND COALESCE(m.folder_id,0)=?"
		args = append(args, in.FolderID)
	case "all":
	default:
		return nil, fmt.Errorf("检测范围无效")
	}
	if in.Sample != 0 && in.Sample != 1 && in.Sample != 3 && in.Sample != 5 {
		return nil, fmt.Errorf("请选择全部检测或每组抽样 1 / 3 / 5 个")
	}
	rows, err := tx.Query("SELECT c.id,COALESCE(c.parent_id,-c.id),c.email,COALESCE(m.email,''),c.link,c.link_mode,"+mailPolicySQL+childFrom+"WHERE "+where+" ORDER BY RANDOM()", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []inspectionResult{}
	counts := map[int64]int{}
	for rows.Next() {
		var v inspectionResult
		var parent int64
		if err = rows.Scan(&v.MailboxID, &parent, &v.Email, &v.ParentEmail, &v.Link, &v.LinkMode, &v.PolicyKey); err != nil {
			return nil, err
		}
		if in.Sample == 0 || counts[parent] < in.Sample {
			items = append(items, v)
			counts[parent]++
		}
		if len(items) > 20000 {
			return nil, fmt.Errorf("一次检测最多 20000 个子邮箱，请按文件夹分批检测")
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range items {
		if items[i].LinkMode == "source" {
			source, e := a.sourceLink(context.Background(), tx, *items[i].MailboxID, base, true)
			if e != nil {
				return nil, e
			}
			if !source.LinkAvailable {
				return nil, badMail(source.LinkReason)
			}
			items[i].Link = source.Link
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("没有符合条件的可检测子邮箱")
	}
	return items, nil
}
func (a *App) createMailInspection(w http.ResponseWriter, r *http.Request) {
	if err := a.inspectionPrimary(); err != nil {
		mailError(w, err)
		return
	}
	var in inspectionInput
	if !readJSON(w, r, &in) {
		return
	}
	if in.Mode != "link" && in.Mode != "inbox" {
		fail(w, fmt.Errorf("检测方式无效"))
		return
	}
	if in.Scope == "" {
		in.Scope = "folder"
		if len(in.IDs) > 0 {
			in.Scope = "selected"
		} else if len(in.MasterIDs) > 0 {
			in.Scope = "master"
		}
	}
	if err := normalizeInspectionOptions(&in.Options); err != nil {
		mailError(w, err)
		return
	}
	senders := []smtpSettings{}
	if in.Mode == "inbox" {
		if !in.ConfirmSend {
			fail(w, fmt.Errorf("收信验证需要明确确认发送测试邮件"))
			return
		}
		var err error
		senders, err = a.smtpSendersForInspection(in.SMTP)
		if err != nil {
			mailError(w, err)
			return
		}
	}
	a.inspectionMu.Lock()
	defer a.inspectionMu.Unlock()
	if a.closing || len(a.inspectionJobs) >= 4 {
		mailError(w, mailFailure{http.StatusConflict, "服务正在关闭或运行任务已达上限"})
		return
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.Begin()
	if err != nil {
		mailError(w, err)
		return
	}
	defer tx.Rollback()
	candidates, err := a.inspectionCandidates(tx, in, a.baseURL(r))
	if err != nil {
		mailError(w, err)
		return
	}
	id, err := insertInspection(tx, in.Mode, in.Scope, in.FolderID, in.Options, candidates, "admin")
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		mailError(w, err)
		return
	}
	a.launchInspectionWithSenders(id, in.Mode, in.Options, senders, candidates, false)
	respond(w, 201, map[string]any{"id": id, "status": "queued", "total": len(candidates)})
}
func insertInspection(tx *sql.Tx, mode, scope string, folder int64, opts inspectionOptions, items []inspectionResult, actor string) (int64, error) {
	options, _ := json.Marshal(opts)
	res, err := tx.Exec("INSERT INTO mail_inspections(mode,scope,folder_id,status,total,actor,created_at,options) VALUES(?,?,?,'queued',?,?,?,?)", mode, scope, nullableID(folder), len(items), actor, stampNow(), string(options))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for i := range items {
		if items[i].LinkMode == "" {
			items[i].LinkMode = "custom"
		}
		res, err = tx.Exec("INSERT INTO mail_inspection_results(inspection_id,mailbox_id,email,parent_email,link,result,message,checked_at,processed,test_token,sender_email,sent_at,link_mode,policy_key) VALUES(?,?,?,?,?,'pending','等待检测','',0,?,?,?,?,?)", id, items[i].MailboxID, items[i].Email, items[i].ParentEmail, items[i].Link, items[i].token, items[i].SenderEmail, items[i].SentAt, items[i].LinkMode, items[i].PolicyKey)
		if err != nil {
			return 0, err
		}
		items[i].ID, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	}
	return id, nil
}

// Caller holds inspectionMu; workers have no dependency on the HTTP request lifetime.
func (a *App) launchInspection(id int64, mode string, opts inspectionOptions, smtp smtpSettings, items []inspectionResult, retry bool) {
	senders := []smtpSettings{}
	if smtp.From != "" {
		senders = []smtpSettings{smtp}
	}
	a.launchInspectionWithSenders(id, mode, opts, senders, items, retry)
}

func (a *App) launchInspectionWithSenders(id int64, mode string, opts inspectionOptions, senders []smtpSettings, items []inspectionResult, retry bool) {
	if a.cfg.Role != "primary" || a.closing {
		return
	}
	parent := a.inspectionContext
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.inspectionJobs[id] = cancel
	a.inspectionWG.Add(1)
	go func() {
		defer a.inspectionWG.Done()
		defer func() { cancel(); a.inspectionMu.Lock(); delete(a.inspectionJobs, id); a.inspectionMu.Unlock() }()
		res, err := a.inspectionExec("UPDATE mail_inspections SET status='running' WHERE id=? AND status='queued'", id)
		if err != nil {
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return
		}
		jobs := make(chan inspectionResult)
		var wg sync.WaitGroup
		client := a.inspectionHTTP
		if client == nil {
			client = newInspectionHTTPClient(opts.Timeout)
			defer client.CloseIdleConnections()
		}
		for i := 0; i < opts.Concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for item := range jobs {
					if ctx.Err() != nil {
						return
					}
					if _, err := a.inspectionExec("UPDATE mail_inspection_results SET phase='checking',started_at=?,message='正在检查接码页面' WHERE id=? AND processed=0 AND EXISTS(SELECT 1 FROM mail_inspections WHERE id=? AND status='running')", stampNow(), item.ID, id); err != nil {
						cancel()
						return
					}
					result, code, msg := "pending", 0, "未执行"
					if mode == "link" {
						result, code, msg = a.checkMailInspectionLink(ctx, client, item, opts.Timeout, "")
					} else {
						if !retry {
							if err := a.sendInspectionItem(ctx, id, opts, senders, &item); err != nil {
								msg = "发信失败或投递状态不确定；复检仅查询本次测试标识"
							} else {
								result, code, msg = a.pollMailInspectionInbox(ctx, client, item, opts)
							}
						} else {
							_, _ = a.inspectionExec("UPDATE mail_inspection_results SET phase='waiting',message='正在查询原测试邮件标识' WHERE id=? AND processed=0 AND EXISTS(SELECT 1 FROM mail_inspections WHERE id=? AND status='running')", item.ID, id)
							result, code, msg = a.pollMailInspectionInbox(ctx, client, item, opts)
						}
					}
					if ctx.Err() != nil {
						return
					}
					if err := a.saveInspectionResult(id, item, result, code, msg); err != nil {
						cancel()
						return
					}
				}
			}()
		}
	dispatch:
		for _, item := range items {
			select {
			case jobs <- item:
			case <-ctx.Done():
				break dispatch
			}
		}
		close(jobs)
		wg.Wait()
		status := "completed"
		if ctx.Err() != nil {
			status = "failed"
		}
		a.inspectionExec("UPDATE mail_inspections SET status=?,finished_at=? WHERE id=? AND status IN ('queued','running')", status, stampNow(), id)
		_, _ = a.inspectionExec("UPDATE mail_inspection_results SET phase='interrupted',message='任务中断或停止，可复检' WHERE inspection_id=? AND processed=0", id)
	}()
}
func (a *App) saveInspectionResult(task int64, item inspectionResult, result string, code int, msg string) error {
	if err := a.inspectionPrimary(); err != nil {
		return err
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE mail_inspection_results SET result=?,status_code=?,message=?,checked_at=?,processed=1,phase='finished' WHERE id=? AND inspection_id=? AND processed=0 AND EXISTS(SELECT 1 FROM mail_inspections WHERE id=? AND status='running')`, result, code, msg, stampNow(), item.ID, task, task)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	_, err = tx.Exec(`UPDATE mail_inspections SET processed=processed+1,passed=passed+?,failed=failed+?,pending=pending+? WHERE id=?`, result == "passed", result == "failed", result == "pending", task)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Sending is serialized across tasks, so the interval and rotation also apply
// when multiple inspections run together. Credentials only live in task memory.
func (a *App) sendInspectionItem(ctx context.Context, task int64, opts inspectionOptions, senders []smtpSettings, item *inspectionResult) error {
	if err := a.inspectionPrimary(); err != nil {
		return err
	}
	if _, err := a.inspectionExec("UPDATE mail_inspection_results SET phase='sending',message='等待发信' WHERE id=? AND processed=0 AND EXISTS(SELECT 1 FROM mail_inspections WHERE id=? AND status='running')", item.ID, task); err != nil {
		return err
	}
	a.inspectionSendMu.Lock()
	defer a.inspectionSendMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if item.LinkMode == "source" {
		if _, err := a.inspectionSource(ctx, *item); err != nil {
			return err
		}
	}
	if len(senders) == 0 {
		return fmt.Errorf("no senders")
	}
	if !a.inspectionSenderLoaded {
		err := a.store.db.QueryRow("SELECT id FROM mail_smtp_senders WHERE last_used_at IS NOT NULL ORDER BY last_used_at DESC,id DESC LIMIT 1").Scan(&a.inspectionLastSenderID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		a.inspectionSenderLoaded = true
	}
	if delay := time.Until(a.inspectionLastSend.Add(time.Duration(opts.Interval) * time.Millisecond)); delay > 0 && !inspectionWait(ctx, delay) {
		return ctx.Err()
	}
	start := 0
	for i, candidate := range senders {
		if candidate.ID > a.inspectionLastSenderID {
			start = i
			break
		}
	}
	var sender smtpSettings
	for offset := range senders {
		candidate := senders[(start+offset)%len(senders)]
		if candidate.ID > 0 {
			var enabled bool
			err := a.store.db.QueryRow("SELECT enabled FROM mail_smtp_senders WHERE id=?", candidate.ID).Scan(&enabled)
			if err == sql.ErrNoRows || (err == nil && !enabled) {
				continue
			}
			if err != nil {
				return err
			}
		}
		sender = candidate
		break
	}
	if sender.From == "" {
		return fmt.Errorf("没有可用的发件邮箱")
	}
	item.token, item.SenderEmail = "LUMA-"+randomToken(), sender.From
	err := func() error {
		a.store.write.Lock()
		defer a.store.write.Unlock()
		tx, err := a.store.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if sender.ID > 0 {
			var enabled bool
			if err := tx.QueryRow("SELECT enabled FROM mail_smtp_senders WHERE id=?", sender.ID).Scan(&enabled); err != nil {
				return err
			}
			if !enabled {
				return badMail("发件邮箱已停用")
			}
		}
		// Commit the token before SMTP. An ambiguous delivery is never resent by retry.
		res, err := tx.Exec(`UPDATE mail_inspection_results SET test_token=?,sender_email=?,message='正在发送测试邮件' WHERE id=? AND inspection_id=? AND processed=0 AND test_token='' AND EXISTS(SELECT 1 FROM mail_inspections WHERE id=? AND status='running')`, item.token, sender.From, item.ID, task, task)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return context.Canceled
		}
		if sender.ID > 0 {
			_, err = tx.Exec("UPDATE mail_smtp_senders SET last_used_at=? WHERE id=?", time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z"), sender.ID)
			if err != nil {
				return err
			}
		}
		return tx.Commit()
	}()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.inspectionLastSenderID, a.inspectionLastSend = sender.ID, time.Now()
	send := a.inspectionSend
	if send == nil {
		send = sendInspectionMail
	}
	sendCtx, cancel := context.WithTimeout(ctx, time.Duration(opts.Timeout)*time.Second)
	defer cancel()
	if err = send(sendCtx, sender, item.Email, item.token); err != nil {
		return err
	}
	_, err = a.inspectionExec("UPDATE mail_inspection_results SET sent_at=?,phase='waiting',message='邮件已发送，等待接码页面出现测试标识' WHERE id=? AND processed=0 AND EXISTS(SELECT 1 FROM mail_inspections WHERE id=? AND status='running')", stampNow(), item.ID, task)
	return err
}
func inspectionWait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func inspectURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("invalid URL")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicIP(ip) {
		return fmt.Errorf("private address")
	}
	return nil
}

var inspectionSpecialNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range inspectionSpecialNetworks {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func newInspectionHTTPClient(timeout int) *http.Client {
	return &http.Client{
		Transport: &http.Transport{Proxy: nil, DialContext: publicDial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: time.Duration(timeout) * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("redirect limit")
			}
			return inspectURL(req.URL)
		},
	}
}
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses")
	}
	for _, v := range ips {
		if !publicIP(v.IP) {
			return nil, fmt.Errorf("private address is not allowed")
		}
	}
	var last error
	for _, v := range ips {
		c, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(v.IP.String(), port))
		if e == nil {
			return c, nil
		}
		last = e
	}
	return nil, last
}
func checkInspectionLink(ctx context.Context, client *http.Client, link string, timeout int, token string) (string, int, string) {
	if !validLink(link) {
		return "failed", 0, "接码链接格式无效"
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "failed", 0, "链接格式无效"
	}
	req.Header.Set("User-Agent", "Luma-Mail-Inspection/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "pending", 0, "网络、超时或地址限制，需人工确认"
	}
	defer resp.Body.Close()
	code := resp.StatusCode
	if code == 429 || code >= 500 || code == 408 {
		return "pending", code, "限流或服务器暂时不可用"
	}
	if code < 200 || code >= 400 {
		return "failed", code, fmt.Sprintf("HTTP %d", code)
	}
	if token == "" {
		return "passed", code, "接码页面可访问；不代表收信验证通过"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "pending", code, "读取接码页面失败"
	}
	if strings.Contains(string(body), token) {
		return "passed", code, "已找到本次测试邮件标识"
	}
	return "pending", code, "尚未找到本次测试邮件标识"
}
func pollInspectionInbox(ctx context.Context, client *http.Client, link string, opts inspectionOptions, token string) (string, int, string) {
	if token == "" {
		return "pending", 0, "没有可查询的测试标识；请新建并确认发信任务"
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(opts.Wait)*time.Second)
	defer cancel()
	code := 0
	for waitCtx.Err() == nil {
		result, n, msg := checkInspectionLink(waitCtx, client, link, opts.Timeout, token)
		code = n
		if result == "passed" || result == "failed" {
			return result, code, msg
		}
		if !inspectionWait(waitCtx, 3*time.Second) {
			break
		}
	}
	return "pending", code, "等待结束，未确认收到本次测试邮件；可复检且不会重发"
}
func (a *App) mailInspectionDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := inspectionID(w, r)
	if !ok {
		return
	}
	task, err := scanInspection(a.store.db.QueryRow("SELECT "+inspectionColumns+" FROM mail_inspections WHERE id=?", id))
	if err != nil {
		mailError(w, err)
		return
	}
	page, limit := mailInventoryPaging(r)
	where, args := "ir.inspection_id=?", []any{id}
	if result := r.URL.Query().Get("result"); result != "" {
		if result != "passed" && result != "failed" && result != "pending" {
			fail(w, fmt.Errorf("检测结果筛选无效"))
			return
		}
		where += " AND ir.result=?"
		args = append(args, result)
	}
	var total int
	if err = a.store.db.QueryRow("SELECT COUNT(*) FROM mail_inspection_results ir WHERE "+where, args...).Scan(&total); err != nil {
		mailError(w, err)
		return
	}
	rows, err := a.store.db.Query("SELECT ir.id,ir.mailbox_id,ir.email,COALESCE(NULLIF(ir.parent_email,''),m.email,''),ir.link,ir.result,ir.status_code,ir.message,ir.checked_at,ir.processed,ir.sender_email,ir.phase,ir.started_at,ir.sent_at FROM mail_inspection_results ir LEFT JOIN mailboxes c ON c.id=ir.mailbox_id LEFT JOIN mailboxes m ON m.id=c.parent_id WHERE "+where+" ORDER BY ir.id LIMIT ? OFFSET ?", append(args, limit, (page-1)*limit)...)
	if err != nil {
		mailError(w, err)
		return
	}
	defer rows.Close()
	results := []inspectionResult{}
	for rows.Next() {
		var v inspectionResult
		if err = rows.Scan(&v.ID, &v.MailboxID, &v.Email, &v.ParentEmail, &v.Link, &v.Result, &v.StatusCode, &v.Message, &v.CheckedAt, &v.Processed, &v.SenderEmail, &v.Phase, &v.StartedAt, &v.SentAt); err != nil {
			mailError(w, err)
			return
		}
		results = append(results, v)
	}
	if err = rows.Err(); err != nil {
		mailError(w, err)
		return
	}
	rows.Close()
	counts := map[string]int{}
	stages, err := a.store.db.Query("SELECT phase,COUNT(*) FROM mail_inspection_results WHERE inspection_id=? GROUP BY phase", id)
	if err != nil {
		mailError(w, err)
		return
	}
	defer stages.Close()
	for stages.Next() {
		var phase string
		var count int
		if err = stages.Scan(&phase, &count); err != nil {
			mailError(w, err)
			return
		}
		counts[phase] = count
	}
	if err = stages.Err(); err != nil {
		mailError(w, err)
		return
	}
	stages.Close()
	var retryJSON string
	if err = a.store.db.QueryRow("SELECT retry_info FROM mail_inspections WHERE id=?", id).Scan(&retryJSON); err != nil {
		mailError(w, err)
		return
	}
	var retryInfo *inspectionRetryInfo
	if retryJSON != "" {
		if err = json.Unmarshal([]byte(retryJSON), &retryInfo); err != nil {
			mailError(w, err)
			return
		}
	}
	respond(w, 200, map[string]any{"task": task, "results": results, "total": total, "page": page, "limit": limit, "stages": counts, "retry_info": retryInfo})
}
func (a *App) mailInspectionStop(w http.ResponseWriter, r *http.Request) {
	if err := a.inspectionPrimary(); err != nil {
		mailError(w, err)
		return
	}
	id, ok := inspectionID(w, r)
	if !ok {
		return
	}
	a.inspectionMu.Lock()
	defer a.inspectionMu.Unlock()
	a.store.write.Lock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err == nil {
		err = func(tx *sql.Tx) error {
			res, e := tx.Exec("UPDATE mail_inspections SET stop_requested=1,status='stopped',finished_at=? WHERE id=? AND status IN ('queued','running')", stampNow(), id)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return mailFailure{http.StatusConflict, "任务已结束，请刷新"}
			}
			_, e = tx.Exec("UPDATE mail_inspection_results SET phase='interrupted',message='任务已停止，可复检' WHERE inspection_id=? AND processed=0", id)
			return e
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
	if cancel := a.inspectionJobs[id]; cancel != nil {
		cancel()
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) mailInspectionRetry(w http.ResponseWriter, r *http.Request) {
	if err := a.inspectionPrimary(); err != nil {
		mailError(w, err)
		return
	}
	id, ok := inspectionID(w, r)
	if !ok {
		return
	}
	a.inspectionMu.Lock()
	defer a.inspectionMu.Unlock()
	if a.closing || len(a.inspectionJobs) >= 4 {
		mailError(w, mailFailure{http.StatusConflict, "服务正在关闭或运行任务已达上限"})
		return
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.Begin()
	if err != nil {
		mailError(w, err)
		return
	}
	defer tx.Rollback()
	var mode, status, options string
	if err = tx.QueryRow("SELECT mode,status,options FROM mail_inspections WHERE id=?", id).Scan(&mode, &status, &options); err != nil {
		mailError(w, err)
		return
	}
	if status == "queued" || status == "running" {
		mailError(w, mailFailure{http.StatusConflict, "请先结束或停止原任务"})
		return
	}
	var opts inspectionOptions
	if err = json.Unmarshal([]byte(options), &opts); err != nil {
		mailError(w, err)
		return
	}
	if err = normalizeInspectionOptions(&opts); err != nil {
		mailError(w, err)
		return
	}
	// Read every non-passing result so exclusions are visible in the new task.
	// Eligibility is still checked in the same transaction as task creation.
	rows, err := tx.Query(`SELECT ir.mailbox_id,ir.email,COALESCE(NULLIF(ir.parent_email,''),m.email,''),ir.link,ir.test_token,ir.sender_email,ir.sent_at,ir.link_mode,ir.policy_key,
 COALESCE(c.status,''),COALESCE(`+mailLinkReadySQL+`,0),COALESCE(`+mailInspectionMatchesSQL+`,0),CASE WHEN c.link_mode='source' THEN COALESCE(b.reason,'') ELSE '' END
 FROM mail_inspection_results ir LEFT JOIN mailboxes c ON c.id=ir.mailbox_id
 LEFT JOIN mailboxes m ON m.id=c.parent_id LEFT JOIN mail_source_links b ON b.mailbox_id=c.id
 WHERE ir.inspection_id=? AND ir.result<>'passed' ORDER BY ir.id`, id)
	if err != nil {
		mailError(w, err)
		return
	}
	items := []inspectionResult{}
	retryInfo := inspectionRetryInfo{SourceID: id, Skipped: []inspectionRetrySkipped{}}
	for rows.Next() {
		var v inspectionResult
		var stockStatus, sourceReason string
		var linkReady, policyMatches bool
		if err = rows.Scan(&v.MailboxID, &v.Email, &v.ParentEmail, &v.Link, &v.token, &v.SenderEmail, &v.SentAt, &v.LinkMode, &v.PolicyKey, &stockStatus, &linkReady, &policyMatches, &sourceReason); err != nil {
			rows.Close()
			mailError(w, err)
			return
		}
		retryInfo.Requested++
		reason := ""
		switch {
		case stockStatus == "":
			reason = "库存已删除"
		case stockStatus == "sold":
			reason = "库存已售"
		case stockStatus == "reserved":
			reason = "库存已预留"
		case stockStatus != "available":
			reason = "库存已不在可检测状态"
		case !linkReady && sourceReason != "":
			reason = sourceReason
		case !linkReady:
			reason = "接码链接不可用或待补全"
		case !policyMatches:
			reason = "链接或来源策略已变化，请新建检测"
		}
		if reason != "" {
			retryInfo.Skipped = append(retryInfo.Skipped, inspectionRetrySkipped{Email: v.Email, ParentEmail: v.ParentEmail, Reason: reason})
		} else {
			items = append(items, v)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		mailError(w, err)
		return
	}
	if len(items) == 0 {
		if len(retryInfo.Skipped) > 0 {
			fail(w, fmt.Errorf("没有可复检项目：%d 项因库存或链接状态变化被跳过（%s）", retryInfo.Requested, retryInfo.Skipped[0].Reason))
		} else {
			fail(w, fmt.Errorf("没有未通过的检测项"))
		}
		return
	}
	retryInfo.Retried = len(items)
	newID, err := insertInspection(tx, mode, "retry", 0, opts, items, "admin")
	if err == nil {
		var retryJSON []byte
		retryJSON, err = json.Marshal(retryInfo)
		if err == nil {
			_, err = tx.Exec("UPDATE mail_inspections SET retry_info=? WHERE id=?", string(retryJSON), newID)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		mailError(w, err)
		return
	}
	a.launchInspection(newID, mode, opts, smtpSettings{}, items, true)
	respond(w, 201, map[string]any{"id": newID, "retry_info": retryInfo})
}
func (a *App) mailInspectionExport(w http.ResponseWriter, r *http.Request) {
	id, ok := inspectionID(w, r)
	if !ok {
		return
	}
	var exists int
	if err := a.store.db.QueryRow("SELECT id FROM mail_inspections WHERE id=?", id).Scan(&exists); err != nil {
		mailError(w, err)
		return
	}
	rows, err := a.store.db.Query("SELECT ir.email,COALESCE(NULLIF(ir.parent_email,''),m.email,''),ir.link,ir.result,ir.status_code,ir.message,ir.checked_at,ir.sender_email,ir.phase,COALESCE(ir.started_at,''),COALESCE(ir.sent_at,'') FROM mail_inspection_results ir LEFT JOIN mailboxes c ON c.id=ir.mailbox_id LEFT JOIN mailboxes m ON m.id=c.parent_id WHERE ir.inspection_id=? ORDER BY ir.id", id)
	if err != nil {
		mailError(w, err)
		return
	}
	defer rows.Close()
	var b strings.Builder
	b.WriteRune('\ufeff')
	cw := csv.NewWriter(&b)
	cw.Write([]string{"email", "parent_email", "link", "result", "status_code", "message", "checked_at", "sender_email", "phase", "started_at", "sent_at"})
	for rows.Next() {
		var email, parent, link, result, msg, checked, sender, phase, started, sent string
		var code int
		if err = rows.Scan(&email, &parent, &link, &result, &code, &msg, &checked, &sender, &phase, &started, &sent); err != nil {
			mailError(w, err)
			return
		}
		cw.Write([]string{csvSafe(email), csvSafe(parent), csvSafe(link), result, strconv.Itoa(code), csvSafe(msg), checked, csvSafe(sender), phase, started, sent})
	}
	if err = rows.Err(); err != nil {
		mailError(w, err)
		return
	}
	cw.Flush()
	if err = cw.Error(); err != nil {
		mailError(w, err)
		return
	}
	respond(w, 200, map[string]string{"filename": fmt.Sprintf("Luma-检测报告-%d.csv", id), "content": b.String()})
}
