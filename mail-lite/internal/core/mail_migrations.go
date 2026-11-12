package core

import (
	"fmt"
	"strings"
)

// Incremental upgrades also cover databases created by earlier preview builds.
func (a *App) mailColumn(table, name, definition string) error {
	rows, err := a.store.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var column, typ string
		var def any
		if err = rows.Scan(&cid, &column, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if column == name {
			found = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || found {
		return err
	}
	_, err = a.store.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + definition)
	return err
}

func (a *App) migrateMailOperations() error {
	if _, err := a.store.db.Exec(`CREATE TABLE IF NOT EXISTS mail_smtp_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),host TEXT NOT NULL DEFAULT '',port INTEGER NOT NULL DEFAULT 465,
 from_email TEXT NOT NULL DEFAULT '',security TEXT NOT NULL DEFAULT 'ssl',password_ciphertext TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL)`); err != nil {
		return err
	}
	columns := []struct{ table, name, definition string }{
		{"mailboxes", "raw_complete", "INTEGER NOT NULL DEFAULT 0 CHECK(raw_complete IN (0,1))"},
		{"mail_smtp_settings", "migrated_to_senders", "INTEGER NOT NULL DEFAULT 0"},
		{"mail_outbounds", "request_key", "TEXT NOT NULL DEFAULT ''"},
		{"mail_outbounds", "request_hash", "TEXT NOT NULL DEFAULT ''"},
		{"mail_outbounds", "selection", "TEXT NOT NULL DEFAULT 'random'"},
		{"mail_outbounds", "include_master", "INTEGER NOT NULL DEFAULT 0"},
		{"mail_outbounds", "download_disabled", "INTEGER NOT NULL DEFAULT 0"},
		{"mail_inspections", "options", "TEXT NOT NULL DEFAULT '{}'"},
		{"mail_inspections", "retry_info", "TEXT NOT NULL DEFAULT ''"},
		{"mail_inspection_results", "parent_email", "TEXT NOT NULL DEFAULT ''"},
		{"mail_inspection_results", "processed", "INTEGER NOT NULL DEFAULT 1"},
		{"mail_inspection_results", "test_token", "TEXT NOT NULL DEFAULT ''"},
		{"mail_inspection_results", "sent_at", "TEXT"},
		{"mail_inspection_results", "sender_email", "TEXT NOT NULL DEFAULT ''"},
		{"mail_inspection_results", "phase", "TEXT NOT NULL DEFAULT 'queued'"},
		{"mail_inspection_results", "started_at", "TEXT"},
	}
	for _, c := range columns {
		if err := a.mailColumn(c.table, c.name, c.definition); err != nil {
			return fmt.Errorf("mail migration: %w", err)
		}
	}
	var sqlText string
	if err := a.store.db.QueryRow("SELECT sql FROM sqlite_master WHERE name='mail_outbound_items'").Scan(&sqlText); err != nil {
		return err
	}
	if strings.Contains(strings.ToUpper(sqlText), "ON DELETE RESTRICT") {
		_, err := a.store.db.Exec(`BEGIN;
   CREATE TABLE mail_outbound_items_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT, outbound_id INTEGER NOT NULL REFERENCES mail_outbounds(id) ON DELETE CASCADE,
    mailbox_id INTEGER REFERENCES mailboxes(id) ON DELETE SET NULL,
    email TEXT NOT NULL,parent_email TEXT NOT NULL DEFAULT '',link TEXT NOT NULL DEFAULT '',returned_at TEXT);
   INSERT INTO mail_outbound_items_new SELECT * FROM mail_outbound_items;
   DROP TABLE mail_outbound_items;
   ALTER TABLE mail_outbound_items_new RENAME TO mail_outbound_items;
   CREATE INDEX mail_outbound_items_order ON mail_outbound_items(outbound_id);
   COMMIT;`)
		if err != nil {
			a.store.db.Exec("ROLLBACK")
			return err
		}
	}
	_, err := a.store.db.Exec(`
  CREATE TABLE IF NOT EXISTS mail_smtp_settings (
   id INTEGER PRIMARY KEY CHECK(id=1),host TEXT NOT NULL DEFAULT '',port INTEGER NOT NULL DEFAULT 465,
   from_email TEXT NOT NULL DEFAULT '',security TEXT NOT NULL DEFAULT 'ssl',password_ciphertext TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL);
  CREATE TABLE IF NOT EXISTS mail_smtp_senders (
   id INTEGER PRIMARY KEY AUTOINCREMENT,host TEXT NOT NULL,port INTEGER NOT NULL,from_email TEXT NOT NULL,
   security TEXT NOT NULL DEFAULT 'ssl',password_ciphertext TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,
   created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_used_at TEXT);
  CREATE INDEX IF NOT EXISTS mail_smtp_senders_enabled ON mail_smtp_senders(enabled,id);
  CREATE UNIQUE INDEX IF NOT EXISTS mail_smtp_senders_email ON mail_smtp_senders(from_email COLLATE NOCASE);
  INSERT INTO mail_smtp_senders(host,port,from_email,security,password_ciphertext,enabled,created_at,updated_at)
   SELECT host,port,from_email,security,password_ciphertext,1,updated_at,updated_at FROM mail_smtp_settings s
    WHERE s.id=1 AND s.migrated_to_senders=0 AND s.password_ciphertext<>'' AND NOT EXISTS(SELECT 1 FROM mail_smtp_senders n WHERE n.from_email=s.from_email COLLATE NOCASE);
  UPDATE mail_smtp_settings SET migrated_to_senders=1;
  CREATE TABLE IF NOT EXISTS mail_inspection_schedules (
   id INTEGER PRIMARY KEY CHECK(id=1),enabled INTEGER NOT NULL DEFAULT 0,
   run_at TEXT NOT NULL DEFAULT '02:00',scope TEXT NOT NULL DEFAULT 'all',folder_id INTEGER,
   sample INTEGER NOT NULL DEFAULT 1,include_sold INTEGER NOT NULL DEFAULT 0,
   options TEXT NOT NULL DEFAULT '{"timeout":10,"concurrency":5,"wait":60,"interval":1000}',
   last_run_date TEXT NOT NULL DEFAULT '',last_run_at TEXT,last_run_task_id INTEGER,last_run_status TEXT NOT NULL DEFAULT '',
   last_error TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL);
  INSERT OR IGNORE INTO mail_inspection_schedules(id,sample,updated_at) VALUES(1,1,strftime('%Y-%m-%dT%H:%M:%SZ','now'));
  CREATE UNIQUE INDEX IF NOT EXISTS mail_outbound_request ON mail_outbounds(request_key) WHERE request_key<>'';
  CREATE INDEX IF NOT EXISTS mail_outbound_mailbox ON mail_outbound_items(mailbox_id);
  CREATE INDEX IF NOT EXISTS mail_inspection_mailbox ON mail_inspection_results(mailbox_id,id DESC);
  CREATE TABLE IF NOT EXISTS mail_outbound_masters (
   outbound_id INTEGER NOT NULL REFERENCES mail_outbounds(id) ON DELETE CASCADE,
   master_id INTEGER REFERENCES mailboxes(id) ON DELETE SET NULL,email TEXT NOT NULL,raw TEXT NOT NULL,
   position INTEGER NOT NULL,PRIMARY KEY(outbound_id,position));
  CREATE TABLE IF NOT EXISTS mail_outbound_history (
   id INTEGER PRIMARY KEY AUTOINCREMENT,outbound_id INTEGER NOT NULL REFERENCES mail_outbounds(id) ON DELETE CASCADE,
   actor TEXT NOT NULL,action TEXT NOT NULL,reason TEXT NOT NULL,count INTEGER NOT NULL,created_at TEXT NOT NULL);
  UPDATE mail_inspections SET status='failed',finished_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE status IN ('queued','running');
  UPDATE mail_inspection_results SET message='服务重启中断；可复检，收信验证不会重新发信',phase='interrupted'
   WHERE processed=0 AND inspection_id IN (SELECT id FROM mail_inspections WHERE status='failed');
  UPDATE mail_inspection_results SET phase='finished' WHERE processed=1;`)
	if err != nil {
		return err
	}
	if err := a.mailColumn("mail_inspection_schedules", "last_run_at", "TEXT"); err != nil {
		return err
	}
	return a.migrateMailRawEncryption()
}

// Old inventory previews stored a single-line mailbox record directly. The
// base64url ciphertext cannot contain @, so only valid mailbox rows can be
// accepted as legacy plaintext after authenticated decryption fails.
func (a *App) mailMasterRaw(value string) (string, error) {
	if plain, err := unseal(a.key, value); err == nil {
		return string(plain), nil
	}
	if value == "" || len(value) <= 65536 && !strings.ContainsAny(value, "\r\n") && address(strings.SplitN(value, "----", 2)[0]) != "" {
		return value, nil
	}
	return "", fmt.Errorf("invalid encrypted mailbox material")
}

func (a *App) migrateMailRawEncryption() error {
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type material struct {
		id, position int64
		raw          string
	}
	readMaterials := func(query string) ([]material, error) {
		rows, err := tx.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []material{}
		for rows.Next() {
			var item material
			if err := rows.Scan(&item.id, &item.position, &item.raw); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, rows.Err()
	}
	masters, err := readMaterials("SELECT id,0,raw FROM mailboxes WHERE kind='master'")
	if err != nil {
		return err
	}
	snapshots, err := readMaterials("SELECT outbound_id,position,raw FROM mail_outbound_masters")
	if err != nil {
		return err
	}
	encrypt := func(value string) (string, string, error) {
		plain, err := a.mailMasterRaw(value)
		if err != nil {
			return "", "", err
		}
		if _, err := unseal(a.key, value); err == nil {
			return plain, value, nil
		}
		ciphertext, err := seal(a.key, []byte(plain))
		return plain, ciphertext, err
	}
	for _, item := range masters {
		plain, ciphertext, err := encrypt(item.raw)
		if err != nil {
			return fmt.Errorf("encrypt mailbox %d: %w", item.id, err)
		}
		if _, err := tx.Exec("UPDATE mailboxes SET raw=?,raw_complete=? WHERE id=?", ciphertext, completeRaw(plain), item.id); err != nil {
			return err
		}
	}
	for _, item := range snapshots {
		_, ciphertext, err := encrypt(item.raw)
		if err != nil {
			return fmt.Errorf("encrypt outbound %d material: %w", item.id, err)
		}
		if _, err := tx.Exec("UPDATE mail_outbound_masters SET raw=? WHERE outbound_id=? AND position=?", ciphertext, item.id, item.position); err != nil {
			return err
		}
	}
	return tx.Commit()
}
