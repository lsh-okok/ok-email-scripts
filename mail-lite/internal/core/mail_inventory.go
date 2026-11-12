package core

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// mailInventoryID parses an inventory identifier from a path. The inventory
// endpoints use integer ids, while the rest of the admin API uses opaque ids.
func mailInventoryID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("邮箱编号无效")
	}
	return id, nil
}

func mailFolderName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return "", false
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 50 {
		fail(w, errors.New("文件夹名称需要 1–50 个字符"))
		return "", false
	}
	return in.Name, true
}

func mailCheckFolder(ctx context.Context, tx *sql.Tx, id int64) error {
	if id == 0 {
		return nil
	}
	var found int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM mail_folders WHERE id=?", id).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (a *App) mailFolders(w http.ResponseWriter, r *http.Request) {
	tx, err := a.store.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	// Aggregate each mailbox kind separately so multiple children never inflate
	// the master count. Children inherit their parent's group, as in mailFilter.
	rows, err := tx.QueryContext(r.Context(), `WITH master_counts AS (
		SELECT folder_id,COUNT(*) masters FROM mailboxes WHERE kind='master' GROUP BY folder_id
	), child_counts AS (
		SELECT m.folder_id,COUNT(*) children FROM mailboxes c
		LEFT JOIN mailboxes m ON m.id=c.parent_id WHERE c.kind='child' GROUP BY m.folder_id
	)
	SELECT f.id,f.name,COALESCE(m.masters,0),COALESCE(c.children,0)
	FROM mail_folders f LEFT JOIN master_counts m ON m.folder_id=f.id
	LEFT JOIN child_counts c ON c.folder_id=f.id ORDER BY f.name COLLATE NOCASE`)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	items := []MailFolder{}
	for rows.Next() {
		var item MailFolder
		if err := rows.Scan(&item.ID, &item.Name, &item.Masters, &item.Children); err != nil {
			fail(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	if err := rows.Close(); err != nil {
		fail(w, err)
		return
	}
	var unclassified, unclassifiedChildren, totalMasters, totalChildren int
	if err := tx.QueryRowContext(r.Context(), `SELECT
		COALESCE(SUM(b.kind='master' AND b.folder_id IS NULL),0),
		COALESCE(SUM(b.kind='child' AND m.folder_id IS NULL),0),
		COALESCE(SUM(b.kind='master'),0),COALESCE(SUM(b.kind='child'),0)
		FROM mailboxes b LEFT JOIN mailboxes m ON m.id=b.parent_id`).Scan(&unclassified, &unclassifiedChildren, &totalMasters, &totalChildren); err != nil {
		fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{
		"items": items, "unclassified": unclassified, "unclassified_children": unclassifiedChildren,
		"total_masters": totalMasters, "total_children": totalChildren,
	})
}

func (a *App) createMailFolder(w http.ResponseWriter, r *http.Request) {
	name, ok := mailFolderName(w, r)
	if !ok {
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
	result, err := tx.ExecContext(r.Context(), "INSERT INTO mail_folders(name,created_at) VALUES(?,?)", name, stampNow())
	if err != nil {
		fail(w, err)
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusCreated, MailFolder{ID: id, Name: name})
}

func (a *App) updateMailFolder(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
		return
	}
	name, ok := mailFolderName(w, r)
	if !ok {
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
	result, err := tx.ExecContext(r.Context(), "UPDATE mail_folders SET name=? WHERE id=?", name, id)
	if err != nil {
		fail(w, err)
		return
	}
	changed, err := result.RowsAffected()
	if err != nil {
		fail(w, err)
		return
	}
	if changed == 0 {
		fail(w, ErrNotFound)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) deleteMailFolder(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
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
	var name string
	if err := tx.QueryRowContext(r.Context(), "SELECT name FROM mail_folders WHERE id=?", id).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	var moved int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM mailboxes WHERE folder_id=?", id).Scan(&moved); err != nil {
		fail(w, err)
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE mailboxes SET folder_id=NULL,updated_at=? WHERE folder_id=?", stampNow(), id); err != nil {
		fail(w, err)
		return
	}
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM mail_folders WHERE id=?", id); err != nil {
		fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"ok": true, "moved": moved, "name": name})
}

func mailInventoryPaging(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		page = 1000000
	}
	if limit < 1 || limit > 750 {
		limit = 20
	}
	return page, limit
}

func (a *App) mailMasters(w http.ResponseWriter, r *http.Request) {
	where, args, err := mailFilter(r.URL.Query(), "master")
	if err != nil {
		fail(w, err)
		return
	}
	page, limit := mailInventoryPaging(r)
	var total int
	if err := a.store.db.QueryRowContext(r.Context(), "SELECT COUNT(*)"+masterFrom+"WHERE "+where, args...).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	queryArgs := append(append([]any{}, args...), limit, (page-1)*limit)
	rows, err := a.store.db.QueryContext(r.Context(), "SELECT "+masterColumns+masterFrom+"WHERE "+where+" ORDER BY m.id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	items := []MailMaster{}
	for rows.Next() {
		item, err := scanMaster(rows)
		if err != nil {
			fail(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "limit": limit})
}

func (a *App) mailChildren(w http.ResponseWriter, r *http.Request) {
	where, args, err := mailFilter(r.URL.Query(), "child")
	if err != nil {
		fail(w, err)
		return
	}
	page, limit := mailInventoryPaging(r)
	var total int
	if err := a.store.db.QueryRowContext(r.Context(), "SELECT COUNT(*)"+childFrom+"WHERE "+where, args...).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	queryArgs := append(append([]any{}, args...), limit, (page-1)*limit)
	rows, err := a.store.db.QueryContext(r.Context(), "SELECT "+childColumns+childFrom+"WHERE "+where+" ORDER BY c.parent_id,c.position,c.id LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	items := []MailChild{}
	for rows.Next() {
		item, err := scanChild(rows)
		if err != nil {
			fail(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	rows.Close()
	for i := range items {
		if err := a.enrichMailChild(r.Context(), a.store.db, &items[i], a.baseURL(r)); err != nil {
			fail(w, err)
			return
		}
	}
	respond(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "limit": limit})
}

type mailMasterInput struct {
	Raw      string `json:"raw"`
	Note     string `json:"note"`
	FolderID int64  `json:"folder_id"`
}

func (in *mailMasterInput) validate() (string, error) {
	in.Raw = strings.TrimSpace(in.Raw)
	in.Note = strings.TrimSpace(in.Note)
	parts := strings.SplitN(in.Raw, "----", 2)
	email := address(parts[0])
	if email == "" || len(in.Raw) > 65536 || strings.ContainsAny(in.Raw, "\r\n") {
		return "", errors.New("原始资料需为单行，首字段为有效主邮箱地址")
	}
	if len([]rune(in.Note)) > 1000 || in.FolderID < 0 {
		return "", errors.New("备注最多 1000 字，文件夹编号不能为负")
	}
	return email, nil
}

func (a *App) mailMasterDetail(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
		return
	}
	item, err := scanMaster(a.store.db.QueryRowContext(r.Context(), "SELECT "+masterColumns+masterFrom+"WHERE m.id=? AND m.kind='master'", id))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, ErrNotFound)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	var raw string
	if err := a.store.db.QueryRowContext(r.Context(), "SELECT raw FROM mailboxes WHERE id=? AND kind='master'", id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	plain, err := a.mailMasterRaw(raw)
	if err != nil {
		fail(w, errors.New("主邮箱资料无法解密"))
		return
	}
	respond(w, http.StatusOK, map[string]any{"master": item, "raw": plain})
}

func (a *App) createMailMaster(w http.ResponseWriter, r *http.Request) {
	var in mailMasterInput
	if !readJSON(w, r, &in) {
		return
	}
	email, err := in.validate()
	if err != nil {
		fail(w, err)
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
	if err := mailCheckFolder(r.Context(), tx, in.FolderID); err != nil {
		fail(w, err)
		return
	}
	encrypted, err := seal(a.key, []byte(in.Raw))
	if err != nil {
		fail(w, err)
		return
	}
	now := stampNow()
	result, err := tx.ExecContext(r.Context(), "INSERT INTO mailboxes(email,kind,folder_id,raw,raw_complete,note,created_at,updated_at) VALUES(?,'master',?,?,?,?,?,?)", email, nullableID(in.FolderID), encrypted, completeRaw(in.Raw), in.Note, now, now)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := result.LastInsertId()
	if err == nil {
		err = mailHistory(tx, id, "admin", "mail.master.created", "")
	}
	if err != nil {
		fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *App) updateMailMaster(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in mailMasterInput
	if !readJSON(w, r, &in) {
		return
	}
	email, err := in.validate()
	if err != nil {
		fail(w, err)
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
	var oldEmail string
	if err := tx.QueryRowContext(r.Context(), "SELECT email FROM mailboxes WHERE id=? AND kind='master'", id).Scan(&oldEmail); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	if !strings.EqualFold(oldEmail, email) {
		fail(w, errors.New("原始资料首字段必须保持为当前主邮箱地址"))
		return
	}
	if err := mailCheckFolder(r.Context(), tx, in.FolderID); err != nil {
		fail(w, err)
		return
	}
	encrypted, err := seal(a.key, []byte(in.Raw))
	if err != nil {
		fail(w, err)
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE mailboxes SET raw=?,raw_complete=?,note=?,folder_id=?,updated_at=? WHERE id=? AND kind='master'", encrypted, completeRaw(in.Raw), in.Note, nullableID(in.FolderID), stampNow(), id); err != nil {
		fail(w, err)
		return
	}
	if err := mailHistory(tx, id, "admin", "mail.master.updated", "更新资料、备注或文件夹"); err != nil {
		fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

type mailChildInput struct {
	Email    string `json:"email"`
	ParentID int64  `json:"parent_id"`
	Link     string `json:"link"`
	Note     string `json:"note"`
}

func (a *App) createMailChild(w http.ResponseWriter, r *http.Request) {
	var in mailChildInput
	if !readJSON(w, r, &in) {
		return
	}
	in.Email = address(in.Email)
	in.Link = strings.TrimSpace(in.Link)
	in.Note = strings.TrimSpace(in.Note)
	if in.Email == "" || !validLink(in.Link) || in.ParentID < 1 || len([]rune(in.Note)) > 1000 {
		fail(w, errors.New("请填写有效邮箱、主邮箱编号及 HTTP(S) 接码链接，备注最多 1000 字"))
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
	var status string
	if err := tx.QueryRowContext(r.Context(), "SELECT status FROM mailboxes WHERE id=? AND kind='master'", in.ParentID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	var position int
	if err := tx.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(position),0)+1 FROM mailboxes WHERE parent_id=?", in.ParentID).Scan(&position); err != nil {
		fail(w, err)
		return
	}
	now := stampNow()
	result, err := tx.ExecContext(r.Context(), "INSERT INTO mailboxes(email,kind,parent_id,link,note,status,position,created_at,updated_at) VALUES(?,'child',?,?,?,?,?,?,?)", in.Email, in.ParentID, in.Link, in.Note, status, position, now, now)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := result.LastInsertId()
	if err == nil {
		err = mailHistory(tx, id, "admin", "mail.child.created", "")
	}
	if err != nil {
		fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *App) updateMailChild(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in struct {
		Note     *string `json:"note"`
		Link     *string `json:"link"`
		LinkMode *string `json:"link_mode"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Note == nil && in.Link == nil && in.LinkMode == nil {
		fail(w, errors.New("请填写备注或接码链接"))
		return
	}
	if in.Note != nil {
		*in.Note = strings.TrimSpace(*in.Note)
	}
	if in.Note != nil && len([]rune(*in.Note)) > 1000 {
		fail(w, errors.New("备注最多 1000 字"))
		return
	}
	if in.Link != nil {
		*in.Link = strings.TrimSpace(*in.Link)
		if !validLink(*in.Link) {
			fail(w, errors.New("请填写有效的 HTTP(S) 接码链接"))
			return
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
	var status, note, link, mode string
	if err := tx.QueryRowContext(r.Context(), "SELECT status,note,link,link_mode FROM mailboxes WHERE id=? AND kind='child'", id).Scan(&status, &note, &link, &mode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	oldMode := mode
	if in.Link != nil {
		mode = "custom"
	}
	if in.LinkMode != nil {
		mode = *in.LinkMode
	}
	if mode != "custom" && mode != "source" {
		fail(w, errors.New("链接模式无效"))
		return
	}
	linkChanged := (in.Link != nil && *in.Link != link) || mode != oldMode
	if linkChanged && status != "available" {
		fail(w, ErrConflict)
		return
	}
	if in.Note != nil {
		note = *in.Note
	}
	if in.Link != nil {
		link = *in.Link
	}
	if mode == "custom" && mode != oldMode && !validLink(link) {
		mailError(w, badMail("切换自定义模式时请填写有效的接码链接"))
		return
	}
	if mode == "source" && linkChanged {
		source, e := a.sourceLink(r.Context(), tx, id, a.baseURL(r), true)
		if e != nil {
			mailError(w, e)
			return
		}
		if !source.LinkAvailable {
			mailError(w, badMail(source.LinkReason))
			return
		}
		link = ""
	}
	if linkChanged {
		if err := recordMailActivity(r.Context(), tx, MailActivity{Action: "mail.link.updated", Scope: "child", SelectionCount: 1, ChildCount: 1}); err != nil {
			fail(w, err)
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE mailboxes SET note=?,link=?,link_mode=?,link_revision=link_revision+?,updated_at=? WHERE id=? AND kind='child'", note, link, mode, linkChanged, stampNow(), id); err != nil {
		fail(w, err)
		return
	}
	reason := "更新备注"
	if linkChanged {
		reason = "更新接码链接"
		if in.Note != nil {
			reason += "及备注"
		}
	}
	if err := mailHistory(tx, id, "admin", "mail.child.updated", reason); err != nil {
		fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) childMailHistory(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var kind string
	if err := a.store.db.QueryRowContext(r.Context(), "SELECT kind FROM mailboxes WHERE id=? AND kind='child'", id).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	rows, err := a.store.db.QueryContext(r.Context(), "SELECT id,actor,action,reason,created_at FROM mail_history WHERE mailbox_id=? ORDER BY id DESC LIMIT 100", id)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var historyID int64
		var actor, action, reason, createdAt string
		if err := rows.Scan(&historyID, &actor, &action, &reason, &createdAt); err != nil {
			fail(w, err)
			return
		}
		items = append(items, map[string]any{"id": historyID, "actor": actor, "action": action, "reason": reason, "created_at": createdAt})
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, items)
}

func uniqueMailInventoryIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return nil, errors.New("请选择 1–500 条记录")
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, errors.New("记录编号无效")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func mailMarks(ids []int64) (string, []any) {
	pl := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		pl[i], args[i] = "?", id
	}
	return strings.Join(pl, ","), args
}

func (a *App) mailFlags(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []int64 `json:"ids"`
		Flag   string  `json:"flag"`
		Value  bool    `json:"value"`
		Reason string  `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ids, err := uniqueMailInventoryIDs(in.IDs)
	if err != nil {
		fail(w, err)
		return
	}
	column := map[string]string{"abnormal": "abnormal", "disabled": "disabled", "after_sale": "after_sale"}[in.Flag]
	in.Reason = strings.TrimSpace(in.Reason)
	if column == "" || in.Reason == "" || len([]rune(in.Reason)) > 500 {
		fail(w, errors.New("请选择标识并填写 1–500 字操作原因"))
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
	pl, args := mailMarks(ids)
	checkArgs := append([]any{"child"}, args...)
	var count int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM mailboxes WHERE kind=? AND id IN ("+pl+")", checkArgs...).Scan(&count); err != nil {
		fail(w, err)
		return
	}
	if count != len(ids) {
		fail(w, ErrConflict)
		return
	}
	updateArgs := append([]any{in.Value, stampNow()}, args...)
	if _, err := tx.ExecContext(r.Context(), "UPDATE mailboxes SET "+column+"=?,updated_at=? WHERE id IN ("+pl+")", updateArgs...); err != nil {
		fail(w, err)
		return
	}
	for _, id := range ids {
		if err := mailHistory(tx, id, "admin", "mail.flags.updated", in.Reason); err != nil {
			fail(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"updated": len(ids)})
}
