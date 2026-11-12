package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type cardRow struct {
	AccountID     string `json:"account_id"`
	Email         string `json:"email"`
	Provider      string `json:"provider"`
	AccountStatus string `json:"account_status"`
	LinkCount     int    `json:"link_count"`
	Code          string `json:"code"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
	RedeemedAt    string `json:"redeemed_at"`
}

type redemptionLink struct {
	Recipient    string `json:"recipient"`
	URL          string `json:"url"`
	PlatformName string `json:"platform_name"`
	ExpiresAt    string `json:"expires_at"`
}

type redemptionResult struct {
	Code         string           `json:"code"`
	Success      bool             `json:"success"`
	Reused       bool             `json:"reused,omitempty"`
	AccountEmail string           `json:"account_email,omitempty"`
	Links        []redemptionLink `json:"links,omitempty"`
	Error        string           `json:"error,omitempty"`
}

const cardStatusSQL = `CASE WHEN c.account_id IS NULL THEN 'unissued' WHEN c.enabled=0 THEN 'disabled' WHEN c.redeemed_at<>'' THEN 'redeemed' ELSE 'unused' END`
const cardRowSQL = `SELECT a.id,a.email,a.provider,a.status,
 (SELECT count(*) FROM records l WHERE l.kind='link' AND l.account_id=a.id),
 COALESCE(c.code_encrypted,''),` + cardStatusSQL + `,COALESCE(c.created_at,''),COALESCE(c.redeemed_at,'')
 FROM records a LEFT JOIN redemption_cards c ON c.account_id=a.id`

func (a *App) registerCards(m *http.ServeMux) {
	primary := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if a.cfg.Role != "primary" {
				respond(w, http.StatusForbidden, map[string]string{"error": "请在主节点兑换卡密"})
				return
			}
			next(w, r)
		}
	}
	m.HandleFunc("GET /api/admin/cards", primary(a.admin(a.listCards)))
	m.HandleFunc("POST /api/admin/cards", primary(a.admin(a.createCards)))
	m.HandleFunc("PUT /api/admin/cards/{id}", primary(a.admin(a.setCardEnabled)))
	m.HandleFunc("POST /api/redeem", primary(a.redeemCards))
}

func (a *App) scanCard(row scanner) (cardRow, error) {
	var v cardRow
	var encrypted string
	err := row.Scan(&v.AccountID, &v.Email, &v.Provider, &v.AccountStatus, &v.LinkCount, &encrypted, &v.Status, &v.CreatedAt, &v.RedeemedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil || encrypted == "" {
		return v, err
	}
	plain, err := unseal(a.key, encrypted)
	v.Code = string(plain)
	return v, err
}

func (a *App) listCards(w http.ResponseWriter, r *http.Request) {
	page, size := pagination(r)
	where, args := "a.kind='account' AND a.provider<>'mx'", []any{}
	if search := strings.TrimSpace(r.URL.Query().Get("q")); search != "" {
		where += " AND (a.email LIKE ? OR c.code_hash=?)"
		args = append(args, "%"+search+"%", digest(strings.TrimSpace(search)))
	}
	if status := r.URL.Query().Get("status"); status != "" {
		if status != "unissued" && status != "unused" && status != "redeemed" && status != "disabled" {
			fail(w, errors.New("卡密状态无效"))
			return
		}
		where += " AND (" + cardStatusSQL + ")=?"
		args = append(args, status)
	}
	tx, err := a.store.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRowContext(r.Context(), "SELECT count(*) FROM records a LEFT JOIN redemption_cards c ON c.account_id=a.id WHERE "+where, args...).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	summary := map[string]int{"unissued": 0, "unused": 0, "redeemed": 0, "disabled": 0}
	summaryRows, err := tx.QueryContext(r.Context(), `SELECT `+cardStatusSQL+`,count(*) FROM records a LEFT JOIN redemption_cards c ON c.account_id=a.id WHERE a.kind='account' AND a.provider<>'mx' GROUP BY 1`)
	if err != nil {
		fail(w, err)
		return
	}
	for summaryRows.Next() {
		var status string
		var count int
		if err = summaryRows.Scan(&status, &count); err != nil {
			break
		}
		summary[status] = count
	}
	if err == nil {
		err = summaryRows.Err()
	}
	summaryRows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := tx.QueryContext(r.Context(), cardRowSQL+" WHERE "+where+" ORDER BY a.email COLLATE NOCASE,a.id LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		fail(w, err)
		return
	}
	items := []cardRow{}
	for rows.Next() {
		var item cardRow
		item, err = a.scanCard(rows)
		if err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "page_size": size, "summary": summary})
}

func (a *App) createCards(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountIDs []string `json:"account_ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.AccountIDs) < 1 || len(body.AccountIDs) > 500 {
		fail(w, errors.New("每次请选择 1–500 个主邮箱"))
		return
	}
	ids, seen := []string{}, map[string]bool{}
	for _, id := range body.AccountIDs {
		if id == "" || len(id) > 128 {
			fail(w, errors.New("主邮箱 ID 无效"))
			return
		}
		if !seen[id] {
			ids, seen[id] = append(ids, id), true
		}
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	items := []cardRow{}
	for _, id := range ids {
		item, err := a.scanCard(tx.QueryRowContext(r.Context(), cardRowSQL+" WHERE a.kind='account' AND a.id=?", id))
		if err != nil {
			fail(w, err)
			return
		}
		if item.AccountStatus != "active" || item.Provider == "mx" {
			fail(w, errors.New("只能为已启用的 Outlook 或 iCloud 主邮箱生成卡密"))
			return
		}
		if item.Status == "unissued" {
			item.Code, item.Status, item.CreatedAt = "ML-"+randomToken(), "unused", stampNow()
			encrypted, err := seal(a.key, []byte(item.Code))
			if err != nil {
				fail(w, err)
				return
			}
			if _, err = tx.ExecContext(r.Context(), "INSERT INTO redemption_cards(account_id,code_hash,code_encrypted,created_at) VALUES(?,?,?,?)", id, digest(item.Code), encrypted, item.CreatedAt); err != nil {
				fail(w, err)
				return
			}
		}
		items = append(items, item)
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) setCardEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		fail(w, errors.New("请指定卡密启用状态"))
		return
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), "UPDATE redemption_cards SET enabled=? WHERE account_id=? AND EXISTS(SELECT 1 FROM records WHERE kind='account' AND id=?)", *body.Enabled, r.PathValue("id"), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	count, err := res.RowsAffected()
	if err == nil && count == 0 {
		err = ErrNotFound
	}
	if err != nil {
		fail(w, err)
		return
	}
	item, err := a.scanCard(tx.QueryRowContext(r.Context(), cardRowSQL+" WHERE a.kind='account' AND a.id=?", r.PathValue("id")))
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, item)
}

