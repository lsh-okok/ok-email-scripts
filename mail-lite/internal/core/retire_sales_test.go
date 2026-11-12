package core

import (
	"context"
	"testing"
)

func TestRetireSalesKeepsMailboxVerification(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "kept-main")
	linkFixture(t, a, "kept-main", "kept-child@example.net")
	_, err := a.store.db.Exec(`
 DELETE FROM meta WHERE key='sales_retired_v1';
 INSERT INTO mail_folders(id,name,created_at) VALUES(7,'verification group','now');
 UPDATE mail_inspection_schedules SET enabled=1,scope='folder',folder_id=7,run_at='09:30',include_sold=1;
 INSERT INTO mailboxes(id,email,kind,status,after_sale,created_at,updated_at) VALUES(1,'verify@example.net','master','sold',1,'now','now');
 INSERT INTO mail_inspections(id,mode,scope,status,actor,created_at) VALUES(1,'link','all','completed','admin','now');
 INSERT INTO mail_inspection_results(inspection_id,mailbox_id,email,result,checked_at) VALUES(1,1,'verify@example.net','passed','now');
 INSERT INTO mail_outbounds(id,order_no,mode,status,actor,created_at) VALUES(1,'old-order','sell','sold','admin','now');
 INSERT INTO mail_outbound_items(outbound_id,mailbox_id,email) VALUES(1,1,'verify@example.net');
 INSERT INTO mail_outbound_masters(outbound_id,master_id,email,raw,position) VALUES(1,1,'verify@example.net','old-material',0);
 INSERT INTO mail_outbound_history(outbound_id,actor,action,reason,count,created_at) VALUES(1,'admin','sold','',1,'now');`)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.retireSalesHistory(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"mail_outbounds", "mail_outbound_items", "mail_outbound_masters", "mail_outbound_history"} {
		var count int
		if err := a.store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: count %d, error %v", table, count, err)
		}
	}
	var status string
	var afterSale, resultCount int
	if err = a.store.db.QueryRow("SELECT status,after_sale FROM mailboxes WHERE id=1").Scan(&status, &afterSale); err != nil || status != "available" || afterSale != 0 {
		t.Fatalf("verification mailbox not restored: %s %d %v", status, afterSale, err)
	}
	if err = a.store.db.QueryRow("SELECT count(*) FROM mail_inspection_results WHERE result='passed'").Scan(&resultCount); err != nil || resultCount != 1 {
		t.Fatalf("inspection results were removed: %d %v", resultCount, err)
	}
	if _, err = a.store.Account(context.Background(), "kept-main"); err != nil {
		t.Fatal("source mailbox removed", err)
	}
	plan, err := a.inspectionSchedule()
	if err != nil || !plan.Enabled || plan.Scope != "folder" || plan.FolderID != 7 || plan.RunAt != "09:30" || plan.IncludeSold {
		t.Fatalf("verification schedule changed its configured scope: %+v %v", plan, err)
	}
	// The cleanup is an upgrade, not a recurring operation on verification data.
	if _, err = a.store.db.Exec("UPDATE mailboxes SET after_sale=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err = a.retireSalesHistory(); err != nil {
		t.Fatal(err)
	}
	if err = a.store.db.QueryRow("SELECT after_sale FROM mailboxes WHERE id=1").Scan(&afterSale); err != nil || afterSale != 1 {
		t.Fatalf("cleanup ran twice: %d %v", afterSale, err)
	}
}
