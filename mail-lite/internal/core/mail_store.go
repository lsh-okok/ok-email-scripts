package core

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Email inventory is independent of users and future platform accounts.
func (a *App) migrateMail() error {
	_, err := a.store.db.Exec(`
	CREATE TABLE IF NOT EXISTS mail_folders (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE COLLATE NOCASE,
	 created_at TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS mailboxes (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE COLLATE NOCASE,
	 kind TEXT NOT NULL CHECK(kind IN ('master','child')),
	 parent_id INTEGER REFERENCES mailboxes(id) ON DELETE CASCADE,
	 folder_id INTEGER REFERENCES mail_folders(id) ON DELETE SET NULL,
	 raw TEXT NOT NULL DEFAULT '', raw_complete INTEGER NOT NULL DEFAULT 0 CHECK(raw_complete IN (0,1)),
	 note TEXT NOT NULL DEFAULT '', link TEXT NOT NULL DEFAULT '',
	 status TEXT NOT NULL DEFAULT 'available' CHECK(status IN ('available','reserved','sold')),
	 abnormal INTEGER NOT NULL DEFAULT 0 CHECK(abnormal IN (0,1)),
	 disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN (0,1)),
	 after_sale INTEGER NOT NULL DEFAULT 0 CHECK(after_sale IN (0,1)),
	 position INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
	 CHECK((kind='master' AND parent_id IS NULL AND status<>'reserved' AND link='') OR
	 (kind='child' AND folder_id IS NULL AND raw='')));
	CREATE INDEX IF NOT EXISTS mailboxes_parent ON mailboxes(parent_id,position,id);
	CREATE INDEX IF NOT EXISTS mailboxes_folder ON mailboxes(folder_id);
	CREATE INDEX IF NOT EXISTS mailboxes_status ON mailboxes(kind,status);
	CREATE TRIGGER IF NOT EXISTS mail_parent_insert BEFORE INSERT ON mailboxes
	 WHEN NEW.parent_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes WHERE id=NEW.parent_id AND kind='master')
	 BEGIN SELECT RAISE(ABORT,'invalid mail parent'); END;
	CREATE TRIGGER IF NOT EXISTS mail_parent_update BEFORE UPDATE OF parent_id ON mailboxes
	 WHEN NEW.parent_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes WHERE id=NEW.parent_id AND kind='master')
	 BEGIN SELECT RAISE(ABORT,'invalid mail parent'); END;
	CREATE TABLE IF NOT EXISTS mail_history (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
	 actor TEXT NOT NULL, action TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
	CREATE INDEX IF NOT EXISTS mail_history_record ON mail_history(mailbox_id,id DESC);
	CREATE TABLE IF NOT EXISTS mail_activities (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, actor TEXT NOT NULL, action TEXT NOT NULL,
	 scope TEXT NOT NULL CHECK(scope IN ('master','child')), format TEXT NOT NULL DEFAULT '',
	 selection_count INTEGER NOT NULL CHECK(selection_count>=0),
	 master_count INTEGER NOT NULL CHECK(master_count>=0), child_count INTEGER NOT NULL CHECK(child_count>=0),
	 created_at TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS mail_import_previews (
		token TEXT PRIMARY KEY, session_token TEXT NOT NULL,
	 payload TEXT NOT NULL, folder_id INTEGER, expires_at TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS mail_outbounds (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, order_no TEXT NOT NULL UNIQUE,
	 mode TEXT NOT NULL CHECK(mode IN ('reserve','sell','quick')),
	 status TEXT NOT NULL CHECK(status IN ('reserved','sold','cancelled','partial_return','returned')),
	 customer TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '',
	 actor TEXT NOT NULL, created_at TEXT NOT NULL, delivered_at TEXT, cancelled_at TEXT);
	CREATE TABLE IF NOT EXISTS mail_outbound_items (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, outbound_id INTEGER NOT NULL REFERENCES mail_outbounds(id) ON DELETE CASCADE,
	 mailbox_id INTEGER REFERENCES mailboxes(id) ON DELETE SET NULL,
	 email TEXT NOT NULL, parent_email TEXT NOT NULL DEFAULT '', link TEXT NOT NULL DEFAULT '',
	 returned_at TEXT);
	CREATE INDEX IF NOT EXISTS mail_outbounds_created ON mail_outbounds(created_at DESC);
	CREATE INDEX IF NOT EXISTS mail_outbound_items_order ON mail_outbound_items(outbound_id);
	CREATE TABLE IF NOT EXISTS mail_inspections (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, mode TEXT NOT NULL CHECK(mode IN ('link','inbox')),
	 scope TEXT NOT NULL, folder_id INTEGER, status TEXT NOT NULL CHECK(status IN ('queued','running','completed','stopped','failed')),
	 total INTEGER NOT NULL DEFAULT 0, processed INTEGER NOT NULL DEFAULT 0, passed INTEGER NOT NULL DEFAULT 0,
	 failed INTEGER NOT NULL DEFAULT 0, pending INTEGER NOT NULL DEFAULT 0, actor TEXT NOT NULL,
	created_at TEXT NOT NULL, finished_at TEXT, stop_requested INTEGER NOT NULL DEFAULT 0);
	CREATE TABLE IF NOT EXISTS mail_inspection_results (
	 id INTEGER PRIMARY KEY AUTOINCREMENT, inspection_id INTEGER NOT NULL REFERENCES mail_inspections(id) ON DELETE CASCADE,
	 mailbox_id INTEGER REFERENCES mailboxes(id) ON DELETE SET NULL, email TEXT NOT NULL, link TEXT NOT NULL DEFAULT '',
	 result TEXT NOT NULL CHECK(result IN ('passed','failed','pending')), status_code INTEGER NOT NULL DEFAULT 0,
	message TEXT NOT NULL DEFAULT '', checked_at TEXT NOT NULL);
	CREATE INDEX IF NOT EXISTS mail_inspections_created ON mail_inspections(created_at DESC);
	CREATE INDEX IF NOT EXISTS mail_inspection_results_task ON mail_inspection_results(inspection_id,id);`)
	if err != nil {
		return err
	}
	if err := a.migrateMailOperations(); err != nil {
		return err
	}
	if err := a.migrateMailMapping(); err != nil {
		return err
	}
	if err := a.migrateMailSourceLinks(); err != nil {
		return err
	}
	return a.retireSalesHistory()
}