func (a *App) allowRedemption(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	key, now := "redeem:"+host, time.Now()
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if len(a.attempts) >= 10000 {
		for k, v := range a.attempts {
			if now.After(v.until) {
				delete(a.attempts, k)
			}
		}
	}
	attempt, exists := a.attempts[key]
	if !exists && len(a.attempts) >= 10000 {
		w.Header().Set("Retry-After", "60")
		respond(w, http.StatusTooManyRequests, map[string]string{"error": "兑换请求繁忙，请稍后重试"})
		return false
	}
	if now.After(attempt.until) {
		attempt = loginAttempt{until: now.Add(time.Minute)}
	}
	if attempt.count >= 30 {
		w.Header().Set("Retry-After", "60")
		respond(w, http.StatusTooManyRequests, map[string]string{"error": "兑换请求过多，请一分钟后重试"})
		return false
	}
	attempt.count++
	a.attempts[key] = attempt
	return true
}

func (a *App) redeemCards(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		respond(w, http.StatusForbidden, map[string]string{"error": "请求来源不匹配"})
		return
	}
	if !a.allowRedemption(w, r) {
		return
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		respond(w, http.StatusUnsupportedMediaType, map[string]string{"error": "请使用 JSON 提交卡密"})
		return
	}
	var body struct {
		Codes []string `json:"codes"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&body); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "卡密请求格式错误或超过 64 KiB"})
		return
	}
	if err := d.Decode(&struct{}{}); err != io.EOF || len(body.Codes) < 1 || len(body.Codes) > 100 {
		respond(w, http.StatusBadRequest, map[string]string{"error": "每次请输入 1–100 个卡密"})
		return
	}
	codes, seen := []string{}, map[string]bool{}
	for _, raw := range body.Codes {
		code := strings.TrimSpace(raw)
		if code == "" || len(code) > 128 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "卡密不能为空且最多 128 个字符"})
			return
		}
		if !seen[code] {
			codes, seen[code] = append(codes, code), true
		}
	}
	base := a.baseURL(r)
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "兑换服务暂时不可用，请稍后重试"})
		return
	}
	defer tx.Rollback()
	items := []redemptionResult{}
	for _, code := range codes {
		item, err := a.redeemCard(r.Context(), tx, code, base)
		if err != nil {
			respond(w, http.StatusInternalServerError, map[string]string{"error": "兑换服务暂时不可用，请稍后重试"})
			return
		}
		items = append(items, item)
	}
	if err = tx.Commit(); err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "兑换服务暂时不可用，请稍后重试"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) redeemCard(ctx context.Context, tx *sql.Tx, code, base string) (redemptionResult, error) {
	result := redemptionResult{Code: code}
	var accountID, redeemedAt string
	var enabled bool
	err := tx.QueryRowContext(ctx, "SELECT account_id,enabled,redeemed_at FROM redemption_cards WHERE code_hash=?", digest(code)).Scan(&accountID, &enabled, &redeemedAt)
	if errors.Is(err, sql.ErrNoRows) {
		result.Error = "卡密无效"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !enabled {
		result.Error = "卡密已停用"
		return result, nil
	}
	accountRecord, err := a.publicRecordWith(ctx, tx, "kind='account' AND id=?", accountID)
	if errors.Is(err, ErrNotFound) {
		result.Error = "主邮箱已删除，请联系管理员"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if accountRecord.Status != "active" || accountRecord.Provider == "mx" {
		result.Error = "主邮箱不可用，请联系管理员"
		return result, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+columns+" FROM records WHERE kind='link' AND account_id=? ORDER BY recipient COLLATE NOCASE,platform_id,id", accountID)
	if err != nil {
		return result, err
	}
	links := []Link{}
	for rows.Next() {
		record, scanErr := a.store.scan(rows)
		if scanErr != nil {
			err = scanErr
			break
		}
		var link Link
		if err = decode(record.Data, &link); err != nil {
			break
		}
		links = append(links, link)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	now := time.Now()
	for _, link := range links {
		if link.ExpiresAt != "" {
			expiry, err := time.Parse(time.RFC3339, link.ExpiresAt)
			if err != nil || !now.Before(expiry) {
				continue
			}
		}
		platformName := "全部平台"
		if link.PlatformID != "" {
			platform, err := a.publicRecordWith(ctx, tx, "kind='platform' AND id=?", link.PlatformID)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return result, err
			}
			platformName = platform.Email
		}
		link.AccountCode = accountRecord.PublicCode
		relativeURL, _ := publicLinkURLs("", link, platformName)
		linkURL := base + relativeURL
		// A short recipient path may collide across domains or resolve to an
		// unrestricted policy. The original token still identifies this exact
		// child and policy, so use it when the readable path is ambiguous.
		parsed, err := url.Parse(relativeURL)
		if err != nil {
			return result, err
		}
		segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
		req := &http.Request{URL: parsed}
		if len(segments) != 4 {
			continue
		}
		for i := range segments {
			segments[i], err = url.PathUnescape(segments[i])
			if err != nil {
				return result, err
			}
		}
		req.SetPathValue("platform", segments[1])
		req.SetPathValue("account", segments[2])
		req.SetPathValue("recipient", segments[3])
		effective, resolveErr := a.requestLinkWith(ctx, tx, req)
		if resolveErr != nil && !errors.Is(resolveErr, ErrNotFound) {
			return result, resolveErr
		}
		if resolveErr != nil || effective.ID != link.ID {
			if link.Token == "" || digest(link.Token) != link.TokenHash {
				continue
			}
			linkURL = base + "/m/" + url.PathEscape(link.Token)
		}
		result.Links = append(result.Links, redemptionLink{Recipient: link.Recipient, URL: linkURL, PlatformName: platformName, ExpiresAt: link.ExpiresAt})
	}
	if len(result.Links) == 0 {
		result.Error = "主邮箱暂无可用的子邮箱链接，请联系管理员"
		return result, nil
	}
	if redeemedAt == "" {
		if _, err = tx.ExecContext(ctx, "UPDATE redemption_cards SET redeemed_at=? WHERE account_id=? AND redeemed_at=''", stampNow(), accountID); err != nil {
			return result, err
		}
	} else {
		result.Reused = true
	}
	result.Success, result.AccountEmail = true, accountRecord.Email
	return result, nil
}
