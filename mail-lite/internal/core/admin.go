package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

func (a *App) registerAdmin(m *http.ServeMux) {
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /api/admin/stats": a.stats, "GET /api/admin/accounts": a.accounts, "POST /api/admin/accounts": a.saveAccount, "DELETE /api/admin/accounts/{id}": a.deleteAccount,
		"GET /api/admin/request-stats":              a.requestStats,
		"POST /api/admin/accounts/{id}/credentials": a.accountCredentials, "POST /api/admin/accounts/export": a.exportAccounts,
		"GET /api/admin/accounts/{id}/mx":       a.mxAccountOverview,
		"GET /api/admin/accounts/{id}/messages": a.accountMessages, "GET /api/admin/accounts/{id}/message": a.accountMessage, "POST /api/admin/accounts/{id}/messages/delete": a.deleteMessages,
		"GET /api/admin/groups": a.groups, "POST /api/admin/groups": a.saveGroup, "DELETE /api/admin/groups/{id}": a.deleteGroup,
		"GET /api/admin/platforms": a.platforms, "POST /api/admin/platforms": a.savePlatform, "DELETE /api/admin/platforms/{id}": a.deletePlatform,
		"GET /api/admin/links": a.links, "GET /api/admin/links/selection": a.linkSelection, "POST /api/admin/links/batch": a.batchLinks, "GET /api/admin/links/export": a.exportLinks,
		"GET /api/admin/settings": a.settings, "PUT /api/admin/settings": a.saveSettings, "POST /api/admin/import": a.importJob, "POST /api/admin/refresh": a.refreshJob,
		"PUT /api/admin/password": a.changeAdminPassword,
		"GET /api/admin/jobs":     a.listJobs, "GET /api/admin/nodes": a.nodes, "POST /api/admin/nodes": a.createNode, "DELETE /api/admin/nodes/{id}": a.revokeNode,
		"PUT /api/admin/nodes/{id}/status": a.setNodeStatus, "DELETE /api/admin/nodes/{id}/record": a.deleteNodeRecord,
		"GET /api/admin/mail/stats": a.mailSalesStats, "GET /api/admin/mail/mapping/preview": a.mailMappingPreview, "POST /api/admin/mail/mapping": a.mailMappingCommit,
		"GET /api/admin/mail/folders": a.mailFolders, "POST /api/admin/mail/folders": a.createMailFolder, "PUT /api/admin/mail/folders/{id}": a.updateMailFolder, "DELETE /api/admin/mail/folders/{id}": a.deleteMailFolder,
		"GET /api/admin/mail/masters": a.mailMasters, "GET /api/admin/mail/masters/{id}": a.mailMasterDetail, "POST /api/admin/mail/masters": a.createMailMaster, "PUT /api/admin/mail/masters/{id}": a.updateMailMaster,
		"POST /api/admin/mail/masters/batch": a.mailMasterBatch, "GET /api/admin/mail/children": a.mailChildren, "GET /api/admin/mail/children/{id}/history": a.childMailHistory, "POST /api/admin/mail/children": a.createMailChild, "PUT /api/admin/mail/children/{id}": a.updateMailChild, "POST /api/admin/mail/children/flags": a.mailFlags,
		"POST /api/admin/mail/import/preview": a.mailImportPreview, "POST /api/admin/mail/import/commit": a.mailImportCommit, "POST /api/admin/mail/export": a.mailExport,
		"GET /api/admin/mail/activities":                  a.mailActivities,
		"POST /api/admin/mail/children/link-sync/preview": a.mailLinkSync,
		"POST /api/admin/mail/children/link-sync":         a.mailLinkSync,
		"GET /api/admin/mail/outbounds":                   a.mailOutboundList, "GET /api/admin/mail/outbounds/{id}": a.mailOutboundDetail, "POST /api/admin/mail/outbounds": a.createMailOutbound, "POST /api/admin/mail/outbounds/{id}/action": a.updateMailOutbound, "GET /api/admin/mail/outbounds/{id}/export": a.mailOutboundExport,
		"POST /api/admin/mail/quick-outbound": a.createMailOutbound, "POST /api/admin/mail/status": a.mailStatusUpdate, "POST /api/admin/mail/children/status": a.mailChildStatusUpdate, "GET /api/admin/mail/selection": a.mailSelection, "GET /api/admin/mail/stock": a.mailStock,
		"GET /api/admin/mail/settings": a.getMailSMTPSettings, "PUT /api/admin/mail/settings": a.saveMailSMTPSettings,
		"GET /api/admin/mail/senders": a.mailSenders, "POST /api/admin/mail/senders": a.saveMailSender, "PUT /api/admin/mail/senders/{id}": a.saveMailSender, "DELETE /api/admin/mail/senders/{id}": a.deleteMailSender,
		"GET /api/admin/mail/inspections": a.mailInspectionList, "POST /api/admin/mail/inspections": a.createMailInspection, "GET /api/admin/mail/inspections/{id}": a.mailInspectionDetail, "GET /api/admin/mail/inspections/{id}/export": a.mailInspectionExport, "POST /api/admin/mail/inspections/{id}/stop": a.mailInspectionStop, "POST /api/admin/mail/inspections/{id}/retry": a.mailInspectionRetry,
		"GET /api/admin/mail/inspection-schedule": a.getInspectionSchedule, "PUT /api/admin/mail/inspection-schedule": a.saveInspectionSchedule, "POST /api/admin/mail/inspection-schedule/run": a.runInspectionSchedule,
	} {
		m.HandleFunc(pattern, a.admin(handler))
	}
}
func (a *App) stats(w http.ResponseWriter, r *http.Request) {
	out := map[string]int{}
	for _, kind := range []string{"account", "link", "group", "platform"} {
		var count int
		if e := a.store.db.QueryRowContext(r.Context(), "SELECT count(*) FROM records WHERE kind=?", kind).Scan(&count); e != nil {
			fail(w, e)
			return
		}
		out[kind+"s"] = count
	}
	var outlook, icloud, active, disabled int
	if e := a.store.db.QueryRowContext(r.Context(), `SELECT
		COALESCE(sum(provider='outlook'),0), COALESCE(sum(provider='icloud'),0),
		COALESCE(sum(status='active'),0), COALESCE(sum(status='disabled'),0)
		FROM records WHERE kind='account'`).Scan(&outlook, &icloud, &active, &disabled); e != nil {
		fail(w, e)
		return
	}
	out["outlook_accounts"], out["icloud_accounts"] = outlook, icloud
	out["active_accounts"], out["disabled_accounts"] = active, disabled
	var mxCount int
	if err := a.store.db.QueryRowContext(r.Context(), "SELECT count(*) FROM records WHERE kind='account' AND provider='mx'").Scan(&mxCount); err != nil {
		fail(w, err)
		return
	}
	out["mx_accounts"] = mxCount
	respond(w, 200, out)
}
func publicAccount(v Account) Account {
	v.HasCredential = v.Password != "" || v.RefreshToken != ""
	v.Password = ""
	v.RefreshToken = ""
	return v
}
func (a *App) accounts(w http.ResponseWriter, r *http.Request) {
	p, n := pagination(r)
	where, args := accountFilter(r.URL.Query())
	records, total, e := a.store.List(r.Context(), where, args, n, (p-1)*n)
	if e != nil {
		fail(w, e)
		return
	}
	counts, e := a.accountLinkCounts(r.Context(), records)
	if e != nil {
		fail(w, e)
		return
	}
	out := []accountView{}
	for _, record := range records {
		var v Account
		if e := json.Unmarshal(record.Data, &v); e != nil {
			fail(w, e)
			return
		}
		v.Version = record.Version
		out = append(out, accountView{publicAccount(v), counts[v.ID]})
	}
	respond(w, 200, Page{out, total, p, n})
}
func validateAccount(v *Account) error {
	if v.Provider == "mx" {
		if err := validateMX(v); err != nil {
			return err
		}
	}
	v.Email = address(v.Email)
	if v.Email == "" {
		return errors.New("邮箱地址格式不正确")
	}
	if v.Provider != "outlook" && v.Provider != "icloud" && v.Provider != "mx" {
		return errors.New("仅支持 Outlook、iCloud 和 MX")
	}
	if v.Status == "" {
		v.Status = "active"
	}
	if v.Status != "active" && v.Status != "disabled" {
		return errors.New("无效的邮箱状态")
	}
	if v.Provider == "outlook" && (v.ClientID == "" || v.RefreshToken == "") {
		return errors.New("Outlook 需要 Client ID 和 Refresh Token")
	}
	if v.Provider == "icloud" && v.Password == "" {
		return errors.New("iCloud 需要应用专用密码")
	}
	return validateProxies(v.Proxies)
}
func (a *App) saveAccount(w http.ResponseWriter, r *http.Request) {
	var v Account
	if !readJSON(w, r, &v) {
		return
	}
	a.mutations.Lock()
	defer a.mutations.Unlock()
	var expected *int64
	if v.ID != "" {
		old, e := a.store.Account(r.Context(), v.ID)
		if e != nil {
			fail(w, e)
			return
		}
		if v.Version != old.Version {
			fail(w, ErrConflict)
			return
		}
		if old.Provider == "mx" || v.Provider == "mx" {
			if old.Provider != v.Provider {
				fail(w, errors.New("MX 与其他邮箱类型不能互相转换"))
				return
			}
			if err := validateMX(&v); err != nil {
				fail(w, err)
				return
			}
			if old.MXDomain != v.MXDomain {
				fail(w, errors.New("已创建的 MX 域名不可修改，请新增域名"))
				return
			}
		}
		version := old.Version
		expected = &version
		if v.RefreshToken == "" {
			v.RefreshToken = old.RefreshToken
		}
		if v.Password == "" {
			v.Password = old.Password
		}
		v.Generation = old.Generation + 1
		v.LastRefresh = old.LastRefresh
		v.RefreshError = old.RefreshError
	} else {
		v.ID = randomID()
	}
	if e := validateAccount(&v); e != nil {
		fail(w, e)
		return
	}
	record, e := a.store.Save(r.Context(), v, expected)
	if e != nil {
		fail(w, e)
		return
	}
	a.cache.Invalidate(v.ID)
	a.tokens.Invalidate(v.ID)
	decode(record.Data, &v)
	v.Version = record.Version
	respond(w, 200, publicAccount(v))
}
func (a *App) deleteAccount(w http.ResponseWriter, r *http.Request) {
	a.mutations.Lock()
	defer a.mutations.Unlock()
	id := r.PathValue("id")
	if e := a.store.Delete(r.Context(), "account", []string{id}); e != nil {
		fail(w, e)
		return
	}
	a.cache.Invalidate(id)
	a.tokens.Invalidate(id)
	if a.mx != nil {
		if _, err := a.mx.db.ExecContext(r.Context(), "DELETE FROM mx_messages WHERE account_id=?", id); err != nil {
			fail(w, err)
			return
		}
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) accountMessages(w http.ResponseWriter, r *http.Request) {
	v, e := a.store.Account(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	page, e := a.adminMessagesPage(r.Context(), v, r.URL.Query())
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, page)
}
func (a *App) accountMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, e := a.store.Account(r.Context(), id)
	if e != nil {
		fail(w, e)
		return
	}
	ref, e := parseRef(a.key, r.URL.Query().Get("ref"), id)
	if e != nil {
		fail(w, e)
		return
	}
	msg, e := a.mail.Detail(r.Context(), v, ref)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, msg)
}
func (a *App) deleteMessages(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Refs []string `json:"refs"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.Refs) == 0 || len(body.Refs) > 100 {
		fail(w, errors.New("每次请选择 1–100 封邮件"))
		return
	}
	v, e := a.store.Account(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	values, e := a.mail.Delete(ctx, v, body.Refs)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, map[string]any{"results": values})
}
func (a *App) groups(w http.ResponseWriter, r *http.Request) {
	rs, e := a.store.All(r.Context(), "group")
	if e != nil {
		fail(w, e)
		return
	}
	counts := map[string]int{}
	rows, e := a.store.db.QueryContext(r.Context(), "SELECT parent_id,count(*) FROM records WHERE kind='account' GROUP BY parent_id")
	if e != nil {
		fail(w, e)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var count int
		if e := rows.Scan(&id, &count); e != nil {
			fail(w, e)
			return
		}
		counts[id] = count
	}
	if e := rows.Err(); e != nil {
		fail(w, e)
		return
	}
	out := make([]Group, 0, len(rs))
	for _, record := range rs {
		var group Group
		if e := json.Unmarshal(record.Data, &group); e != nil {
			fail(w, e)
			return
		}
		group.AccountCount = counts[group.ID]
		out = append(out, group)
	}
	respond(w, 200, map[string]any{"items": out, "unassigned_count": counts[""]})
}
func (a *App) platforms(w http.ResponseWriter, r *http.Request) { a.recordList(w, r, "platform") }
func (a *App) recordList(w http.ResponseWriter, r *http.Request, kind string) {
	rs, e := a.store.All(r.Context(), kind)
	if e != nil {
		fail(w, e)
		return
	}
	out := []json.RawMessage{}
	for _, v := range rs {
		out = append(out, v.Data)
	}
	respond(w, 200, map[string]any{"items": out})
}
func (a *App) saveGroup(w http.ResponseWriter, r *http.Request) {
	var v Group
	if !readJSON(w, r, &v) {
		return
	}
	// Counts are a response-only field used by the group navigator.
	v.AccountCount = 0
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" || len([]rune(v.Name)) > 80 {
		fail(w, errors.New("分组名称需要 1–80 个字符"))
		return
	}
	if e := validateProxies(v.Proxies); e != nil {
		fail(w, e)
		return
	}
	if v.ID == "" {
		v.ID = randomID()
	}
	a.mutations.Lock()
	defer a.mutations.Unlock()
	rs, e := a.store.All(r.Context(), "group")
	if e != nil {
		fail(w, e)
		return
	}
	groups := map[string]Group{v.ID: v}
	for _, r := range rs {
		if r.ID != v.ID {
			var g Group
			decode(r.Data, &g)
			groups[g.ID] = g
		}
	}
	for _, g := range groups {
		seen := map[string]bool{}
		for g.ID != "" {
			if seen[g.ID] || len(seen) >= 3 {
				fail(w, errors.New("分组不能循环，且最多三级"))
				return
			}
			seen[g.ID] = true
			if g.ParentID == "" {
				break
			}
			next, ok := groups[g.ParentID]
			if !ok {
				fail(w, errors.New("上级分组不存在"))
				return
			}
			g = next
		}
	}
	if _, e = a.store.Save(r.Context(), v, nil); e != nil {
		fail(w, e)
		return
	}
	a.cache.Invalidate("")
	respond(w, 200, v)
}
func (a *App) deleteGroup(w http.ResponseWriter, r *http.Request) {
	a.mutations.Lock()
	defer a.mutations.Unlock()
	if e := a.store.Delete(r.Context(), "group", []string{r.PathValue("id")}); e != nil {
		fail(w, e)
		return
	}
	a.cache.Invalidate("")
	respond(w, 200, map[string]bool{"ok": true})
}
func cleanList(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" && !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			out = append(out, s)
		}
	}
	return out
}
func (a *App) savePlatform(w http.ResponseWriter, r *http.Request) {
	var v Platform
	if !readJSON(w, r, &v) {
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	v.Emails = cleanList(v.Emails)
	v.Domains = cleanList(v.Domains)
	v.Keywords = cleanList(v.Keywords)
	if v.Name == "" || len([]rune(v.Name)) > 80 || len(v.Emails)+len(v.Domains) == 0 {
		fail(w, errors.New("填写平台名称及至少一个发件地址或域名"))
		return
	}
	for i, s := range v.Emails {
		if address(s) == "" {
			fail(w, errors.New("发件邮箱格式错误"))
			return
		}
		v.Emails[i] = address(s)
	}
	for i, s := range v.Domains {
		s = strings.ToLower(s)
		if strings.ContainsAny(s, "/@:* ") || address("test@"+s) == "" {
			fail(w, errors.New("发件域名格式错误"))
			return
		}
		v.Domains[i] = s
	}
	if v.ID == "" {
		v.ID = randomID()
	}
	if _, e := a.store.Save(r.Context(), v, nil); e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, v)
}
func (a *App) deletePlatform(w http.ResponseWriter, r *http.Request) {
	if e := a.store.Delete(r.Context(), "platform", []string{r.PathValue("id")}); e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func linkFilter(r *http.Request) (string, []any) {
	where := "kind='link'"
	args := []any{}
	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		if platform, account, recipient, ok := readableLinkParts(v); ok {
			// A platform segment can be selected at request time for an
			// unrestricted link, so pasted platform variants must find both the
			// unrestricted record and legacy links bound to that platform.
			where += " AND account_id IN (SELECT id FROM records WHERE kind='account' AND (public_code=? OR id=?)) AND " + recipientPredicate(recipient) + " AND (platform_id='' OR platform_id=?)"
			args = append(args, account, account, recipient, platform)
		} else {
			where += " AND (recipient LIKE ? OR token_hash=? OR account_id=? OR account_id IN (SELECT id FROM records WHERE kind='account' AND public_code=?))"
			args = append(args, "%"+v+"%", digest(linkSearchToken(v)), v, v)
		}
	}
	if v := q.Get("account_id"); v != "" {
		where += " AND account_id=?"
		args = append(args, v)
	}
	if v := q.Get("platform_id"); v != "" {
		where += " AND platform_id=?"
		args = append(args, v)
	}
	return where, args
}
func (a *App) baseURL(r *http.Request) string {
	s := a.store.Settings(r.Context()).PublicBaseURL
	if s != "" {
		return strings.TrimRight(s, "/")
	}
	if a.cfg.ExternalOrigin != "" {
		return a.cfg.ExternalOrigin
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
func (a *App) links(w http.ResponseWriter, r *http.Request) {
	p, n := pagination(r)
	// Link management can display a whole mailbox without changing the page
	// size limits used by account and other administration lists.
	if requested, _ := strconv.Atoi(r.URL.Query().Get("page_size")); requested > n {
		n = min(requested, 750)
	}
	where, args := linkFilter(r)
	rs, total, e := a.store.List(r.Context(), where, args, n, (p-1)*n)
	if e != nil {
		fail(w, e)
		return
	}
	counts, e := a.linkAccessCounts(r.Context(), rs)
	if e != nil {
		fail(w, e)
		return
	}
	platforms, e := a.platformNames(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	out := []linkView{}
	accounts := map[string]Account{}
	base := a.baseURL(r)
	for _, record := range rs {
		var v Link
		decode(record.Data, &v)
		if _, ok := accounts[v.AccountID]; !ok {
			account, e := a.store.Account(r.Context(), v.AccountID)
			if e == nil {
				accounts[v.AccountID] = account
			}
		}
		v.AccountEmail = accounts[v.AccountID].Email
		v.AccountCode = accounts[v.AccountID].PublicCode
		v.PlatformName = platforms[v.PlatformID]
		v.URL, v.JSONURL = publicLinkURLs(base, v, v.PlatformName)
		v.Token = ""
		v.TokenHash = ""
		out = append(out, linkView{v, counts[v.ID]})
	}
	respond(w, 200, Page{out, total, p, n})
}

const maxBatchLinks = 10000

func (a *App) linkSelection(w http.ResponseWriter, r *http.Request) {
	accountID := r.URL.Query().Get("account_id")
	if strings.TrimSpace(accountID) == "" {
		fail(w, errors.New("请先选择主邮箱"))
		return
	}
	// Only verify the indexed ID. Selection does not need to decrypt account
	// credentials or link data, and it never changes the selected records.
	var exists bool
	if e := a.store.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM records WHERE kind='account' AND id=?)", accountID).Scan(&exists); e != nil {
		fail(w, e)
		return
	}
	if !exists {
		fail(w, ErrNotFound)
		return
	}
	where, args := linkFilter(r)
	args = append(args, maxBatchLinks+1)
	rows, e := a.store.db.QueryContext(r.Context(), "SELECT id FROM records WHERE "+where+" ORDER BY id LIMIT ?", args...)
	if e != nil {
		fail(w, e)
		return
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			fail(w, e)
			return
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		fail(w, e)
		return
	}
	if len(ids) > maxBatchLinks {
		fail(w, fmt.Errorf("每次最多选择 %d 条链接，请缩小筛选范围", maxBatchLinks))
		return
	}
	respond(w, 200, map[string]any{"ids": ids, "total": len(ids)})
}

func validExpiry(s string) bool {
	if s == "" {
		return true
	}
	_, e := time.Parse(time.RFC3339, s)
	return e == nil
}
func (a *App) batchLinks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
		Value  string   `json:"value"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > maxBatchLinks {
		fail(w, fmt.Errorf("每次请选择 1–%d 条链接", maxBatchLinks))
		return
	}
	ids := make([]string, 0, len(body.IDs))
	seen := make(map[string]bool, len(body.IDs))
	for _, id := range body.IDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	body.IDs = ids
	a.mutations.Lock()
	defer a.mutations.Unlock()
	if body.Action == "delete" {
		if e := a.store.Delete(r.Context(), "link", body.IDs); e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	if body.Action != "platform" && body.Action != "expiry" {
		fail(w, errors.New("仅支持修改平台、有效期或删除链接"))
		return
	}
	if body.Action == "expiry" && !validExpiry(body.Value) {
		fail(w, errors.New("有效期需要 ISO 8601 日期时间，留空表示长期有效"))
		return
	}
	records := []Record{}
	for _, id := range body.IDs {
		record, e := a.store.Get(r.Context(), "link", id)
		if e != nil {
			fail(w, e)
			return
		}
		var v Link
		decode(record.Data, &v)
		if body.Action == "expiry" {
			v.ExpiresAt = body.Value
		} else {
			v.PlatformID = body.Value
		}
		record, _ = recordOf(v)
		records = append(records, record)
	}
	if e := a.store.Put(r.Context(), records, nil); e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) exportLinks(w http.ResponseWriter, r *http.Request) {
	where, args := linkFilter(r)
	platforms, e := a.platformNames(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	rows, e := a.store.db.QueryContext(r.Context(), "SELECT "+columns+" FROM records WHERE "+where+" ORDER BY account_id,id", args...)
	if e != nil {
		fail(w, e)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="mail-lite-links.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	base := a.baseURL(r)
	accounts := map[string]Account{}
	for rows.Next() {
		record, e := a.store.scan(rows)
		if e != nil {
			return
		}
		var v Link
		if decode(record.Data, &v) != nil {
			return
		}
		if _, ok := accounts[v.AccountID]; !ok {
			account, err := a.store.Account(r.Context(), v.AccountID)
			if err != nil {
				return
			}
			accounts[v.AccountID] = account
		}
		v.AccountCode = accounts[v.AccountID].PublicCode
		pageURL, _ := publicLinkURLs(base, v, platforms[v.PlatformID])
		fmt.Fprintf(w, "%s----%s\n", v.Recipient, pageURL)
	}
}
func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, a.store.Settings(r.Context()))
}
func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	var v Settings
	if !readJSON(w, r, &v) {
		return
	}
	if v.ScanLimit < 20 || v.ScanLimit > 1000 || v.RefreshDays < 1 || v.RefreshDays > 365 {
		fail(w, errors.New("扫描数量应为 20–1000，刷新间隔应为 1–365 天"))
		return
	}
	if _, e := time.LoadLocation(v.TimeZone); e != nil {
		fail(w, errors.New("无效的时区，例如 Asia/Shanghai"))
		return
	}
	if _, e := cron.ParseStandard(v.RefreshCron); e != nil {
		fail(w, errors.New("Cron 需要有效的五段表达式"))
		return
	}
	if v.PublicBaseURL != "" {
		u, e := url.Parse(v.PublicBaseURL)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			fail(w, errors.New("公开地址格式不正确"))
			return
		}
	}
	if _, e := a.store.Save(r.Context(), v, nil); e != nil {
		fail(w, e)
		return
	}
	a.cache.Invalidate("")
	a.configureSchedule()
	respond(w, 200, v)
}
func (a *App) listJobs(w http.ResponseWriter, r *http.Request) {
	rs, e := a.store.AuxList("job")
	if e != nil {
		fail(w, e)
		return
	}
	if len(rs) > 30 {
		rs = rs[:30]
	}
	respond(w, 200, map[string]any{"items": rs})
}
func (a *App) nodes(w http.ResponseWriter, r *http.Request) {
	rs, e := a.store.AuxList("node")
	if e != nil {
		fail(w, e)
		return
	}
	out := []Node{}
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()
	for _, raw := range rs {
		var v Node
		decode(raw, &v)
		v.Token = ""
		for _, s := range a.snapshots {
			if s.NodeID == v.ID && !v.Disabled && !v.Revoked {
				v.Syncing = true
				v.SnapshotDone = s.Sent
				v.SnapshotTotal = s.Total
				v.LastSeen = s.Updated.UTC().Format(time.RFC3339)
			}
		}
		out = append(out, v)
	}
	respond(w, 200, map[string]any{"items": out, "cursor": a.store.Cursor()})
}
func (a *App) createNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" || len([]rune(body.Name)) > 80 {
		fail(w, errors.New("填写 1–80 个字符的节点名称"))
		return
	}
	v := Node{ID: randomID(), Name: strings.TrimSpace(body.Name), Token: randomToken()}
	if e := a.store.AuxPut("node", v.ID, v); e != nil {
		fail(w, e)
		return
	}
	respond(w, 201, v)
}
func (a *App) revokeNode(w http.ResponseWriter, r *http.Request) {
	a.mutations.Lock()
	defer a.mutations.Unlock()
	var v Node
	if e := a.store.AuxGet("node", r.PathValue("id"), &v); e != nil {
		fail(w, e)
		return
	}
	v.Revoked = true
	v.Token = ""
	if e := a.store.AuxPut("node", v.ID, v); e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) setNodeStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Disabled == nil {
		respond(w, 400, map[string]string{"error": "请指定节点是否停用"})
		return
	}
	a.mutations.Lock()
	defer a.mutations.Unlock()
	var v Node
	if e := a.store.AuxGet("node", r.PathValue("id"), &v); e != nil {
		fail(w, e)
		return
	}
	if v.Revoked {
		respond(w, 409, map[string]string{"error": "节点凭据已永久撤销，请创建新的接入凭据"})
		return
	}
	if v.Disabled != *body.Disabled {
		v.Disabled = *body.Disabled
		v.Error = ""
		if !v.Disabled {
			// Wait for a fresh heartbeat before presenting the node as online.
			v.LastSeen = ""
		}
		if e := a.store.AuxPut("node", v.ID, v); e != nil {
			fail(w, e)
			return
		}
	}
	v.Token = ""
	respond(w, 200, v)
}