type MailFolder struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Masters  int    `json:"masters"`
	Children int    `json:"children"`
}
type MailStats struct {
	Masters    int `json:"masters"`
	Children   int `json:"children"`
	Sellable   int `json:"sellable"`
	Reserved   int `json:"reserved"`
	Sold       int `json:"sold"`
	Flagged    int `json:"flagged"`
	Orphans    int `json:"orphans"`
	Incomplete int `json:"incomplete"`
}

type mailStatsReader interface {
	QueryRow(string, ...any) *sql.Row
}

// The workbench and inventory use the same sellable and incomplete rules.
func readMailStats(db mailStatsReader) (MailStats, error) {
	var s MailStats
	err := db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(raw_complete=0),0) FROM mailboxes WHERE kind='master'`).Scan(&s.Masters, &s.Incomplete)
	if err == nil {
		err = db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN `+sellableSQL+` THEN 1 ELSE 0 END),0),
	COALESCE(SUM(c.status='reserved'),0),COALESCE(SUM(c.status='sold'),0),
	COALESCE(SUM(c.abnormal=1 OR c.disabled=1 OR c.after_sale=1),0),COALESCE(SUM(c.parent_id IS NULL),0)`+childFrom+`WHERE c.kind='child'`).Scan(&s.Children, &s.Sellable, &s.Reserved, &s.Sold, &s.Flagged, &s.Orphans)
	}
	return s, err
}

