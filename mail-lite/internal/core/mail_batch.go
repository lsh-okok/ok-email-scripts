package core

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type mailDeleteImpact struct {
	Masters  int    `json:"masters"`
	Children int    `json:"children"`
	Reserved int    `json:"reserved"`
	Sold     int    `json:"sold"`
	Version  string `json:"version"`
}

// deleteImpact is kept as a compatibility name for callers that used the
// go-version helper directly. HTTP handlers use the encrypted-aware variant.
type deleteImpact = mailDeleteImpact

func requireMailInventoryIDs(tx *sql.Tx, ids []int64, kind string) error {
	if len(ids) == 0 {
		return errors.New("请选择记录")
	}
	ph, args := mailMarks(ids)
	args = append([]any{kind}, args...)
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE kind=? AND id IN ("+ph+")", args...).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return ErrConflict
	}
	return nil
}

func mailDeleteImpactFor(tx *sql.Tx, ids []int64, decrypt func(string) (string, error)) (mailDeleteImpact, error) {
	var out mailDeleteImpact
	if err := requireMailInventoryIDs(tx, ids, "master"); err != nil {
		return out, err
	}
	ph, args := mailMarks(ids)
	rows, err := tx.Query("SELECT id,email,raw,note,COALESCE(folder_id,0),COALESCE(parent_id,0),link,status,abnormal,disabled,after_sale,position,updated_at FROM mailboxes WHERE id IN ("+ph+") OR parent_id IN ("+ph+") ORDER BY id", append(append([]any{}, args...), args...)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var version bytes.Buffer
	for rows.Next() {
		var id, folder, parent int64
		var abnormal, disabled, after, pos int
		var email, raw, note, link, status, updated string
		if err = rows.Scan(&id, &email, &raw, &note, &folder, &parent, &link, &status, &abnormal, &disabled, &after, &pos, &updated); err != nil {
			return out, err
		}
		if parent == 0 {
			plain, e := decrypt(raw)
			if e != nil {
				return out, e
			}
			raw = plain
			out.Masters++
		} else {
			out.Children++
			if status == "reserved" {
				out.Reserved++
			}
			if status == "sold" {
				out.Sold++
			}
		}
		v, _ := json.Marshal([]any{id, email, raw, note, folder, parent, link, status, abnormal, disabled, after, pos, updated})
		version.Write(v)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Version = digest(version.String())
	return out, nil
}

func (a *App) mailMasterBatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs      []int64 `json:"ids"`
		Action   string  `json:"action"`
		FolderID int64   `json:"folder_id"`
		Version  string  `json:"version"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ids, err := uniqueMailInventoryIDs(in.IDs)
	if err != nil {
		fail(w, err)
		return
	}
	if in.Action != "move" && in.Action != "preview_delete" && in.Action != "previewDelete" && in.Action != "delete" {
		fail(w, errors.New("操作类型无效"))
		return
	}
	if in.Action == "previewDelete" {
		in.Action = "preview_delete"
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if err = requireMailInventoryIDs(tx, ids, "master"); err != nil {
		fail(w, err)
		return
	}
	if in.Action == "move" {
		if in.FolderID < 0 {
			fail(w, errors.New("文件夹编号无效"))
			return
		}
		if err = mailCheckFolder(r.Context(), tx, in.FolderID); err != nil {
			fail(w, err)
			return
		}
		ph, args := mailMarks(ids)
		updateArgs := append([]any{nullableID(in.FolderID), stampNow()}, args...)
		if _, err = tx.ExecContext(r.Context(), "UPDATE mailboxes SET folder_id=?,updated_at=? WHERE kind='master' AND id IN ("+ph+")", updateArgs...); err != nil {
			fail(w, err)
			return
		}
		for _, id := range ids {
			if err = mailHistory(tx, id, "admin", "mail.masters.moved", "移动文件夹"); err != nil {
				fail(w, err)
				return
			}
		}
		if err = recordMailActivity(r.Context(), tx, MailActivity{
			Action: "mail.masters.moved", Scope: "master", SelectionCount: len(ids), MasterCount: len(ids),
		}); err != nil {
			fail(w, err)
			return
		}
		if err = tx.Commit(); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]any{"ok": true, "updated": len(ids)})
		return
	}
	impact, err := mailDeleteImpactFor(tx, ids, a.mailMasterRaw)
	if err != nil {
		fail(w, err)
		return
	}
	if in.Action == "preview_delete" {
		respond(w, http.StatusOK, impact)
		return
	}
	if strings.TrimSpace(in.Version) == "" || in.Version != impact.Version {
		respond(w, http.StatusConflict, map[string]string{"error": "库存已变化，请重新预览删除范围"})
		return
	}
	if impact.Reserved > 0 || impact.Sold > 0 {
		respond(w, http.StatusConflict, map[string]string{"error": "含预留或已售子邮箱，请先取消预留单或完成售后退库"})
		return
	}
	ph, args := mailMarks(ids)
	var inspecting int
	query := `SELECT COUNT(*) FROM mail_inspection_results ir JOIN mail_inspections it ON it.id=ir.inspection_id
 WHERE it.status IN ('queued','running') AND ir.mailbox_id IN (SELECT id FROM mailboxes WHERE parent_id IN (` + ph + `))`
	if err = tx.QueryRowContext(r.Context(), query, args...).Scan(&inspecting); err != nil {
		fail(w, err)
		return
	}
	if inspecting > 0 {
		respond(w, http.StatusConflict, map[string]string{"error": "涉及运行中的检测任务，请先停止检测再删除"})
		return
	}
	if err = recordMailActivity(r.Context(), tx, MailActivity{
		Action: "mail.masters.deleted", Scope: "master", SelectionCount: len(ids),
		MasterCount: impact.Masters, ChildCount: impact.Children,
	}); err != nil {
		fail(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM mailboxes WHERE kind='master' AND id IN ("+ph+")", args...); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"ok": true, "updated": len(ids)})
}

func csvSafe(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		return "'" + value
	}
	return value
}

func (a *App) mailExport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs           []int64 `json:"ids"`
		Kind          string  `json:"kind"`
		Format        string  `json:"format"`
		IncludeMaster *bool   `json:"include_master"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ids, err := uniqueMailInventoryIDs(in.IDs)
	if err != nil {
		fail(w, err)
		return
	}
	if in.Kind != "master" && in.Kind != "child" {
		fail(w, errors.New("导出范围无效"))
		return
	}
	if in.Format != "txt" && in.Format != "emails" && in.Format != "csv" && in.Format != "json" {
		fail(w, errors.New("导出格式无效"))
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
	if err = requireMailInventoryIDs(tx, ids, in.Kind); err != nil {
		fail(w, err)
		return
	}
	ph, args := mailMarks(ids)
	where := "c.id"
	if in.Kind == "master" {
		where = "c.parent_id"
	}
	var content strings.Builder
	lines := []string{}
	if in.Kind == "master" && in.Format == "txt" && (in.IncludeMaster == nil || *in.IncludeMaster) {
		rows, e := tx.Query("SELECT raw FROM mailboxes WHERE kind='master' AND id IN ("+ph+") ORDER BY id", args...)
		if e != nil {
			fail(w, e)
			return
		}
		for rows.Next() {
			var raw string
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				fail(w, e)
				return
			}
			plain, de := a.mailMasterRaw(raw)
			if de != nil {
				rows.Close()
				fail(w, errors.New("主邮箱资料无法解密"))
				return
			}
			lines = append(lines, mailTXTLine(strings.Split(plain, "----")...))
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			fail(w, e)
			return
		}
		rows.Close()
	}
	rows, err := tx.Query("SELECT c.id,c.link_mode,c.email,COALESCE(m.email,''),c.link,c.note,c.status,c.abnormal,c.disabled,c.after_sale FROM mailboxes c LEFT JOIN mailboxes m ON m.id=c.parent_id WHERE c.kind='child' AND "+where+" IN ("+ph+") ORDER BY c.parent_id,c.position,c.id LIMIT 100001", args...)
	if err != nil {
		fail(w, err)
		return
	}
	type childExport struct {
		ID                                     int64
		Mode                                   string
		Email, ParentEmail, Link, Note, Status string
		Abnormal, Disabled, AfterSale          bool
	}
	items := []childExport{}
	for rows.Next() {
		var c childExport
		if err = rows.Scan(&c.ID, &c.Mode, &c.Email, &c.ParentEmail, &c.Link, &c.Note, &c.Status, &c.Abnormal, &c.Disabled, &c.AfterSale); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		items = append(items, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		fail(w, err)
		return
	}
	rows.Close()
	if len(items) > 100000 {
		fail(w, errors.New("单次最多导出 100000 条，请缩小范围"))
		return
	}
	for i := range items {
		if items[i].Mode == "source" {
			source, e := a.sourceLink(r.Context(), tx, items[i].ID, a.baseURL(r), true)
			if e != nil {
				fail(w, e)
				return
			}
			if !source.LinkAvailable {
				mailError(w, badMail("包含不可用的系统关联链接："+source.LinkReason))
				return
			}
			items[i].Link = source.Link
		}
	}
	ext := "txt"
	switch in.Format {
	case "txt", "emails":
		for _, c := range items {
			if in.Format == "txt" {
				lines = append(lines, mailTXTLine(c.Email, c.Link))
			} else {
				lines = append(lines, strings.TrimSpace(c.Email))
			}
		}
		content.WriteString(strings.Join(lines, "\n"))
	case "csv":
		ext = "csv"
		var b bytes.Buffer
		b.WriteString("\ufeff")
		cw := csv.NewWriter(&b)
		_ = cw.Write([]string{"email", "parent_email", "link", "status", "abnormal", "disabled", "after_sale", "note"})
		for _, c := range items {
			_ = cw.Write([]string{csvSafe(c.Email), csvSafe(c.ParentEmail), csvSafe(c.Link), c.Status, strconv.FormatBool(c.Abnormal), strconv.FormatBool(c.Disabled), strconv.FormatBool(c.AfterSale), csvSafe(c.Note)})
		}
		cw.Flush()
		if err = cw.Error(); err != nil {
			fail(w, err)
			return
		}
		content.WriteString(b.String())
	case "json":
		ext = "json"
		records := make([]map[string]any, 0, len(items))
		for _, c := range items {
			records = append(records, map[string]any{"email": c.Email, "parent_email": c.ParentEmail, "link": c.Link, "status": c.Status, "abnormal": c.Abnormal, "disabled": c.Disabled, "after_sale": c.AfterSale, "note": c.Note})
		}
		b, e := json.MarshalIndent(records, "", "  ")
		if e != nil {
			fail(w, e)
			return
		}
		content.Write(b)
	}
	masterCount := 0
	if in.Kind == "master" {
		masterCount = len(ids)
	}
	if err = recordMailActivity(r.Context(), tx, MailActivity{
		Action: "mail.exported", Scope: in.Kind, Format: in.Format, SelectionCount: len(ids),
		MasterCount: masterCount, ChildCount: len(items),
	}); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"filename": "mail-export-" + time.Now().Format("2006-01-02") + "." + ext, "content": content.String(), "count": len(items)})
}
