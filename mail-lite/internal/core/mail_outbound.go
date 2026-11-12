package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MailOutbound is the durable sales batch snapshot exposed to the admin UI.
// The JSON names intentionally use snake_case to match the rest of mail-lite.
type MailOutbound struct {
	ID            int64   `json:"id"`
	OrderNo       string  `json:"order_no"`
	Mode          string  `json:"mode"`
	Status        string  `json:"status"`
	Selection     string  `json:"selection"`
	IncludeMaster bool    `json:"include_master"`
	Customer      string  `json:"customer"`
	Note          string  `json:"note"`
	Actor         string  `json:"actor"`
	CreatedAt     string  `json:"created_at"`
	DeliveredAt   *string `json:"delivered_at"`
	Items         int     `json:"items"`
	Downloadable  bool    `json:"downloadable"`
}

type MailOutboundItem struct {
	ID          int64   `json:"id"`
	MailboxID   *int64  `json:"mailbox_id"`
	Email       string  `json:"email"`
	ParentEmail string  `json:"parent_email"`
	Link        string  `json:"link,omitempty"`
	ReturnedAt  *string `json:"returned_at"`
}

type mailOutboundEvent struct {
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
	Count     int    `json:"count"`
	CreatedAt string `json:"created_at"`
}

type mailOutboundDetail struct {
	MailOutbound
	Items   []MailOutboundItem  `json:"items"`
	History []mailOutboundEvent `json:"history"`
}

const outboundColumns = `o.id,o.order_no,o.mode,o.status,o.selection,o.include_master,o.customer,o.note,o.actor,o.created_at,o.delivered_at,
 (SELECT COUNT(*) FROM mail_outbound_items WHERE outbound_id=o.id),
 (o.status IN ('reserved','sold') AND o.download_disabled=0 AND NOT EXISTS(SELECT 1 FROM mail_outbound_items WHERE outbound_id=o.id AND mailbox_id IS NULL))`

func scanMailOutbound(row scanner) (MailOutbound, error) {
	var v MailOutbound
	err := row.Scan(&v.ID, &v.OrderNo, &v.Mode, &v.Status, &v.Selection, &v.IncludeMaster, &v.Customer, &v.Note, &v.Actor, &v.CreatedAt, &v.DeliveredAt, &v.Items, &v.Downloadable)
	return v, err
}