type MailMaster struct {
	ID         int64  `json:"id"`
	Email      string `json:"email"`
	FolderID   int64  `json:"folder_id"`
	Folder     string `json:"folder"`
	Note       string `json:"note"`
	Status     string `json:"status"`
	Incomplete bool   `json:"incomplete"`
	Children   int    `json:"children"`
	Sellable   int    `json:"sellable"`
	Reserved   int    `json:"reserved"`
	Sold       int    `json:"sold"`
	Flagged    int    `json:"flagged"`
	CreatedAt  string `json:"created_at"`
}
type MailChild struct {
	ID            int64  `json:"id"`
	Email         string `json:"email"`
	ParentID      int64  `json:"parent_id"`
	ParentEmail   string `json:"parent_email"`
	Folder        string `json:"folder"`
	Link          string `json:"link,omitempty"`
	SourceMapped  bool   `json:"source_mapped"`
	LinkMode      string `json:"link_mode"`
	LinkAvailable bool   `json:"link_available"`
	LinkReason    string `json:"link_reason"`
	LinkPlatform  string `json:"link_platform"`
	LinkExpiresAt string `json:"link_expires_at"`
	LinkLocalOnly bool   `json:"link_local_only"`
	Note          string `json:"note"`
	Status        string `json:"status"`
	Abnormal      bool   `json:"abnormal"`
	Disabled      bool   `json:"disabled"`
	AfterSale     bool   `json:"after_sale"`
	Sellable      bool   `json:"sellable"`
	Position      int    `json:"position"`
	CreatedAt     string `json:"created_at"`
	LinkCheck     string `json:"link_check"`
	InboxCheck    string `json:"inbox_check"`
	OutboundID    int64  `json:"outbound_id"`
}

const sellableSQL = `c.status='available' AND c.abnormal=0 AND c.disabled=0 AND c.after_sale=0 AND c.parent_id IS NOT NULL AND ` + mailLinkReadySQL + ` AND m.status='available'`
const masterFrom = ` FROM mailboxes m LEFT JOIN mail_folders f ON f.id=m.folder_id LEFT JOIN (
	SELECT c.parent_id,COUNT(*) children,SUM(CASE WHEN ` + sellableSQL + ` THEN 1 ELSE 0 END) sellable,
	SUM(c.status='reserved') reserved,SUM(c.status='sold') sold,
	SUM(c.abnormal=1 OR c.disabled=1 OR c.after_sale=1) flagged
	FROM mailboxes c LEFT JOIN mailboxes m ON m.id=c.parent_id WHERE c.kind='child' GROUP BY c.parent_id
) s ON s.parent_id=m.id `
const masterColumns = `m.id,m.email,COALESCE(m.folder_id,0),COALESCE(f.name,'未分类'),m.note,m.status,
 (m.raw_complete=0),COALESCE(s.children,0),COALESCE(s.sellable,0),COALESCE(s.reserved,0),COALESCE(s.sold,0),COALESCE(s.flagged,0),m.created_at`
const childFrom = ` FROM mailboxes c LEFT JOIN mailboxes m ON m.id=c.parent_id LEFT JOIN mail_folders f ON f.id=m.folder_id `
const childColumns = `c.id,c.email,COALESCE(c.parent_id,0),COALESCE(m.email,''),COALESCE(f.name,'未分类'),c.link,c.note,c.status,c.abnormal,c.disabled,c.after_sale,COALESCE((` + sellableSQL + `),0),c.position,c.created_at,
 COALESCE((SELECT ir.result FROM mail_inspection_results ir JOIN mail_inspections it ON it.id=ir.inspection_id WHERE ir.mailbox_id=c.id AND ` + mailInspectionMatchesSQL + ` AND ir.processed=1 AND it.mode='link' ORDER BY ir.id DESC LIMIT 1),'unmeasured'),
 COALESCE((SELECT ir.result FROM mail_inspection_results ir JOIN mail_inspections it ON it.id=ir.inspection_id WHERE ir.mailbox_id=c.id AND ` + mailInspectionMatchesSQL + ` AND ir.processed=1 AND it.mode='inbox' ORDER BY ir.id DESC LIMIT 1),'unmeasured'),
 COALESCE((SELECT oi.outbound_id FROM mail_outbound_items oi JOIN mail_outbounds o ON o.id=oi.outbound_id WHERE oi.mailbox_id=c.id AND oi.returned_at IS NULL AND o.status IN ('reserved','sold','partial_return') ORDER BY oi.id DESC LIMIT 1),0),c.link_mode,EXISTS(SELECT 1 FROM mail_source_mappings sm WHERE sm.mailbox_id=c.id AND sm.source_kind='link')`

