package core

import (
	"context"
	"database/sql"
	"net/http"
)

// MailActivity stores operation counts independently of inventory lifetime.
// Credentials, mailbox addresses, links and exported content are never included.
type MailActivity struct {
	ID             int64  `json:"id"`
	Actor          string `json:"actor"`
	Action         string `json:"action"`
	Scope          string `json:"scope"`
	Format         string `json:"format"`
	SelectionCount int    `json:"selection_count"`
	MasterCount    int    `json:"master_count"`
	ChildCount     int    `json:"child_count"`
	CreatedAt      string `json:"created_at"`
}

func recordMailActivity(ctx context.Context, tx *sql.Tx, activity MailActivity) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO mail_activities
	 (actor,action,scope,format,selection_count,master_count,child_count,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		"admin", activity.Action, activity.Scope, activity.Format, activity.SelectionCount,
		activity.MasterCount, activity.ChildCount, stampNow())
	return err
}

func (a *App) mailActivities(w http.ResponseWriter, r *http.Request) {
	page, limit := mailInventoryPaging(r)
	var total int
	if err := a.store.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM mail_activities").Scan(&total); err != nil {
		fail(w, err)
		return
	}
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT id,actor,action,scope,format,selection_count,master_count,child_count,created_at
	 FROM mail_activities ORDER BY id DESC LIMIT ? OFFSET ?`, limit, (page-1)*limit)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	items := []MailActivity{}
	for rows.Next() {
		var activity MailActivity
		if err := rows.Scan(&activity.ID, &activity.Actor, &activity.Action, &activity.Scope, &activity.Format,
			&activity.SelectionCount, &activity.MasterCount, &activity.ChildCount, &activity.CreatedAt); err != nil {
			fail(w, err)
			return
		}
		items = append(items, activity)
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "limit": limit})
}
