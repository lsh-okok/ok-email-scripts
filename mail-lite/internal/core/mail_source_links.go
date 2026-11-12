package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type mailQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// The view mirrors canonical public resolution: an unrestricted link takes
// precedence, with the requested platform subsequently applied to its policy.
// Only indexed metadata is used here; encrypted records are read by the shared
// public resolver before delivery or internal inspection.
func (a *App) migrateMailSourceLinks() error {
	for _, c := range []struct{ table, name, definition string }{
		{"mailboxes", "link_mode", "TEXT NOT NULL DEFAULT 'custom' CHECK(link_mode IN ('custom','source'))"},
		{"mailboxes", "link_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"mail_inspection_results", "link_mode", "TEXT NOT NULL DEFAULT 'custom'"},
		{"mail_inspection_results", "policy_key", "TEXT NOT NULL DEFAULT ''"},
		{"mail_outbounds", "inspection_mode", "TEXT NOT NULL DEFAULT ''"},
		{"mail_outbounds", "inspection_valid_hours", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := a.mailColumn(c.table, c.name, c.definition); err != nil {
			return err
		}
	}
	_, err := a.store.db.Exec(`CREATE VIEW IF NOT EXISTS mail_source_links AS
 SELECT c.id mailbox_id,sl.id source_id,sl.account_id,sl.recipient,sl.platform_id,
 a.public_code account_code,COALESCE(p.email,'全部平台') platform_name,
 e.id effective_id,e.expires_at,
 COALESCE(sl.id,'')||':'||COALESCE(sl.version,0)||':'||COALESCE(e.id,'')||':'||COALESCE(e.version,0)||':'||COALESCE(a.id,'')||':'||COALESCE(a.version,0)||':'||COALESCE(p.version,0)||':'||c.link_revision policy_key,
 CASE WHEN sl.id IS NULL THEN '来源链接已删除或未关联'
 WHEN a.id IS NULL OR pm.source_id IS NULL THEN '来源主账号已删除或归属不一致'
 WHEN c.email<>sl.recipient COLLATE NOCASE OR c.email<>sm.source_email COLLATE NOCASE OR pm.source_id<>sl.account_id THEN '来源收件人或主账号已变化'
 WHEN a.status<>'active' THEN '来源主账号已停用'
 WHEN sl.platform_id<>'' AND p.id IS NULL THEN '来源平台规则不可用'
 WHEN e.id IS NULL THEN '取信链接无法解析'
 WHEN e.expires_at<>'' AND (julianday(e.expires_at) IS NULL OR julianday(e.expires_at)<=julianday('now')) THEN '取信链接已过期'
 ELSE '' END reason,
 EXISTS(SELECT 1 FROM records other WHERE other.kind='link' AND other.account_id=sl.account_id
 AND substr(other.recipient,1,instr(other.recipient,'@')-1)=substr(sl.recipient,1,instr(sl.recipient,'@')-1) COLLATE NOCASE
 AND other.recipient<>sl.recipient COLLATE NOCASE AND (other.platform_id='' OR other.platform_id=sl.platform_id)) ambiguous
 FROM mailboxes c
 LEFT JOIN mail_source_mappings sm ON sm.source_kind='link' AND sm.mailbox_id=c.id
 LEFT JOIN records sl ON sl.kind='link' AND sl.id=sm.source_id
 LEFT JOIN mail_source_mappings pm ON pm.source_kind='account' AND pm.mailbox_id=c.parent_id
 LEFT JOIN records a ON a.kind='account' AND a.id=sl.account_id
 LEFT JOIN records unrestricted ON unrestricted.kind='link' AND unrestricted.account_id=sl.account_id AND unrestricted.recipient=sl.recipient COLLATE NOCASE AND unrestricted.platform_id=''
 LEFT JOIN records e ON e.kind='link' AND e.id=COALESCE(unrestricted.id,sl.id)
 LEFT JOIN records p ON p.kind='platform' AND p.id=sl.platform_id
 WHERE c.kind='child';`)
	return err
}

const mailLinkReadySQL = `((c.link_mode='custom' AND c.link<>'') OR (c.link_mode='source' AND EXISTS(SELECT 1 FROM mail_source_links b WHERE b.mailbox_id=c.id AND b.reason='')))`
const mailPolicySQL = `(CASE WHEN c.link_mode='source' THEN COALESCE((SELECT b.policy_key FROM mail_source_links b WHERE b.mailbox_id=c.id),'missing') WHEN c.link_revision=0 THEN '' ELSE 'custom:'||c.link_revision END)`
const mailInspectionMatchesSQL = `(ir.link_mode=c.link_mode AND ir.policy_key=` + mailPolicySQL + ` AND (c.link_mode='source' OR ir.link=c.link))`

type mailSourceLink struct {
	Link          string `json:"link"`
	LinkMode      string `json:"link_mode"`
	LinkAvailable bool   `json:"link_available"`
	LinkReason    string `json:"link_reason"`
	Platform      string `json:"link_platform"`
	ExpiresAt     string `json:"link_expires_at"`
	LocalOnly     bool   `json:"link_local_only"`
	PolicyKey     string `json:"-"`
	SourceID      string `json:"-"`
	AccountID     string `json:"-"`
	Recipient     string `json:"-"`
	PlatformID    string `json:"-"`
	Effective     Link   `json:"-"`
	basePath      string
}

func localMailURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func (a *App) sourceLink(ctx context.Context, q mailQuery, id int64, base string, verify bool) (mailSourceLink, error) {
	out := mailSourceLink{LinkMode: "source"}
	baseURL, err := url.Parse(base)
	if err != nil {
		return out, err
	}
	out.basePath = strings.TrimRight(baseURL.EscapedPath(), "/")
	var code, platformID string
	var ambiguous bool
	err = q.QueryRowContext(ctx, `SELECT COALESCE(source_id,''),COALESCE(account_id,''),COALESCE(recipient,''),COALESCE(platform_id,''),COALESCE(account_code,''),platform_name,COALESCE(expires_at,''),policy_key,reason,ambiguous FROM mail_source_links WHERE mailbox_id=?`, id).Scan(&out.SourceID, &out.AccountID, &out.Recipient, &platformID, &code, &out.Platform, &out.ExpiresAt, &out.PolicyKey, &out.LinkReason, &ambiguous)
	if err != nil {
		return out, err
	}
	if out.SourceID != "" {
		out.Link, _ = publicLinkURLs(base, Link{AccountCode: code, AccountID: out.AccountID, Recipient: out.Recipient, PlatformID: platformID}, out.Platform)
		if ambiguous {
			i := strings.LastIndexByte(out.Link, '/')
			out.Link = out.Link[:i+1] + url.PathEscape(out.Recipient)
		}
		out.LocalOnly = localMailURL(out.Link)
	}
	if out.LinkReason != "" {
		return out, nil
	}
	out.PlatformID = platformID
	if verify {
		effective, err := a.verifyMailSourceURL(ctx, q, out, out.Link)
		if err != nil {
			out.LinkReason = "取信链接与销售邮箱不一致或无法解析"
			return out, nil
		}
		out.Effective = effective
	}
	out.LinkAvailable = true
	return out, nil
}

func (a *App) verifyMailSourceURL(ctx context.Context, q mailQuery, source mailSourceLink, raw string) (Link, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return Link{}, err
	}
	// A reverse proxy may mount the public site below a path such as /mail.
	// Strip only the configured prefix, then resolve the original canonical
	// route and check its account, recipient and platform against this source.
	path := r.URL.EscapedPath()
	if source.basePath != "" {
		if !strings.HasPrefix(path, source.basePath+"/") {
			return Link{}, ErrNotFound
		}
		path = strings.TrimPrefix(path, source.basePath)
	}
	platform, account, recipient, ok := readableLinkParts(path)
	if !ok {
		return Link{}, ErrNotFound
	}
	segment := unrestrictedPlatformSegment
	if platform != "" {
		segment = "platform~" + platform
	}
	r.SetPathValue("platform", segment)
	r.SetPathValue("account", account)
	r.SetPathValue("recipient", recipient)
	effective, err := a.requestLinkWith(ctx, q, r)
	if err != nil {
		return effective, err
	}
	if effective.AccountID != source.AccountID || !strings.EqualFold(effective.Recipient, source.Recipient) || effective.PlatformID != source.PlatformID {
		return Link{}, ErrConflict
	}
	return effective, nil
}

// Keep custom links opaque: only an explicit persisted source mode may use
// internal lookup, never a hostname or URL that happens to point to this app.
func (a *App) enrichMailChild(ctx context.Context, q mailQuery, c *MailChild, base string) error {
	if c.LinkMode != "source" {
		c.LinkAvailable = validLink(c.Link)
		c.LinkLocalOnly = localMailURL(c.Link)
		if !c.LinkAvailable {
			c.LinkReason = "接码链接待补全"
		}
		return nil
	}
	s, err := a.sourceLink(ctx, q, c.ID, base, false)
	if err != nil {
		return err
	}
	c.Link = s.Link
	c.LinkAvailable = s.LinkAvailable
	c.LinkReason = s.LinkReason
	c.LinkPlatform = s.Platform
	c.LinkExpiresAt = s.ExpiresAt
	c.LinkLocalOnly = s.LocalOnly
	return nil
}

type mailLinkSyncItem struct {
	ID     int64  `json:"id"`
	Email  string `json:"email"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	Link   string `json:"link,omitempty"`
}
type mailLinkSyncPreview struct {
	Ready   int                `json:"ready"`
	Skipped int                `json:"skipped"`
	Failed  int                `json:"failed"`
	Version string             `json:"version"`
	Items   []mailLinkSyncItem `json:"items"`
}

func (a *App) previewMailLinkSync(ctx context.Context, tx *sql.Tx, ids []int64, base string) (mailLinkSyncPreview, error) {
	out := mailLinkSyncPreview{Items: []mailLinkSyncItem{}}
	where, args := "sm.source_kind='link'", []any{}
	if len(ids) > 0 {
		ph, p := mailMarks(ids)
		where = "c.kind='child' AND c.id IN (" + ph + ")"
		args = p
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.email,c.status,c.link,c.link_mode,c.updated_at,sm.source_id IS NOT NULL FROM mailboxes c LEFT JOIN mail_source_mappings sm ON sm.mailbox_id=c.id AND sm.source_kind='link' WHERE `+where+` ORDER BY c.id LIMIT 20001`, args...)
	if err != nil {
		return out, err
	}
	type row struct {
		id                                 int64
		email, status, link, mode, updated string
		mapped                             bool
	}
	all := []row{}
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.email, &r.status, &r.link, &r.mode, &r.updated, &r.mapped); err != nil {
			rows.Close()
			return out, err
		}
		all = append(all, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(all) > 20000 {
		return out, badMail("一次最多补齐 20000 条，请选择子邮箱分批处理")
	}
	var version strings.Builder
	for _, r := range all {
		item := mailLinkSyncItem{ID: r.id, Email: r.email, Status: "skipped"}
		key := ""
		if !r.mapped {
			item.Reason = "没有关联来源，保留自定义库存"
		} else if r.status != "available" {
			item.Reason = "预留或已售库存保持不变"
		} else if r.mode == "source" || r.link != "" {
			item.Reason = "已关联或已填写链接，保持不变"
		} else {
			s, e := a.sourceLink(ctx, tx, r.id, base, true)
			if e != nil {
				return out, e
			}
			key = s.PolicyKey
			if s.LinkAvailable {
				item.Status = "ready"
				item.Link = s.Link
				out.Ready++
			} else {
				item.Status = "failed"
				item.Reason = s.LinkReason
				out.Failed++
			}
		}
		if item.Status == "skipped" {
			out.Skipped++
		}
		v, _ := json.Marshal([]any{r.id, r.updated, r.status, r.link, r.mode, item.Status, key, item.Link})
		version.Write(v)
		out.Items = append(out.Items, item)
	}
	out.Version = digest(version.String())
	return out, nil
}