func scanMaster(row scanner) (MailMaster, error) {
	var m MailMaster
	err := row.Scan(&m.ID, &m.Email, &m.FolderID, &m.Folder, &m.Note, &m.Status, &m.Incomplete, &m.Children, &m.Sellable, &m.Reserved, &m.Sold, &m.Flagged, &m.CreatedAt)
	return m, err
}
func scanChild(row scanner) (MailChild, error) {
	var c MailChild
	err := row.Scan(&c.ID, &c.Email, &c.ParentID, &c.ParentEmail, &c.Folder, &c.Link, &c.Note, &c.Status, &c.Abnormal, &c.Disabled, &c.AfterSale, &c.Sellable, &c.Position, &c.CreatedAt, &c.LinkCheck, &c.InboxCheck, &c.OutboundID, &c.LinkMode, &c.SourceMapped)
	return c, err
}
func likeMail(s string) string {
	return "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(s) + "%"
}

func mailFilter(q url.Values, kind string) (string, []any, error) {
	alias := "m"
	if kind == "child" {
		alias = "c"
	}
	where := alias + ".kind=?"
	args := []any{kind}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		if len(s) > 254 {
			return "", nil, fmt.Errorf("搜索内容过长")
		}
		where += " AND (" + alias + ".email LIKE ? ESCAPE '\\' OR " + alias + ".note LIKE ? ESCAPE '\\')"
		args = append(args, likeMail(s), likeMail(s))
	}
	if folder := q.Get("folder"); folder != "" {
		id, e := strconv.ParseInt(folder, 10, 64)
		if e != nil || id < 0 {
			return "", nil, fmt.Errorf("文件夹筛选无效")
		}
		where += " AND COALESCE(m.folder_id,0)=?"
		args = append(args, id)
	}
	if status := q.Get("status"); status != "" {
		if status != "available" && status != "sold" && !(kind == "child" && status == "reserved") {
			return "", nil, fmt.Errorf("状态筛选无效")
		}
		where += " AND " + alias + ".status=?"
		args = append(args, status)
	}
	if kind == "master" {
		switch q.Get("inventory") {
		case "":
		case "sellable":
			where += " AND COALESCE(s.sellable,0)>0"
		case "sold":
			where += " AND s.children>0 AND s.sold=s.children"
		case "partial":
			where += " AND s.sold>0 AND s.sold<s.children"
		case "incomplete":
			where += " AND m.raw_complete=0"
		default:
			return "", nil, fmt.Errorf("库存筛选无效")
		}
	} else {
		if parent := q.Get("parent"); parent != "" {
			id, e := strconv.ParseInt(parent, 10, 64)
			if e != nil || id < 1 {
				return "", nil, fmt.Errorf("主邮箱筛选无效")
			}
			where += " AND c.parent_id=?"
			args = append(args, id)
		}
		switch q.Get("flag") {
		case "":
		case "sellable":
			where += " AND " + sellableSQL
		case "unsellable":
			where += " AND c.status='available' AND NOT COALESCE((" + sellableSQL + "),0)"
		case "abnormal":
			where += " AND c.abnormal=1"
		case "disabled":
			where += " AND c.disabled=1"
		case "after_sale":
			where += " AND c.after_sale=1"
		case "orphan":
			where += " AND c.parent_id IS NULL"
		default:
			return "", nil, fmt.Errorf("标识筛选无效")
		}
	}
	return where, args, nil
}

func mailHistory(tx *sql.Tx, id int64, actor, action, reason string) error {
	_, err := tx.Exec("INSERT INTO mail_history(mailbox_id,actor,action,reason,created_at) VALUES(?,?,?,?,?)", id, actor, action, reason, stampNow())
	return err
}
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
func validLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && len(s) <= 8192 && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(s, "\r\n\t")
}
func completeRaw(raw string) bool {
	_, material, ok := strings.Cut(raw, "----")
	return ok && strings.TrimSpace(material) != ""
}