func (a *App) deleteNodeRecord(w http.ResponseWriter, r *http.Request) {
	a.mutations.Lock()
	defer a.mutations.Unlock()
	var v Node
	if e := a.store.AuxGet("node", r.PathValue("id"), &v); e != nil {
		fail(w, e)
		return
	}
	if !v.Revoked {
		respond(w, 409, map[string]string{"error": "请先撤销节点凭据，再删除记录"})
		return
	}
	if e := a.store.AuxDelete("node", v.ID); e != nil {
		fail(w, e)
		return
	}
	a.snapshotMu.Lock()
	for id, s := range a.snapshots {
		if s.NodeID == v.ID {
			osRemoveSnapshot(s)
			delete(a.snapshots, id)
		}
	}
	a.snapshotMu.Unlock()
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) maintenance() {
	if err := a.cleanMX(context.Background()); err != nil {
		slog.Error("MX cleanup", "error", err)
	}
	a.store.db.Exec("DELETE FROM events WHERE created_at < ?", time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339Nano))
	a.snapshotMu.Lock()
	for id, s := range a.snapshots {
		if time.Since(s.Created) > 15*time.Minute {
			osRemoveSnapshot(s)
			delete(a.snapshots, id)
		}
	}
	a.snapshotMu.Unlock()
	rows, e := a.store.db.Query("SELECT key,data FROM aux WHERE bucket='session'")
	if e == nil {
		expired := []string{}
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				b, err := unseal(a.key, v)
				var s session
				if err != nil || decode(b, &s) != nil || time.Now().After(s.Expires) {
					expired = append(expired, k)
				}
			}
		}
		rows.Close()
		for _, k := range expired {
			a.store.AuxDelete("session", k)
		}
	}
}