func (a *App) mailLinkSync(w http.ResponseWriter, r *http.Request) {
	if err := a.inspectionPrimary(); err != nil {
		mailError(w, err)
		return
	}
	var in struct {
		IDs     []int64 `json:"ids"`
		Version string  `json:"version"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.IDs) > 0 {
		ids, err := uniqueMailInventoryIDs(in.IDs)
		if err != nil {
			mailError(w, err)
			return
		}
		in.IDs = ids
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if len(in.IDs) > 0 {
		if err := requireMailInventoryIDs(tx, in.IDs, "child"); err != nil {
			mailError(w, err)
			return
		}
	}
	out, err := a.previewMailLinkSync(r.Context(), tx, in.IDs, a.baseURL(r))
	if err != nil {
		mailError(w, err)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/preview") {
		respond(w, 200, out)
		return
	}
	if in.Version == "" || in.Version != out.Version {
		respond(w, 409, map[string]string{"error": "库存或来源已变化，请重新预览补齐范围"})
		return
	}
	for _, item := range out.Items {
		if item.Status != "ready" {
			continue
		}
		if _, err = tx.ExecContext(r.Context(), "UPDATE mailboxes SET link_mode='source',link='',link_revision=link_revision+1,updated_at=? WHERE id=? AND status='available' AND link=''", stampNow(), item.ID); err != nil {
			fail(w, err)
			return
		}
		if err = mailHistory(tx, item.ID, "admin", "mail.link.associated", "自动关联现有取信链接"); err != nil {
			fail(w, err)
			return
		}
	}
	if out.Ready > 0 {
		err = recordMailActivity(r.Context(), tx, MailActivity{Action: "mail.links.associated", Scope: "child", SelectionCount: out.Ready, ChildCount: out.Ready})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, map[string]any{"updated": out.Ready, "skipped": out.Skipped, "failed": out.Failed})
}

func (a *App) resolveOutboundLinks(ctx context.Context, tx *sql.Tx, items []outboundCandidate, base string) error {
	for i := range items {
		var mode string
		if err := tx.QueryRowContext(ctx, "SELECT link_mode FROM mailboxes WHERE id=?", items[i].ID).Scan(&mode); err != nil {
			return err
		}
		if mode != "source" {
			continue
		}
		s, err := a.sourceLink(ctx, tx, items[i].ID, base, true)
		if err != nil {
			return err
		}
		if !s.LinkAvailable {
			return fmt.Errorf("%w：%s", ErrConflict, s.LinkReason)
		}
		items[i].Link = s.Link
	}
	return nil
}

func (a *App) inspectionSource(ctx context.Context, item inspectionResult) (mailSourceLink, error) {
	if item.MailboxID == nil {
		return mailSourceLink{}, fmt.Errorf("来源库存不存在")
	}
	var mode string
	if err := a.store.db.QueryRowContext(ctx, "SELECT link_mode FROM mailboxes WHERE id=?", *item.MailboxID).Scan(&mode); err != nil {
		return mailSourceLink{}, err
	}
	if mode != "source" {
		return mailSourceLink{}, fmt.Errorf("链接模式已变化，请新建检测")
	}
	s, err := a.sourceLink(ctx, a.store.db, *item.MailboxID, "http://internal.invalid", true)
	if err != nil {
		return s, err
	}
	if !s.LinkAvailable {
		return s, fmt.Errorf("%s", s.LinkReason)
	}
	if s.PolicyKey != item.PolicyKey {
		return s, fmt.Errorf("来源策略已变化，请新建检测")
	}
	return s, nil
}

func (a *App) checkMailInspectionLink(ctx context.Context, client *http.Client, item inspectionResult, timeout int, token string) (string, int, string) {
	if item.LinkMode != "source" {
		return checkInspectionLink(ctx, client, item.Link, timeout, token)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	s, err := a.inspectionSource(ctx, item)
	if err != nil {
		return "failed", 0, err.Error()
	}
	_, messages, err := a.readPublicLink(ctx, s.Effective, 20)
	if err != nil {
		return "pending", 0, "系统取信暂未成功，请稍后复检"
	}
	if token == "" {
		return "passed", 0, "系统链接与内部取信正常；未验证公网可达性或实际收信"
	}
	for _, m := range messages {
		if strings.Contains(m.Subject, token) || strings.Contains(m.Text, token) || strings.Contains(m.HTML, token) {
			return "passed", 0, "已通过相同取信规则找到本次测试邮件标识"
		}
	}
	if s.Effective.PlatformID != "" {
		return "pending", 0, "尚未找到测试标识；测试邮件可能被该链接的平台规则过滤"
	}
	return "pending", 0, "尚未找到本次测试邮件标识"
}

func (a *App) pollMailInspectionInbox(ctx context.Context, client *http.Client, item inspectionResult, opts inspectionOptions) (string, int, string) {
	if item.LinkMode != "source" {
		return pollInspectionInbox(ctx, client, item.Link, opts, item.token)
	}
	if item.token == "" {
		return "pending", 0, "没有可查询的测试标识；请新建并确认发信任务"
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(opts.Wait)*time.Second)
	defer cancel()
	message := "等待结束，未确认收到本次测试邮件；可复检且不会重发"
	for ctx.Err() == nil {
		result, code, msg := a.checkMailInspectionLink(ctx, client, item, opts.Timeout, item.token)
		message = msg
		if result != "pending" {
			return result, code, msg
		}
		if !inspectionWait(ctx, 3*time.Second) {
			break
		}
	}
	return "pending", 0, message
}
