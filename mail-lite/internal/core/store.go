package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var ErrConflict = errors.New("记录已变更，请刷新列表后重试")
var ErrNotFound = errors.New("record not found")

type Store struct {
	db    *sql.DB
	key   []byte
	path  string
	write sync.Mutex
}

func OpenStore(path string, key []byte) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", filepath.ToSlash(path)+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(8)
	s := &Store{db: db, key: key, path: path}
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS records(kind TEXT NOT NULL,id TEXT NOT NULL,email TEXT NOT NULL DEFAULT '',account_id TEXT NOT NULL DEFAULT '',parent_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT '',provider TEXT NOT NULL DEFAULT '',platform_id TEXT NOT NULL DEFAULT '',recipient TEXT NOT NULL DEFAULT '',token_hash TEXT NOT NULL DEFAULT '',expires_at TEXT NOT NULL DEFAULT '',data TEXT NOT NULL,version INTEGER NOT NULL,PRIMARY KEY(kind,id));
CREATE UNIQUE INDEX IF NOT EXISTS account_email ON records(email COLLATE NOCASE) WHERE kind='account';
CREATE UNIQUE INDEX IF NOT EXISTS link_token ON records(token_hash) WHERE kind='link';
DROP INDEX IF EXISTS link_recipient;
CREATE UNIQUE INDEX IF NOT EXISTS link_recipient ON records(account_id,recipient COLLATE NOCASE,platform_id) WHERE kind='link';
CREATE INDEX IF NOT EXISTS link_recipient_local ON records(account_id,substr(recipient,1,instr(recipient,'@')-1) COLLATE NOCASE,platform_id) WHERE kind='link';
CREATE INDEX IF NOT EXISTS record_account ON records(kind,account_id,id);
CREATE INDEX IF NOT EXISTS record_parent ON records(kind,parent_id,id);
CREATE INDEX IF NOT EXISTS record_platform ON records(kind,platform_id,id);
CREATE INDEX IF NOT EXISTS record_email ON records(kind,email COLLATE NOCASE);
CREATE TABLE IF NOT EXISTS events(cursor INTEGER PRIMARY KEY AUTOINCREMENT,data TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS link_access(link_id TEXT PRIMARY KEY,page_count INTEGER NOT NULL DEFAULT 0,json_count INTEGER NOT NULL DEFAULT 0,last_access_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS redemption_cards(account_id TEXT PRIMARY KEY,code_hash TEXT NOT NULL UNIQUE,code_encrypted TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),created_at TEXT NOT NULL,redeemed_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS aux(bucket TEXT NOT NULL,key TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(bucket,key));`)
	if e != nil {
		db.Close()
		return nil, e
	}
	var hasCode int
	if e = db.QueryRow("SELECT count(*) FROM pragma_table_info('records') WHERE name='public_code'").Scan(&hasCode); e == nil && hasCode == 0 {
		_, e = db.Exec("ALTER TABLE records ADD COLUMN public_code TEXT NOT NULL DEFAULT ''")
	}
	if e == nil {
		_, e = db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS account_public_code ON records(public_code) WHERE kind='account' AND public_code<>''")
	}
	if e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Meta(k string) string {
	var v string
	s.db.QueryRow("SELECT value FROM meta WHERE key=?", k).Scan(&v)
	return v
}
func (s *Store) SetMeta(k, v string) error {
	_, e := s.db.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v)
	return e
}
func (s *Store) Cursor() int64 { v, _ := strconv.ParseInt(s.Meta("cursor"), 10, 64); return v }

const columns = "kind,id,email,account_id,parent_id,status,provider,platform_id,recipient,token_hash,expires_at,data,version,public_code"

type scanner interface{ Scan(...any) error }

func (s *Store) scan(row scanner) (Record, error) {
	var r Record
	var data string
	e := row.Scan(&r.Kind, &r.ID, &r.Email, &r.AccountID, &r.ParentID, &r.Status, &r.Provider, &r.PlatformID, &r.Recipient, &r.TokenHash, &r.ExpiresAt, &data, &r.Version, &r.PublicCode)
	if errors.Is(e, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	r.Data, e = unseal(s.key, data)
	return r, e
}
func (s *Store) Get(ctx context.Context, kind, id string) (Record, error) {
	return s.scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM records WHERE kind=? AND id=?", kind, id))
}
func (s *Store) Find(ctx context.Context, where string, args ...any) (Record, error) {
	return s.scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM records WHERE "+where+" LIMIT 1", args...))
}
func (s *Store) List(ctx context.Context, where string, args []any, limit, offset int) ([]Record, int, error) {
	var total int
	if e := s.db.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE "+where, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	a := append(append([]any{}, args...), limit, offset)
	rows, e := s.db.QueryContext(ctx, "SELECT "+columns+" FROM records WHERE "+where+" ORDER BY id LIMIT ? OFFSET ?", a...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := make([]Record, 0)
	for rows.Next() {
		r, e := s.scan(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
func (s *Store) All(ctx context.Context, kind string) ([]Record, error) {
	r, _, e := s.List(ctx, "kind=?", []any{kind}, 1_000_000, 0)
	return r, e
}
func recordOf(v any) (Record, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return Record{}, e
	}
	r := Record{Data: b}
	switch x := v.(type) {
	case Account:
		r.Kind = "account"
		r.PublicCode = x.PublicCode
		r.ID = x.ID
		r.Email = x.Email
		r.ParentID = x.GroupID
		r.Status = x.Status
		r.Provider = x.Provider
	case Group:
		r.Kind = "group"
		r.ID = x.ID
		r.ParentID = x.ParentID
		r.Email = x.Name
	case Platform:
		r.Kind = "platform"
		r.ID = x.ID
		r.Email = x.Name
	case Link:
		r.Kind = "link"
		r.ID = x.ID
		r.AccountID = x.AccountID
		r.Recipient = x.Recipient
		r.TokenHash = x.TokenHash
		r.PlatformID = x.PlatformID
		r.ExpiresAt = x.ExpiresAt
	case Settings:
		r.Kind = "settings"
		r.ID = "global"
	default:
		return r, errors.New("unknown record type")
	}
	return r, nil
}
func (s *Store) Save(ctx context.Context, v any, expected *int64) (Record, error) {
	r, e := recordOf(v)
	if e != nil {
		return r, e
	}
	e = s.Put(ctx, []Record{r}, expected)
	if e != nil {
		return r, e
	}
	return s.Get(ctx, r.Kind, r.ID)
}
func (s *Store) Put(ctx context.Context, records []Record, expected *int64) error {
	s.write.Lock()
	defer s.write.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, r := range records {
		if expected != nil {
			var version int64
			e = tx.QueryRowContext(ctx, "SELECT version FROM records WHERE kind=? AND id=?", r.Kind, r.ID).Scan(&version)
			if e != nil || version != *expected {
				return ErrConflict
			}
		}
		if r.Kind == "account" {
			if r.Provider == "mx" {
				var v Account
				if e = json.Unmarshal(r.Data, &v); e != nil {
					return e
				}
				if e = validateAccount(&v); e != nil {
					return e
				}
				r, e = recordOf(v)
				if e != nil {
					return e
				}
			}
			var oldProvider, oldEmail string
			previous := tx.QueryRowContext(ctx, "SELECT provider,email FROM records WHERE kind='account' AND id=?", r.ID).Scan(&oldProvider, &oldEmail)
			if previous != nil && !errors.Is(previous, sql.ErrNoRows) {
				return previous
			}
			if previous == nil && (oldProvider == "mx" || r.Provider == "mx") && (oldProvider != r.Provider || !strings.EqualFold(oldEmail, r.Email)) {
				return errors.New("MX 域名和类型不可修改")
			}
			if e = s.prepareAccountCode(ctx, tx, &r); e != nil {
				return e
			}
		}
		if e = validateReferences(ctx, tx, r); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "INSERT INTO events(data,created_at) VALUES('',?)", time.Now().UTC().Format(time.RFC3339Nano))
		if e != nil {
			return e
		}
		r.Version, _ = res.LastInsertId()
		if e = s.upsert(ctx, tx, r); e != nil {
			return e
		}
		b, _ := json.Marshal(Event{Cursor: r.Version, Record: r})
		enc, e := seal(s.key, b)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE events SET data=? WHERE cursor=?", enc, r.Version); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES('cursor',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.FormatInt(r.Version, 10)); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// Public identifiers belong to the account, not its credentials. All primary
// writes preserve them, including imports and token rotation with stale data.
func (s *Store) prepareAccountCode(ctx context.Context, tx *sql.Tx, r *Record) error {
	var code string
	e := tx.QueryRowContext(ctx, "SELECT public_code FROM records WHERE kind='account' AND id=?", r.ID).Scan(&code)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	for code == "" {
		candidate := randomToken()[:10]
		var count int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='account' AND public_code=?", candidate).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			code = candidate
		}
	}
	var account Account
	if e = json.Unmarshal(r.Data, &account); e != nil {
		return e
	}
	account.PublicCode, r.PublicCode = code, code
	r.Data, e = json.Marshal(account)
	return e
}

func (s *Store) ensureAccountCodes(ctx context.Context) error {
	records, _, e := s.List(ctx, "kind='account' AND public_code=''", nil, 1_000_000, 0)
	if e != nil || len(records) == 0 {
		return e
	}
	// Normal writes publish migration events so existing replicas receive codes.
	return s.Put(ctx, records, nil)
}
func validateReferences(ctx context.Context, tx *sql.Tx, r Record) error {
	check := func(kind, id string) error {
		if id == "" {
			return nil
		}
		var n int
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind=? AND id=?", kind, id).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			return fmt.Errorf("%s does not exist", kind)
		}
		return nil
	}
	if r.Kind == "link" {
		if r.AccountID == "" {
			return errors.New("main mailbox is required")
		}
		if e := check("account", r.AccountID); e != nil {
			return e
		}
		var provider, email string
		if err := tx.QueryRowContext(ctx, "SELECT provider,email FROM records WHERE kind='account' AND id=?", r.AccountID).Scan(&provider, &email); err != nil {
			return err
		}
		if provider == "mx" {
			_, domain, _ := strings.Cut(email, "@")
			if !mxRecipient(Account{MXDomain: domain}, r.Recipient) {
				return errors.New("收件地址不属于此 MX 域名")
			}
		}
		return check("platform", r.PlatformID)
	}
	if r.Kind == "account" || r.Kind == "group" {
		return check("group", r.ParentID)
	}
	return nil
}
func (s *Store) upsert(ctx context.Context, tx *sql.Tx, r Record) error {
	enc, e := seal(s.key, r.Data)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(`+columns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET email=excluded.email,account_id=excluded.account_id,parent_id=excluded.parent_id,status=excluded.status,provider=excluded.provider,platform_id=excluded.platform_id,recipient=excluded.recipient,token_hash=excluded.token_hash,expires_at=excluded.expires_at,data=excluded.data,version=excluded.version,public_code=excluded.public_code`, r.Kind, r.ID, r.Email, r.AccountID, r.ParentID, r.Status, r.Provider, r.PlatformID, r.Recipient, r.TokenHash, r.ExpiresAt, enc, r.Version, r.PublicCode)
	return e
}
func (s *Store) Delete(ctx context.Context, kind string, ids []string) error {
	s.write.Lock()
	defer s.write.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, id := range ids {
		var n int
		switch kind {
		case "account":
			rows, err := tx.QueryContext(ctx, "SELECT id FROM records WHERE kind='link' AND account_id=? ORDER BY id", id)
			if err != nil {
				return err
			}
			var children []string
			for rows.Next() {
				var child string
				if err = rows.Scan(&child); err != nil {
					rows.Close()
					return err
				}
				children = append(children, child)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, child := range children {
				if err = s.deleteRecord(ctx, tx, "link", child); err != nil {
					return err
				}
			}
		case "group":
			e = tx.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind IN ('group','account') AND parent_id=?", id).Scan(&n)
		case "platform":
			e = tx.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='link' AND platform_id=?", id).Scan(&n)
		}
		if e != nil {
			return e
		}
		if n > 0 {
			return errors.New("record is still referenced; move or remove its children first")
		}
		if e = s.deleteRecord(ctx, tx, kind, id); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) deleteRecord(ctx context.Context, tx *sql.Tx, kind, id string) error {
	res, e := tx.ExecContext(ctx, "DELETE FROM records WHERE kind=? AND id=?", kind, id)
	if e != nil {
		return e
	}
	affected, e := res.RowsAffected()
	if e != nil || affected == 0 {
		return e
	}
	if kind == "link" {
		if _, e = tx.ExecContext(ctx, "DELETE FROM link_access WHERE link_id=?", id); e != nil {
			return e
		}
	}
	res, e = tx.ExecContext(ctx, "INSERT INTO events(data,created_at) VALUES('',?)", time.Now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return e
	}
	cursor, e := res.LastInsertId()
	if e != nil {
		return e
	}
	b, e := json.Marshal(Event{Cursor: cursor, Record: Record{Kind: kind, ID: id}, Deleted: true})
	if e != nil {
		return e
	}
	enc, e := seal(s.key, b)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE events SET data=? WHERE cursor=?", enc, cursor); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES('cursor',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.FormatInt(cursor, 10))
	return e
}
func (s *Store) Account(ctx context.Context, id string) (Account, error) {
	var a Account
	r, e := s.Get(ctx, "account", id)
	if e != nil {
		return a, e
	}
	e = json.Unmarshal(r.Data, &a)
	a.Version = r.Version
	return a, e
}
func (s *Store) Settings(ctx context.Context) Settings {
	v := DefaultSettings()
	if r, e := s.Get(ctx, "settings", "global"); e == nil {
		json.Unmarshal(r.Data, &v)
	}
	return v
}
func (s *Store) AuxPut(bucket, key string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	enc, e := seal(s.key, b)
	if e != nil {
		return e
	}
	_, e = s.db.Exec("INSERT INTO aux(bucket,key,data) VALUES(?,?,?) ON CONFLICT(bucket,key) DO UPDATE SET data=excluded.data", bucket, key, enc)
	return e
}
func (s *Store) AuxGet(bucket, key string, v any) error {
	var enc string
	e := s.db.QueryRow("SELECT data FROM aux WHERE bucket=? AND key=?", bucket, key).Scan(&enc)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	b, e := unseal(s.key, enc)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func (s *Store) AuxDelete(bucket, key string) error {
	_, e := s.db.Exec("DELETE FROM aux WHERE bucket=? AND key=?", bucket, key)
	return e
}
func (s *Store) AuxList(bucket string) ([]json.RawMessage, error) {
	rows, e := s.db.Query("SELECT data FROM aux WHERE bucket=? ORDER BY key DESC", bucket)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			return nil, e
		}
		b, e := unseal(s.key, v)
		if e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func replicaRecord(r Record) Record {
	if r.Kind == "account" {
		var a Account
		json.Unmarshal(r.Data, &a)
		a.RefreshToken = ""
		r.Data, _ = json.Marshal(a)
	}
	if r.Kind == "link" {
		var l Link
		json.Unmarshal(r.Data, &l)
		l.Token = ""
		r.Data, _ = json.Marshal(l)
	}
	return r
}
func (s *Store) Events(ctx context.Context, after int64, limit int) ([]Event, bool, error) {
	var first sql.NullInt64
	if e := s.db.QueryRowContext(ctx, "SELECT min(cursor) FROM events").Scan(&first); e != nil {
		return nil, false, e
	}
	if (first.Valid && after < first.Int64-1) || (!first.Valid && after < s.Cursor()) || after > s.Cursor() {
		return nil, true, nil
	}
	rows, e := s.db.QueryContext(ctx, "SELECT data FROM events WHERE cursor>? ORDER BY cursor LIMIT ?", after, limit)
	if e != nil {
		return nil, false, e
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var enc string
		if e = rows.Scan(&enc); e != nil {
			return nil, false, e
		}
		b, e := unseal(s.key, enc)
		if e != nil {
			return nil, false, e
		}
		var v Event
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, false, e
		}
		if !v.Deleted {
			v.Record = replicaRecord(v.Record)
		}
		out = append(out, v)
	}
	return out, false, rows.Err()
}
func (s *Store) Apply(ctx context.Context, events []Event, cursor int64) error {
	s.write.Lock()
	defer s.write.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	last := s.Cursor()
	for _, v := range events {
		if v.Cursor <= last {
			continue
		}
		if v.Cursor != last+1 {
			return errors.New("replication cursor gap")
		}
		if !validRecordKind(v.Record.Kind) {
			return errors.New("invalid replication entity")
		}
		if v.Deleted {
			_, e = tx.ExecContext(ctx, "DELETE FROM records WHERE kind=? AND id=?", v.Record.Kind, v.Record.ID)
			if e == nil && v.Record.Kind == "link" {
				_, e = tx.ExecContext(ctx, "DELETE FROM link_access WHERE link_id=?", v.Record.ID)
			}
		} else {
			e = s.upsert(ctx, tx, v.Record)
		}
		if e != nil {
			return e
		}
		last = v.Cursor
	}
	if last != cursor {
		return errors.New("replication cursor mismatch")
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES('cursor',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.FormatInt(last, 10))
	if e != nil {
		return e
	}
	return tx.Commit()
}
func validRecordKind(s string) bool {
	return s == "account" || s == "group" || s == "link" || s == "platform" || s == "settings"
}
func (s *Store) SnapshotFile(path string) error {
	s.write.Lock()
	defer s.write.Unlock()
	_, e := s.db.Exec("VACUUM INTO '" + strings.ReplaceAll(filepath.ToSlash(path), "'", "''") + "'")
	return e
}
func (s *Store) SnapshotPage(ctx context.Context, kind, id string) ([]Record, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT "+columns+" FROM records WHERE kind>? OR (kind=? AND id>?) ORDER BY kind,id LIMIT 1000", kind, kind, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, e := s.scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, replicaRecord(r))
	}
	return out, rows.Err()
}
func (s *Store) Stage(ctx context.Context, records []Record, progress any) error {
	s.write.Lock()
	defer s.write.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, r := range records {
		if !validRecordKind(r.Kind) {
			return errors.New("invalid snapshot entity")
		}
		if e = s.upsert(ctx, tx, r); e != nil {
			return e
		}
	}
	b, _ := json.Marshal(progress)
	_, e = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES('snapshot_progress',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(b))
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) InstallSnapshot(ctx context.Context, stage *Store, cursor int64) error {
	// Stage and active databases use the same local encryption key. ATTACH keeps
	// replacement transactional, so WAL readers see either complete version.
	s.write.Lock()
	defer s.write.Unlock()
	conn, e := s.db.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	_, e = conn.ExecContext(ctx, "ATTACH DATABASE ? AS incoming", stage.path)
	if e != nil {
		return e
	}
	defer conn.ExecContext(context.Background(), "DETACH DATABASE incoming")
	tx, e := conn.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "DELETE FROM records"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO records("+columns+") SELECT "+columns+" FROM incoming.records"); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES('cursor',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.FormatInt(cursor, 10))
	if e != nil {
		return e
	}
	return tx.Commit()
}
