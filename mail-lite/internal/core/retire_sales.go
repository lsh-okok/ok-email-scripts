package core

// Discard the retired sales history once while keeping the mailbox/link
// mappings, inspection results and SMTP settings used by mailbox verification.
func (a *App) retireSalesHistory() error {
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done int
	if err = tx.QueryRow("SELECT count(*) FROM meta WHERE key='sales_retired_v1'").Scan(&done); err != nil || done > 0 {
		return err
	}
	for _, statement := range []string{
		"DELETE FROM mail_outbounds",
		"DELETE FROM mail_import_previews",
		"DELETE FROM mail_activities",
		"DELETE FROM mail_history WHERE action LIKE 'mail.outbound.%' OR action LIKE 'mail.status.%'",
		"UPDATE mailboxes SET status='available',after_sale=0 WHERE status<>'available' OR after_sale<>0",
		"UPDATE mail_inspection_schedules SET include_sold=0 WHERE include_sold<>0",
		"INSERT INTO meta(key,value) VALUES('sales_retired_v1','1')",
	} {
		if _, err = tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}
