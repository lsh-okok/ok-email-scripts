package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Import links in bounded transactions. Existing policy and tokens are preserved
// when reimporting a recipient under the same platform policy, making interrupted
// imports safe to repeat while allowing one link per platform policy.
func (a *App) runLinksImport(ctx context.Context, j *Job, body ImportRequest) {
	for _, f := range body.Files {
		j.Total += len(strings.Split(strings.TrimSpace(f.Text), "\n"))
	}
	for _, f := range body.Files {
		accountID := body.AccountID
		known := map[string]bool{}
		linkKey := func(recipient string) string { return strings.ToLower(recipient) + "\x00" + body.PlatformID }
		pending := []Record{}
		loadKnown := func() error {
			rows, e := a.store.db.QueryContext(ctx, "SELECT recipient,platform_id FROM records WHERE kind='link' AND account_id=?", accountID)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var recipient, platformID string
				if e = rows.Scan(&recipient, &platformID); e != nil {
					return e
				}
				known[strings.ToLower(recipient)+"\x00"+platformID] = true
			}
			return rows.Err()
		}
		flush := func() {
			if len(pending) == 0 {
				return
			}
			if e := a.store.Put(ctx, pending, nil); e != nil {
				for _, record := range pending {
					if e := a.store.Put(ctx, []Record{record}, nil); e != nil {
						jobError(j, fmt.Sprintf("%s：%s 保存失败", f.Name, record.Recipient))
					}
				}
			}
			pending = nil
			a.store.AuxPut("job", j.ID, *j)
		}
		if accountID != "" {
			if e := loadKnown(); e != nil {
				j.Status = "failed"
				jobError(j, "无法读取已有链接")
				return
			}
		}
		for index, line := range strings.Split(strings.TrimSpace(strings.TrimPrefix(f.Text, "\ufeff")), "\n") {
			if ctx.Err() != nil {
				j.Status = "failed"
				jobError(j, "任务已中断，可重新导入")
				return
			}
			j.Done++
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if accountID == "" {
				email := strings.TrimSpace(strings.Split(line, "----")[0])
				record, e := a.store.Find(ctx, "kind='account' AND email=? COLLATE NOCASE", email)
				if e == nil {
					accountID = record.ID
				} else {
					v, err := parseAccountLine(line)
					if err != nil {
						jobError(j, f.Name+"：首行需要已导入的主邮箱，或包含凭据的主邮箱行")
						break
					}
					v.GroupID = body.GroupID
					if _, err = a.store.Save(ctx, v, nil); err != nil {
						jobError(j, f.Name+"：创建主邮箱失败")
						break
					}
					accountID = v.ID
				}
				if loadKnown() != nil {
					jobError(j, "无法读取已有链接")
					break
				}
				continue
			}
			for _, recipient := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == ';' }) {
				recipient = address(strings.Split(recipient, "----")[0])
				if recipient == "" {
					jobError(j, fmt.Sprintf("%s 第 %d 行：邮箱地址格式错误", f.Name, index+1))
					continue
				}
				owner, err := a.store.Account(ctx, accountID)
				if err != nil || owner.Provider == "mx" && !mxRecipient(owner, recipient) {
					jobError(j, fmt.Sprintf("%s 第 %d 行：收件地址不属于此 MX 域名", f.Name, index+1))
					continue
				}
				key := linkKey(recipient)
				if known[key] {
					continue
				}
				known[key] = true
				token := randomToken()
				v := Link{ID: randomID(), AccountID: accountID, Recipient: recipient, Token: token, TokenHash: digest(token), PlatformID: body.PlatformID, ExpiresAt: body.ExpiresAt, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
				record, _ := recordOf(v)
				pending = append(pending, record)
				if len(pending) >= 500 {
					flush()
				}
			}
			a.progress(j)
		}
		flush()
	}
}
