package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type accountView struct {
	Account
	LinkCount int `json:"link_count"`
}

func accountFilter(q url.Values) (string, []any) {
	where, args := "kind='account'", []any{}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		where += " AND email LIKE ?"
		args = append(args, "%"+search+"%")
	}
	for _, field := range []struct{ query, column string }{{"group_id", "parent_id"}, {"provider", "provider"}, {"status", "status"}} {
		if value := q.Get(field.query); value != "" {
			if field.query == "group_id" && value == "__ungrouped__" {
				value = ""
			}
			where += " AND " + field.column + "=?"
			args = append(args, value)
		}
	}
	return where, args
}

func (a *App) accountLinkCounts(ctx context.Context, records []Record) (map[string]int, error) {
	counts := map[string]int{}
	if len(records) == 0 {
		return counts, nil
	}
	args := make([]any, len(records))
	for i, record := range records {
		args[i] = record.ID
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	rows, err := a.store.db.QueryContext(ctx, "SELECT account_id,count(*) FROM records WHERE kind='link' AND account_id IN ("+placeholders+") GROUP BY account_id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}

type cachedCredential struct {
	Kind string `json:"kind"`
	AccessToken
}

type accountDetail struct {
	MXDomain         string             `json:"mx_domain,omitempty"`
	MXRetentionDays  *int               `json:"mx_retention_days,omitempty"`
	ID               string             `json:"id"`
	Email            string             `json:"email"`
	Provider         string             `json:"provider"`
	GroupID          string             `json:"group_id"`
	GroupName        string             `json:"group_name"`
	Status           string             `json:"status"`
	ClientID         string             `json:"client_id"`
	RefreshToken     string             `json:"refresh_token"`
	Password         string             `json:"password"`
	Proxies          []string           `json:"proxies"`
	EffectiveProxies []string           `json:"effective_proxies"`
	Note             string             `json:"note"`
	LastRefresh      string             `json:"last_refresh"`
	RefreshError     string             `json:"refresh_error"`
	LinkCount        int                `json:"link_count"`
	Version          int64              `json:"version"`
	Generation       int64              `json:"generation"`
	AccessTokens     []cachedCredential `json:"access_tokens"`
}

// Viewing credentials never calls Microsoft or rotates the stored refresh token.
func (a *App) accountCredentials(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.Account(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	tokens := []cachedCredential{}
	a.tokens.mu.Lock()
	for _, kind := range []string{"graph", "write", "imap"} {
		if token := a.tokens.values[v.ID+":"+kind]; token.Token != "" && time.Now().Before(token.ExpiresAt) {
			tokens = append(tokens, cachedCredential{kind, token})
		}
	}
	a.tokens.mu.Unlock()
	groupName := ""
	if v.GroupID != "" {
		if record, groupErr := a.store.Get(r.Context(), "group", v.GroupID); groupErr == nil {
			var group Group
			if decode(record.Data, &group) == nil {
				groupName = group.Name
			}
		} else if !errors.Is(groupErr, ErrNotFound) {
			fail(w, groupErr)
			return
		}
	}
	var linkCount int
	if err := a.store.db.QueryRowContext(r.Context(), "SELECT count(*) FROM records WHERE kind='link' AND account_id=?", v.ID).Scan(&linkCount); err != nil {
		fail(w, err)
		return
	}
	effectiveProxies, proxyErr := a.effectiveProxies(r.Context(), v)
	if v.Provider == "mx" {
		effectiveProxies = nil
		proxyErr = nil
	}
	if proxyErr != nil {
		// A malformed group should not prevent an administrator from viewing the
		// stored account data; the error remains visible in the drawer.
		effectiveProxies = []string{}
	}
	respond(w, 200, accountDetail{
		ID: v.ID, Email: v.Email, Provider: v.Provider, MXDomain: v.MXDomain, MXRetentionDays: v.MXRetentionDays, GroupID: v.GroupID, GroupName: groupName,
		Status: v.Status, ClientID: v.ClientID, RefreshToken: v.RefreshToken, Password: v.Password,
		Proxies: append([]string{}, v.Proxies...), EffectiveProxies: effectiveProxies,
		Note: v.Note, LastRefresh: v.LastRefresh, RefreshError: v.RefreshError,
		LinkCount: linkCount, Version: v.Version, Generation: v.Generation, AccessTokens: tokens,
	})
}

type accountExportRequest struct {
	Scope              string   `json:"scope"`
	IDs                []string `json:"ids"`
	Format             string   `json:"format"`
	IncludeCredentials bool     `json:"include_credentials"`
	Query              string   `json:"q"`
	Provider           string   `json:"provider"`
	GroupID            string   `json:"group_id"`
	Status             string   `json:"status"`
}

// A read transaction keeps all exported accounts at one database snapshot, even
// if background token rotation or editing happens during the export.
func (a *App) exportAccounts(w http.ResponseWriter, r *http.Request) {
	var body accountExportRequest
	if !readJSON(w, r, &body) {
		return
	}
	// The current UI exports the complete account import data in one TXT file.
	// Keep the older scope/format requests working for existing API clients.
	if body.Scope == "" {
		body.Scope = "all"
	}
	if body.Format == "" {
		body.Format = "txt"
	}
	if body.Format != "txt" && body.Format != "csv" && body.Format != "json" {
		fail(w, errors.New("请选择 TXT、CSV 或 JSON 导出格式"))
		return
	}
	where, args := accountFilter(url.Values{"q": {body.Query}, "provider": {body.Provider}, "group_id": {body.GroupID}, "status": {body.Status}})
	switch body.Scope {
	case "all":
		if len(body.IDs) != 0 {
			fail(w, errors.New("导出全部邮箱时请勿指定邮箱 ID"))
			return
		}
		where, args = "kind='account'", nil
		body.Format = "txt"
		body.IncludeCredentials = true
	case "selected":
		if len(body.IDs) == 0 || len(body.IDs) > 100 {
			fail(w, errors.New("请选择 1–100 个邮箱"))
			return
		}
		where, args = "kind='account' AND id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(body.IDs)), ",")+")", []any{}
		for _, id := range body.IDs {
			args = append(args, id)
		}
	case "filtered":
		if len(body.IDs) != 0 {
			fail(w, errors.New("导出筛选结果时请勿同时指定邮箱 ID"))
			return
		}
	default:
		fail(w, errors.New("导出范围无效"))
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(r.Context(), "SELECT "+columns+" FROM records WHERE "+where+" ORDER BY email COLLATE NOCASE,id", args...)
	if err != nil {
		fail(w, err)
		return
	}
	accounts := []Account{}
	for rows.Next() {
		record, scanErr := a.store.scan(rows)
		if scanErr != nil {
			err = scanErr
			break
		}
		var account Account
		if err = json.Unmarshal(record.Data, &account); err != nil {
			break
		}
		account.Version = record.Version
		account.HasCredential = account.RefreshToken != "" || account.Password != ""
		if !body.IncludeCredentials {
			account.RefreshToken, account.Password = "", ""
			account.Proxies = nil
		}
		accounts = append(accounts, account)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	selectedIDs := map[string]bool{}
	for _, id := range body.IDs {
		selectedIDs[id] = true
	}
	if body.Scope == "selected" && len(accounts) != len(selectedIDs) {
		fail(w, errors.New("所选邮箱已变更，请刷新列表后重试"))
		return
	}
	var output bytes.Buffer
	contentType := "text/plain; charset=utf-8"
	switch body.Format {
	case "json":
		contentType = "application/json; charset=utf-8"
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(accounts)
	case "csv":
		contentType = "text/csv; charset=utf-8"
		output.WriteString("\ufeff")
		writer := csv.NewWriter(&output)
		header := []string{"邮箱地址", "类型", "状态", "分组 ID", "Client ID", "最近刷新", "刷新错误", "备注"}
		if body.IncludeCredentials {
			header = append(header, "Refresh Token", "应用专用密码")
		}
		writer.Write(header)
		for _, account := range accounts {
			row := []string{account.Email, account.Provider, account.Status, account.GroupID, account.ClientID, account.LastRefresh, account.RefreshError, account.Note}
			if body.IncludeCredentials {
				row = append(row, account.RefreshToken, account.Password)
			}
			for i, cell := range row {
				trimmed := strings.TrimSpace(cell)
				if len(trimmed) > 0 && strings.ContainsAny(trimmed[:1], "=+-@") {
					row[i] = "'" + cell
				}
			}
			writer.Write(row)
		}
		writer.Flush()
		err = writer.Error()
	case "txt":
		for _, account := range accounts {
			line := account.Email
			if body.IncludeCredentials {
				if account.Provider == "mx" {
					line = mailAccountRaw(account)
				} else if account.Provider == "outlook" {
					line += "----" + account.Password + "----" + account.ClientID + "----" + account.RefreshToken
				} else {
					line += "----" + account.Password
				}
				if strings.ContainsAny(line, "\r\n") {
					err = errors.New("凭据含换行符，请选择 JSON 导出以完整保留数据")
					break
				}
			}
			fmt.Fprintln(&output, line)
		}
	}
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="mail-lite-accounts-`+time.Now().Format("20060102-150405")+"."+body.Format+`"`)
	w.Header().Set("X-Export-Count", strconv.Itoa(len(accounts)))
	w.Header().Set("Content-Length", strconv.Itoa(output.Len()))
	w.Write(output.Bytes())
}