func (a *App) mailOutboundList(w http.ResponseWriter, r *http.Request) {
	page, limit := mailInventoryPaging(r)
	where, args := "1=1", []any{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		if len([]rune(q)) > 254 {
			fail(w, errors.New("搜索内容过长"))
			return
		}
		where += " AND (o.order_no LIKE ? ESCAPE '\\' OR o.customer LIKE ? ESCAPE '\\' OR o.note LIKE ? ESCAPE '\\')"
		args = append(args, likeMail(q), likeMail(q), likeMail(q))
	}
	if status := r.URL.Query().Get("status"); status != "" {
		if !strings.Contains("|reserved|sold|cancelled|partial_return|returned|", "|"+status+"|") {
			fail(w, errors.New("出库状态无效"))
			return
		}
		where += " AND o.status=?"
		args = append(args, status)
	}
	var total int
	if err := a.store.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM mail_outbounds o WHERE "+where, args...).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	queryArgs := append(append([]any{}, args...), limit, (page-1)*limit)
	rows, err := a.store.db.QueryContext(r.Context(), "SELECT "+outboundColumns+" FROM mail_outbounds o WHERE "+where+" ORDER BY o.id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	items := []MailOutbound{}
	for rows.Next() {
		v, e := scanMailOutbound(rows)
		if e != nil {
			fail(w, e)
			return
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "limit": limit})
}

func (a *App) mailOutboundDetail(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
		return
	}
	v, err := scanMailOutbound(a.store.db.QueryRowContext(r.Context(), "SELECT "+outboundColumns+" FROM mail_outbounds o WHERE o.id=?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	rows, err := a.store.db.QueryContext(r.Context(), "SELECT id,mailbox_id,email,parent_email,link,returned_at FROM mail_outbound_items WHERE outbound_id=? ORDER BY id", id)
	if err != nil {
		fail(w, err)
		return
	}
	detail := mailOutboundDetail{MailOutbound: v, Items: []MailOutboundItem{}, History: []mailOutboundEvent{}}
	for rows.Next() {
		var item MailOutboundItem
		if err = rows.Scan(&item.ID, &item.MailboxID, &item.Email, &item.ParentEmail, &item.Link, &item.ReturnedAt); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		if !v.Downloadable {
			item.Link = ""
		}
		detail.Items = append(detail.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	rows, err = a.store.db.QueryContext(r.Context(), "SELECT actor,action,reason,count,created_at FROM mail_outbound_history WHERE outbound_id=? ORDER BY id", id)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var event mailOutboundEvent
		if err = rows.Scan(&event.Actor, &event.Action, &event.Reason, &event.Count, &event.CreatedAt); err != nil {
			fail(w, err)
			return
		}
		detail.History = append(detail.History, event)
	}
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, detail)
}

type outboundInput struct {
	IDs           []int64 `json:"ids"`
	MasterIDs     []int64 `json:"master_ids"`
	FolderID      int64   `json:"folder_id"`
	Quantity      int     `json:"quantity"`
	Mode          string  `json:"mode"`
	Selection     string  `json:"selection"`
	IncludeMaster bool    `json:"include_master"`
	Inspection    string  `json:"inspection"`
	ValidHours    int     `json:"valid_hours"`
	Customer      string  `json:"customer"`
	Note          string  `json:"note"`
	RequestKey    string  `json:"request_key"`
}

type outboundCandidate struct {
	ID, ParentID             int64
	Email, ParentEmail, Link string
	Eligible                 bool
}

func uniqueOutboundIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 || len(ids) > 10000 {
		return nil, errors.New("请选择 1–10000 条记录")
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
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

func outboundMarks(ids []int64) (string, []any) {
	pl := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		pl[i], args[i] = "?", id
	}
	return strings.Join(pl, ","), args
}

func requireOutboundIDs(tx *sql.Tx, ids []int64, kind string) error {
	if len(ids) == 0 {
		return errors.New("请选择邮箱")
	}
	pl, args := outboundMarks(ids)
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE kind=? AND id IN ("+pl+")", append([]any{kind}, args...)...).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return ErrConflict
	}
	return nil
}

func checkOutboundFolder(tx *sql.Tx, id int64) error {
	if id == 0 {
		return nil
	}
	var found int64
	if err := tx.QueryRow("SELECT id FROM mail_folders WHERE id=?", id).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

const outboundInspectionSQL = `EXISTS(SELECT 1 FROM mail_inspection_results ir JOIN mail_inspections it ON it.id=ir.inspection_id
 WHERE ir.mailbox_id=c.id AND ` + mailInspectionMatchesSQL + ` AND it.mode=? AND ir.processed=1 AND ir.result='passed' AND ir.checked_at>=?
 AND ir.id=(SELECT MAX(ir2.id) FROM mail_inspection_results ir2 JOIN mail_inspections it2 ON it2.id=ir2.inspection_id WHERE ir2.mailbox_id=c.id AND it2.mode=? AND ir2.processed=1))`

func outboundCandidates(tx *sql.Tx, in outboundInput) ([]outboundCandidate, error) {
	if in.Selection == "" {
		in.Selection = "random"
	}
	if in.Selection != "random" && in.Selection != "group" {
		return nil, errors.New("抽取方式无效")
	}
	if in.Selection == "group" && len(in.MasterIDs) == 0 {
		return nil, errors.New("整组出售需要选择主邮箱")
	}
	if in.Selection == "random" && (in.Quantity < 1 || in.Quantity > 10000) {
		return nil, errors.New("抽取数量需为 1–10000")
	}
	if len(in.IDs) > 0 && len(in.MasterIDs) > 0 {
		return nil, errors.New("请选择一种出库范围")
	}
	where, args := "c.kind='child'", []any{}
	ids, kind := in.IDs, "child"
	if len(in.MasterIDs) > 0 {
		ids, kind = in.MasterIDs, "master"
	}
	if len(ids) > 0 {
		var err error
		ids, err = uniqueOutboundIDs(ids)
		if err != nil {
			return nil, err
		}
		if err = requireOutboundIDs(tx, ids, kind); err != nil {
			return nil, err
		}
		ph, p := outboundMarks(ids)
		column := "c.id"
		if kind == "master" {
			column = "c.parent_id"
		}
		where += " AND " + column + " IN (" + ph + ")"
		args = append(args, p...)
	} else {
		if in.FolderID < 0 {
			return nil, errors.New("请选择来源文件夹")
		}
		if err := checkOutboundFolder(tx, in.FolderID); err != nil {
			return nil, err
		}
		where += " AND COALESCE(m.folder_id,0)=?"
		args = append(args, in.FolderID)
	}
	eligibility := sellableSQL
	if in.Inspection != "" {
		if (in.Inspection != "link" && in.Inspection != "inbox") || in.ValidHours < 1 || in.ValidHours > 720 {
			return nil, errors.New("请选择检测类型及 1–720 小时有效期")
		}
		eligibility += ` AND ` + outboundInspectionSQL
		args = append([]any{in.Inspection, stampNowAt(time.Now().Add(-time.Duration(in.ValidHours) * time.Hour)), in.Inspection}, args...)
	}
	order := "RANDOM()"
	if in.Selection == "group" {
		order = "c.parent_id,c.position,c.id"
	}
	rows, err := tx.Query("SELECT c.id,COALESCE(c.parent_id,0),c.email,COALESCE(m.email,''),c.link,COALESCE(("+eligibility+"),0)"+childFrom+"WHERE "+where+" ORDER BY "+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[int64][]outboundCandidate{}
	all := []outboundCandidate{}
	for rows.Next() {
		var v outboundCandidate
		if err = rows.Scan(&v.ID, &v.ParentID, &v.Email, &v.ParentEmail, &v.Link, &v.Eligible); err != nil {
			return nil, err
		}
		groups[v.ParentID] = append(groups[v.ParentID], v)
		if v.Eligible {
			all = append(all, v)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if in.Selection == "group" {
		all = nil
		for _, id := range ids {
			if len(groups[id]) == 0 {
				return nil, errors.New("整组出售要求每组至少有一个子邮箱")
			}
			for _, c := range groups[id] {
				if !c.Eligible {
					return nil, errors.New("所选组内存在不可售或不符合检测要求的子邮箱，不能整组出售")
				}
				all = append(all, c)
			}
		}
		if len(all) > 10000 {
			return nil, errors.New("一次出库最多 10000 个子邮箱")
		}
		return all, nil
	}
	if len(all) < in.Quantity {
		return nil, fmt.Errorf("符合条件的库存只有 %d 个，少于请求数量", len(all))
	}
	if kind == "master" && len(ids) > 0 {
		available := map[int64][]outboundCandidate{}
		for _, c := range all {
			available[c.ParentID] = append(available[c.ParentID], c)
		}
		all = nil
		for len(all) < in.Quantity {
			for _, id := range ids {
				if len(available[id]) > 0 {
					all = append(all, available[id][0])
					available[id] = available[id][1:]
					if len(all) == in.Quantity {
						break
					}
				}
			}
		}
	}
	return all[:in.Quantity], nil
}

func stampNowAt(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func (a *App) recordOutbound(tx *sql.Tx, in outboundInput, items []outboundCandidate, actor, hash string, base string) (int64, string, error) {
	if err := a.resolveOutboundLinks(context.Background(), tx, items, base); err != nil {
		return 0, "", err
	}
	now, status := stampNow(), "reserved"
	var delivered any
	if in.Mode == "sell" || in.Mode == "quick" {
		status, delivered = "sold", now
	}
	res, err := tx.Exec("INSERT INTO mail_outbounds(order_no,mode,status,selection,include_master,customer,note,actor,created_at,delivered_at,request_key,request_hash,inspection_mode,inspection_valid_hours) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", "PENDING-"+randomToken(), in.Mode, status, in.Selection, in.IncludeMaster, in.Customer, in.Note, actor, now, delivered, in.RequestKey, hash, in.Inspection, in.ValidHours)
	if err != nil {
		return 0, "", err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, "", err
	}
	order := fmt.Sprintf("OUT-%s-%06d", time.Now().Format("20060102"), id)
	if _, err = tx.Exec("UPDATE mail_outbounds SET order_no=? WHERE id=?", order, id); err != nil {
		return 0, "", err
	}
	seen := map[int64]bool{}
	position := 0
	for _, c := range items {
		res, err = tx.Exec("UPDATE mailboxes SET status=?,updated_at=? WHERE id=? AND status='available'", status, now, c.ID)
		if err != nil {
			return 0, "", err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return 0, "", ErrConflict
		}
		if _, err = tx.Exec("INSERT INTO mail_outbound_items(outbound_id,mailbox_id,email,parent_email,link) VALUES(?,?,?,?,?)", id, c.ID, c.Email, c.ParentEmail, c.Link); err != nil {
			return 0, "", err
		}
		if err = mailHistory(tx, c.ID, actor, "mail.outbound."+status, order+"："+in.Note); err != nil {
			return 0, "", err
		}
		if c.ParentID > 0 && !seen[c.ParentID] {
			seen[c.ParentID] = true
			var parentEmail, raw string
			if err = tx.QueryRow("SELECT email,raw FROM mailboxes WHERE id=? AND kind='master'", c.ParentID).Scan(&parentEmail, &raw); err != nil {
				return 0, "", err
			}
			plain, err := a.mailMasterRaw(raw)
			if err != nil {
				return 0, "", err
			}
			ciphertext, err := seal(a.key, []byte(plain))
			if err != nil {
				return 0, "", err
			}
			if _, err = tx.Exec("INSERT INTO mail_outbound_masters(outbound_id,master_id,email,raw,position) VALUES(?,?,?,?,?)", id, c.ParentID, parentEmail, ciphertext, position); err != nil {
				return 0, "", err
			}
			position++
		}
	}
	if in.Selection == "group" && status == "sold" {
		if err = a.setOutboundMastersSold(tx, id, actor); err != nil {
			return 0, "", err
		}
	}
	if _, err = tx.Exec("INSERT INTO mail_outbound_history(outbound_id,actor,action,reason,count,created_at) VALUES(?,?,'created',?,?,?)", id, actor, in.Note, len(items), now); err != nil {
		return 0, "", err
	}
	return id, order, nil
}

func (a *App) setOutboundMastersSold(tx *sql.Tx, id int64, actor string) error {
	if _, err := tx.Exec("UPDATE mailboxes SET status='sold',updated_at=? WHERE id IN (SELECT master_id FROM mail_outbound_masters WHERE outbound_id=?)", stampNow(), id); err != nil {
		return err
	}
	_, err := tx.Exec("INSERT INTO mail_history(mailbox_id,actor,action,reason,created_at) SELECT master_id,?,'mail.master.status','整组出售',? FROM mail_outbound_masters WHERE outbound_id=? AND master_id IS NOT NULL", actor, stampNow(), id)
	return err
}

func (a *App) createMailOutbound(w http.ResponseWriter, r *http.Request) {
	var in outboundInput
	if !readJSON(w, r, &in) {
		return
	}
	quick := strings.HasSuffix(r.URL.Path, "/quick-outbound")
	if quick {
		in.Mode, in.Selection, in.IncludeMaster = "quick", "random", false
		if len(in.IDs) > 0 || len(in.MasterIDs) > 0 {
			fail(w, errors.New("快捷出库仅支持文件夹范围"))
			return
		}
	}
	in.Customer, in.Note, in.RequestKey = strings.TrimSpace(in.Customer), strings.TrimSpace(in.Note), strings.TrimSpace(in.RequestKey)
	if in.Selection == "" {
		in.Selection = "random"
	}
	if (in.Mode != "reserve" && in.Mode != "sell" && in.Mode != "quick") || len([]rune(in.Customer)) > 120 || len([]rune(in.Note)) > 1000 {
		fail(w, errors.New("出库方式或客户、备注长度无效"))
		return
	}
	if len(in.RequestKey) < 8 || len(in.RequestKey) > 128 {
		fail(w, errors.New("缺少有效的出库请求标识，请重新打开出库窗口"))
		return
	}
	payload, _ := json.Marshal(in)
	actor := "admin"
	hash := digest(actor + ":" + string(payload))
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	var oldHash string
	var oldID int64
	err = tx.QueryRowContext(r.Context(), "SELECT id,request_hash FROM mail_outbounds WHERE request_key=?", in.RequestKey).Scan(&oldID, &oldHash)
	if err == nil {
		if oldHash != hash {
			fail(w, ErrConflict)
			return
		}
		respond(w, http.StatusOK, map[string]any{"id": oldID, "reused": true})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(w, err)
		return
	}
	items, err := outboundCandidates(tx, in)
	if err != nil {
		fail(w, err)
		return
	}
	id, order, err := a.recordOutbound(tx, in, items, actor, hash, a.baseURL(r))
	if err != nil {
		fail(w, err)
		return
	}
	parents := map[int64]bool{}
	for _, item := range items {
		if item.ParentID > 0 {
			parents[item.ParentID] = true
		}
	}
	if err = recordMailActivity(r.Context(), tx, MailActivity{
		Action: "mail.outbound.created", Scope: "child", SelectionCount: len(items),
		MasterCount: len(parents), ChildCount: len(items),
	}); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusCreated, map[string]any{"id": id, "order_no": order, "items": len(items)})
}

func (a *App) updateMailOutbound(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in struct {
		Action  string  `json:"action"`
		Reason  string  `json:"reason"`
		ItemIDs []int64 `json:"item_ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Action != "deliver" && in.Action != "cancel" && in.Action != "return" {
		fail(w, errors.New("出库操作无效"))
		return
	}
	if len([]rune(in.Reason)) > 500 || ((in.Action == "cancel" || in.Action == "return") && in.Reason == "") {
		fail(w, errors.New("请填写 1–500 字操作原因"))
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
	var status, selection, order, inspectionMode string
	var inspectionValidHours int
	var disabled bool
	if err = tx.QueryRowContext(r.Context(), "SELECT status,selection,order_no,download_disabled,inspection_mode,inspection_valid_hours FROM mail_outbounds WHERE id=?", id).Scan(&status, &selection, &order, &disabled, &inspectionMode, &inspectionValidHours); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	if disabled && status != "partial_return" {
		fail(w, ErrConflict)
		return
	}
	target, required := "sold", "reserved"
	if in.Action == "cancel" || in.Action == "return" {
		target = "available"
	}
	if in.Action == "return" {
		required = "sold"
	}
	if (in.Action != "return" && status != "reserved") || (in.Action == "return" && status != "sold" && status != "partial_return") {
		fail(w, ErrConflict)
		return
	}
	where, args := "outbound_id=? AND returned_at IS NULL", []any{id}
	if in.Action == "return" {
		ids, e := uniqueOutboundIDs(in.ItemIDs)
		if e != nil {
			fail(w, e)
			return
		}
		if len(ids) != len(in.ItemIDs) {
			fail(w, ErrConflict)
			return
		}
		ph, p := outboundMarks(ids)
		where += " AND id IN (" + ph + ")"
		args = append(args, p...)
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT id,mailbox_id,parent_email,link FROM mail_outbound_items WHERE "+where, args...)
	if err != nil {
		fail(w, err)
		return
	}
	type chosen struct {
		item    int64
		mailbox sql.NullInt64
		link    string
	}
	items := []chosen{}
	parents := map[string]bool{}
	for rows.Next() {
		var v chosen
		var parent string
		if err = rows.Scan(&v.item, &v.mailbox, &parent, &v.link); err != nil {
			rows.Close()
			fail(w, ErrConflict)
			return
		}
		if parent != "" {
			parents[strings.ToLower(parent)] = true
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	if len(items) == 0 || (in.Action == "return" && len(items) != len(in.ItemIDs)) {
		fail(w, ErrConflict)
		return
	}
	if in.Action == "deliver" {
		// Reservation changes the stock status, but must not freeze its sales
		// eligibility. Recheck every item and its original parent in this same
		// transaction before changing any mailbox to sold.
		var invalid bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(
		 SELECT 1 FROM mail_outbound_items oi
		 LEFT JOIN mailboxes c ON c.id=oi.mailbox_id
		 LEFT JOIN mailboxes m ON m.id=c.parent_id
		 WHERE oi.outbound_id=? AND oi.returned_at IS NULL AND NOT COALESCE((
		 c.kind='child' AND c.status='reserved' AND c.abnormal=0 AND c.disabled=0 AND c.after_sale=0
		 AND m.kind='master' AND m.status='available'
		 AND c.email=oi.email COLLATE NOCASE AND m.email=oi.parent_email COLLATE NOCASE
		 AND EXISTS(SELECT 1 FROM mail_outbound_masters om
		 WHERE om.outbound_id=oi.outbound_id AND om.master_id=c.parent_id)),0))`, id).Scan(&invalid); err != nil {
			fail(w, err)
			return
		}
		if invalid {
			mailError(w, mailFailure{http.StatusConflict, "预留邮箱的状态、标识或主邮箱归属已变化，请恢复后交付，或取消预留后重新出库"})
			return
		}
		if selection == "group" {
			if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM mailboxes c WHERE c.parent_id IN (SELECT master_id FROM mail_outbound_masters WHERE outbound_id=?) AND NOT EXISTS(SELECT 1 FROM mail_outbound_items oi WHERE oi.outbound_id=? AND oi.mailbox_id=c.id))`, id, id).Scan(&invalid); err != nil {
				fail(w, err)
				return
			}
			if invalid {
				fail(w, ErrConflict)
				return
			}
		}
	}
	now := stampNow()
	for _, v := range items {
		if !v.mailbox.Valid {
			fail(w, ErrConflict)
			return
		}
		if in.Action == "deliver" {
			var mode string
			if e := tx.QueryRowContext(r.Context(), "SELECT link_mode FROM mailboxes WHERE id=?", v.mailbox.Int64).Scan(&mode); e != nil {
				fail(w, e)
				return
			}
			if mode == "source" {
				source, e := a.sourceLink(r.Context(), tx, v.mailbox.Int64, a.baseURL(r), true)
				if e != nil {
					fail(w, e)
					return
				}
				if !source.LinkAvailable {
					mailError(w, mailFailure{http.StatusConflict, source.LinkReason})
					return
				}
				if _, e = a.verifyMailSourceURL(r.Context(), tx, source, v.link); e != nil {
					mailError(w, mailFailure{http.StatusConflict, "预留单的取信地址已无法准确定位，请取消预留后重新出库"})
					return
				}
			}
			if inspectionMode != "" {
				var passed bool
				if e := tx.QueryRowContext(r.Context(), "SELECT "+outboundInspectionSQL+" FROM mailboxes c WHERE c.id=?", inspectionMode, stampNowAt(time.Now().Add(-time.Duration(inspectionValidHours)*time.Hour)), inspectionMode, v.mailbox.Int64).Scan(&passed); e != nil {
					fail(w, e)
					return
				}
				if !passed {
					mailError(w, mailFailure{http.StatusConflict, "检测结果已失效或来源策略已变化，请重新检测后交付"})
					return
				}
			}
		}
		res, e := tx.ExecContext(r.Context(), "UPDATE mailboxes SET status=?,updated_at=? WHERE id=? AND status=?", target, now, v.mailbox.Int64, required)
		if e != nil {
			fail(w, e)
			return
		}
		changed, _ := res.RowsAffected()
		if changed != 1 {
			fail(w, ErrConflict)
			return
		}
		if in.Action == "return" {
			if _, e = tx.ExecContext(r.Context(), "UPDATE mail_outbound_items SET returned_at=? WHERE id=?", now, v.item); e != nil {
				fail(w, e)
				return
			}
		}
		if e = mailHistory(tx, v.mailbox.Int64, "admin", "mail.outbound."+in.Action, order+"："+in.Reason); e != nil {
			fail(w, e)
			return
		}
	}
	switch in.Action {
	case "deliver":
		if _, err = tx.ExecContext(r.Context(), "UPDATE mail_outbounds SET status='sold',delivered_at=? WHERE id=?", now, id); err == nil && selection == "group" {
			err = a.setOutboundMastersSold(tx, id, "admin")
		}
	case "cancel":
		_, err = tx.ExecContext(r.Context(), "UPDATE mail_outbounds SET status='cancelled',cancelled_at=? WHERE id=?", now, id)
	case "return":
		var left int
		if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM mail_outbound_items WHERE outbound_id=? AND returned_at IS NULL", id).Scan(&left); err == nil {
			newStatus := "returned"
			if left > 0 {
				newStatus = "partial_return"
			}
			_, err = tx.ExecContext(r.Context(), "UPDATE mail_outbounds SET status=?,download_disabled=1 WHERE id=?", newStatus, id)
		}
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO mail_outbound_history(outbound_id,actor,action,reason,count,created_at) VALUES(?,?,?,?,?,?)", id, "admin", in.Action, in.Reason, len(items), now)
	}
	if err == nil {
		err = recordMailActivity(r.Context(), tx, MailActivity{
			Action: "mail.outbound." + in.Action, Scope: "child", SelectionCount: len(items),
			MasterCount: len(parents), ChildCount: len(items),
		})
	}
	if err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) mailOutboundExport(w http.ResponseWriter, r *http.Request) {
	id, err := mailInventoryID(r)
	if err != nil {
		fail(w, err)
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	var order, selection, mode, status string
	var include, disabled bool
	if err = tx.QueryRowContext(r.Context(), "SELECT order_no,selection,mode,status,include_master,download_disabled FROM mail_outbounds WHERE id=?", id).Scan(&order, &selection, &mode, &status, &include, &disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, ErrNotFound)
		} else {
			fail(w, err)
		}
		return
	}
	var missing int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM mail_outbound_items WHERE outbound_id=? AND (mailbox_id IS NULL OR returned_at IS NOT NULL)", id).Scan(&missing); err != nil {
		fail(w, err)
		return
	}
	if disabled || missing > 0 || (status != "reserved" && status != "sold") {
		fail(w, ErrConflict)
		return
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT email,parent_email,link FROM mail_outbound_items WHERE outbound_id=? ORDER BY id", id)
	if err != nil {
		fail(w, err)
		return
	}
	items := []outboundCandidate{}
	for rows.Next() {
		var c outboundCandidate
		if err = rows.Scan(&c.Email, &c.ParentEmail, &c.Link); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		items = append(items, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	include = include || selection == "group"
	if value := r.URL.Query().Get("include_master"); value != "" {
		include, err = strconv.ParseBool(value)
		if err != nil {
			fail(w, errors.New("主邮箱资料导出选项无效"))
			return
		}
	}
	lines := make([]string, 0, len(items))
	if mode != "quick" && include {
		rows, err = tx.QueryContext(r.Context(), "SELECT email,raw FROM mail_outbound_masters WHERE outbound_id=? ORDER BY position", id)
		if err != nil {
			fail(w, err)
			return
		}
		for rows.Next() {
			var email, raw string
			if err = rows.Scan(&email, &raw); err != nil {
				rows.Close()
				fail(w, err)
				return
			}
			plain, e := a.mailMasterRaw(raw)
			if e != nil {
				rows.Close()
				fail(w, e)
				return
			}
			lines = append(lines, mailTXTLine(strings.Split(plain, "----")...))
			for _, c := range items {
				if strings.EqualFold(c.ParentEmail, email) {
					lines = append(lines, mailTXTLine(c.Email, c.Link))
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			fail(w, err)
			return
		}
	} else {
		for _, c := range items {
			lines = append(lines, mailTXTLine(c.Email, c.Link))
		}
	}
	respond(w, http.StatusOK, map[string]string{"filename": order + ".txt", "content": strings.Join(lines, "\n")})
}

func (a *App) mailStock(w http.ResponseWriter, r *http.Request) {
	where, args, err := mailFilter(r.URL.Query(), "child")
	if err != nil {
		fail(w, err)
		return
	}
	var n int
	if err = a.store.db.QueryRowContext(r.Context(), "SELECT COUNT(*)"+childFrom+"WHERE "+where+" AND "+sellableSQL, args...).Scan(&n); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]int{"sellable": n})
}

func (a *App) mailSelection(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "master" && kind != "child" {
		fail(w, errors.New("邮箱类型无效"))
		return
	}
	where, args, err := mailFilter(r.URL.Query(), kind)
	if err != nil {
		fail(w, err)
		return
	}
	alias, from := "m", masterFrom
	if kind == "child" {
		alias, from = "c", childFrom
	}
	rows, err := a.store.db.QueryContext(r.Context(), "SELECT "+alias+".id"+from+"WHERE "+where+" ORDER BY "+alias+".id LIMIT 501", args...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			fail(w, err)
			return
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	if len(ids) > 500 {
		fail(w, errors.New("当前结果超过 500 条，请缩小筛选范围后全选"))
		return
	}
	respond(w, http.StatusOK, map[string]any{"ids": ids})
}

func (a *App) mailStatusUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []int64 `json:"ids"`
		Status string  `json:"status"`
		Reason string  `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ids, err := uniqueOutboundIDs(in.IDs)
	if err != nil {
		fail(w, err)
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if (in.Status != "available" && in.Status != "sold") || in.Reason == "" || len([]rune(in.Reason)) > 500 {
		fail(w, errors.New("请选择状态并填写 1–500 字修改原因"))
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
	if err = requireOutboundIDs(tx, ids, "master"); err != nil {
		fail(w, err)
		return
	}
	ph, args := outboundMarks(ids)
	var reserved int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM mailboxes WHERE parent_id IN ("+ph+") AND status='reserved'", args...).Scan(&reserved); err != nil {
		fail(w, err)
		return
	}
	if reserved > 0 {
		fail(w, ErrConflict)
		return
	}
	if in.Status == "sold" {
		rows, e := tx.QueryContext(r.Context(), "SELECT c.id,c.parent_id,c.email,m.email,c.link"+childFrom+"WHERE c.parent_id IN ("+ph+") AND c.status='available' ORDER BY c.parent_id,c.position,c.id", args...)
		if e != nil {
			fail(w, e)
			return
		}
		items := []outboundCandidate{}
		for rows.Next() {
			var c outboundCandidate
			if e = rows.Scan(&c.ID, &c.ParentID, &c.Email, &c.ParentEmail, &c.Link); e != nil {
				rows.Close()
				fail(w, e)
				return
			}
			items = append(items, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			fail(w, e)
			return
		}
		if len(items) > 0 {
			if _, _, e = a.recordOutbound(tx, outboundInput{Mode: "sell", Selection: "status", Note: in.Reason}, items, "admin", "", a.baseURL(r)); e != nil {
				fail(w, e)
				return
			}
		}
	}
	if _, err = tx.ExecContext(r.Context(), "UPDATE mailboxes SET status=?,updated_at=? WHERE id IN ("+ph+")", append([]any{in.Status, stampNow()}, args...)...); err != nil {
		fail(w, err)
		return
	}
	for _, id := range ids {
		if err = mailHistory(tx, id, "admin", "mail.master.status", in.Status+"："+in.Reason); err != nil {
			fail(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]int{"updated": len(ids)})
}

func (a *App) mailChildStatusUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []int64 `json:"ids"`
		Status string  `json:"status"`
		Reason string  `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ids, err := uniqueOutboundIDs(in.IDs)
	if err != nil {
		fail(w, err)
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if (in.Status != "available" && in.Status != "sold") || in.Reason == "" || len([]rune(in.Reason)) > 500 {
		fail(w, errors.New("请选择状态并填写 1–500 字修改原因"))
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
	if err = requireOutboundIDs(tx, ids, "child"); err != nil {
		fail(w, err)
		return
	}
	ph, args := outboundMarks(ids)
	rows, err := tx.QueryContext(r.Context(), "SELECT c.id,c.parent_id,c.email,COALESCE(m.email,''),c.link,c.status FROM mailboxes c LEFT JOIN mailboxes m ON m.id=c.parent_id WHERE c.kind='child' AND c.id IN ("+ph+")", args...)
	if err != nil {
		fail(w, err)
		return
	}
	selected := make([]outboundCandidate, 0, len(ids))
	var reserved, sold int
	for rows.Next() {
		var c outboundCandidate
		var status string
		if err = rows.Scan(&c.ID, &c.ParentID, &c.Email, &c.ParentEmail, &c.Link, &status); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		switch status {
		case "reserved":
			reserved++
		case "sold":
			sold++
		case "available":
			selected = append(selected, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	if reserved > 0 {
		fail(w, ErrConflict)
		return
	}
	if in.Status == "available" && sold > 0 {
		fail(w, ErrConflict)
		return
	}
	if in.Status == "sold" && len(selected) > 0 {
		if _, _, err = a.recordOutbound(tx, outboundInput{Mode: "sell", Selection: "status", Note: in.Reason}, selected, "admin", "", a.baseURL(r)); err != nil {
			fail(w, err)
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), "UPDATE mailboxes SET status=?,updated_at=? WHERE kind='child' AND id IN ("+ph+") AND status='available'", append([]any{in.Status, stampNow()}, args...)...); err != nil {
		fail(w, err)
		return
	}
	for _, id := range ids {
		if err = mailHistory(tx, id, "admin", "mail.child.status", in.Status+"："+in.Reason); err != nil {
			fail(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]int{"updated": len(selected)})
}
