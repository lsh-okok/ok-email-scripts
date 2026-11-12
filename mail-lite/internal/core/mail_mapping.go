package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func stampNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// mail_source_mappings records the one-time snapshot relationship between the
// parent project's records table and the sales inventory. Keeping this mapping
// separate makes a repeat of the import idempotent and leaves later edits to a
// parent account independent from the sales snapshot.
func (a *App) migrateMailMapping() error {
	_, err := a.store.db.Exec(`
CREATE TABLE IF NOT EXISTS mail_source_mappings (
	 source_kind TEXT NOT NULL CHECK(source_kind IN ('account','link')),
	 source_id TEXT NOT NULL,
	 mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
	 source_email TEXT NOT NULL DEFAULT '',
	 created_at TEXT NOT NULL,
	 PRIMARY KEY(source_kind,source_id));
CREATE UNIQUE INDEX IF NOT EXISTS mail_source_mailbox ON mail_source_mappings(mailbox_id);
CREATE INDEX IF NOT EXISTS mail_source_kind_email ON mail_source_mappings(source_kind,source_email COLLATE NOCASE);`)
	return err
}

type mailMappingAccount struct {
	SourceID        string `json:"source_id"`
	Email           string `json:"email"`
	MailboxID       int64  `json:"mailbox_id"`
	Mapped          bool   `json:"mapped"`
	NeedsEnrichment bool   `json:"needs_enrichment"`
	Conflict        string `json:"conflict,omitempty"`
}

type mailMappingLink struct {
	SourceID           string `json:"source_id"`
	AccountID          string `json:"account_id"`
	Recipient          string `json:"recipient"`
	MailboxID          int64  `json:"mailbox_id"`
	ParentID           int64  `json:"parent_id"`
	Mapped             bool   `json:"mapped"`
	NeedsDetectionLink bool   `json:"needs_detection_link"`
	LinkReason         string `json:"link_reason,omitempty"`
	Conflict           string `json:"conflict,omitempty"`
}

type mailMappingPreview struct {
	Accounts []mailMappingAccount `json:"accounts"`
	Links    []mailMappingLink    `json:"links"`
	Page     int                  `json:"page"`
	Limit    int                  `json:"limit"`
	Total    int                  `json:"total"`
	Summary  struct {
		ActiveAccounts int `json:"active_accounts"`
		Links          int `json:"links"`
		MappedAccounts int `json:"mapped_accounts"`
		MappedLinks    int `json:"mapped_links"`
		Conflicts      int `json:"conflicts"`
	} `json:"summary"`
}

type mailMappingStatus struct {
	mailboxID int64
	complete  bool
	source    bool
	emptyLink bool
}

type mailMappingInventory struct {
	emails   map[string]bool
	accounts map[string]mailMappingStatus
	links    map[string]mailMappingStatus
	reasons  map[int64]string
}

// Match SQLite's ASCII-only NOCASE collation for existing inventory. Proposed
// source addresses still use strings.ToLower, as in the commit preview rules.
func mailMappingEmailKey(email string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, email)
}

func (a *App) mailMappingInventory(ctx context.Context) (mailMappingInventory, error) {
	out := mailMappingInventory{
		emails: map[string]bool{}, accounts: map[string]mailMappingStatus{},
		links: map[string]mailMappingStatus{}, reasons: map[int64]string{},
	}
	rows, err := a.store.db.QueryContext(ctx, `SELECT m.id,m.email,m.raw_complete,m.link_mode='source',m.link='',
 COALESCE(sm.source_kind,''),COALESCE(sm.source_id,'')
 FROM mailboxes m LEFT JOIN mail_source_mappings sm ON sm.mailbox_id=m.id`)
	if err != nil {
		return out, err
	}
	hasSource := false
	for rows.Next() {
		var status mailMappingStatus
		var email, kind, id string
		if err := rows.Scan(&status.mailboxID, &email, &status.complete, &status.source, &status.emptyLink, &kind, &id); err != nil {
			rows.Close()
			return out, err
		}
		out.emails[mailMappingEmailKey(email)] = true
		switch kind {
		case "account":
			out.accounts[id] = status
		case "link":
			out.links[id] = status
			hasSource = hasSource || status.source
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !hasSource {
		return out, err
	}
	// Read the view once and join by mailbox ID in memory. Joining the view to
	// every source row can materialize it and repeatedly scan the entire result.
	rows, err = a.store.db.QueryContext(ctx, "SELECT mailbox_id,reason FROM mail_source_links")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var reason string
		if err := rows.Scan(&id, &reason); err != nil {
			return out, err
		}
		out.reasons[id] = reason
	}
	return out, rows.Err()
}

func (a *App) activeSourceRecords(ctx context.Context) ([]Record, map[string]Account, error) {
	records, _, err := a.store.List(ctx, "kind='account' AND status='active'", nil, 1000000, 0)
	if err != nil {
		return nil, nil, err
	}
	accounts := make(map[string]Account, len(records))
	for _, record := range records {
		var account Account
		if err := json.Unmarshal(record.Data, &account); err != nil {
			return nil, nil, fmt.Errorf("decode account %s: %w", record.ID, err)
		}
		// The records table primary key is authoritative. Older imports may
		// contain a stale JSON id, while links always refer to this primary key.
		account.ID = record.ID
		if account.Status != "active" {
			continue
		}
		accounts[account.ID] = account
	}
	return records, accounts, nil
}

func (a *App) mailMappingPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, accounts, err := a.activeSourceRecords(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	inventory, err := a.mailMappingInventory(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	out := mailMappingPreview{Accounts: make([]mailMappingAccount, 0, len(accounts)), Links: []mailMappingLink{}}
	accountIDs := make([]string, 0, len(accounts))
	for id := range accounts {
		accountIDs = append(accountIDs, id)
	}
	sort.Strings(accountIDs)
	// Reserve every active parent before processing children, including parents
	// later in the sort order. Pagination must not change duplicate detection.
	claimed := map[string]bool{}
	parentConflicts := map[string]bool{}
	for _, id := range accountIDs {
		claimed[strings.ToLower(accounts[id].Email)] = true
	}
	for _, id := range accountIDs {
		account := accounts[id]
		status, mapped := inventory.accounts[id]
		complete := completeRaw(mailAccountRaw(account))
		if mapped {
			complete = status.complete
			out.Summary.MappedAccounts++
		}
		conflict := ""
		if !mapped && inventory.emails[mailMappingEmailKey(account.Email)] {
			conflict = "验证范围已存在相同邮箱"
			parentConflicts[id] = true
			out.Summary.Conflicts++
		}
		out.Accounts = append(out.Accounts, mailMappingAccount{SourceID: id, Email: account.Email, MailboxID: status.mailboxID, Mapped: mapped, NeedsEnrichment: !complete, Conflict: conflict})
		out.Summary.ActiveAccounts++
	}
	// Recipient and account ownership are indexed metadata, maintained atomically
	// with the encrypted payload. Preview needs no link secrets or decryption.
	rows, err := a.store.db.QueryContext(ctx, `SELECT l.id,l.account_id,l.recipient
 FROM records l JOIN records p ON p.kind='account' AND p.id=l.account_id
 WHERE l.kind='link' AND p.status='active' ORDER BY l.account_id,l.id`)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	issuesOnly := r.URL.Query().Get("issues_only") == "1"
	for rows.Next() {
		var id, accountID, recipient string
		if err := rows.Scan(&id, &accountID, &recipient); err != nil {
			fail(w, err)
			return
		}
		if _, active := accounts[accountID]; !active || strings.TrimSpace(recipient) == "" {
			continue
		}
		status, mapped := inventory.links[id]
		item := mailMappingLink{SourceID: id, AccountID: accountID, Recipient: recipient, MailboxID: status.mailboxID, Mapped: mapped, NeedsDetectionLink: true}
		if mapped {
			item.ParentID = inventory.accounts[accountID].mailboxID
			item.NeedsDetectionLink = status.emptyLink
			if status.source {
				reason, found := inventory.reasons[status.mailboxID]
				item.LinkReason = reason
				item.NeedsDetectionLink = !found || reason != ""
			}
			out.Summary.MappedLinks++
		} else {
			if parentConflicts[accountID] {
				item.Conflict = "主邮箱存在映射冲突"
			} else if inventory.emails[mailMappingEmailKey(recipient)] || claimed[strings.ToLower(recipient)] {
				item.Conflict = "验证范围或其他来源存在相同邮箱"
			}
			if item.Conflict != "" {
				out.Summary.Conflicts++
			}
		}
		claimed[strings.ToLower(recipient)] = true
		out.Summary.Links++
		if !issuesOnly || item.Conflict != "" || item.NeedsDetectionLink {
			out.Links = append(out.Links, item)
		}
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	out.Total = len(out.Links)
	out.Page, out.Limit = 1, out.Total
	params := r.URL.Query()
	if params.Has("page") || params.Has("limit") {
		page, err := strconv.Atoi(params.Get("page"))
		if err != nil {
			page = 1
		}
		limit, err := strconv.Atoi(params.Get("limit"))
		if err != nil {
			limit = 50
		}
		limit = max(1, min(200, limit))
		page = max(1, min(max(1, (out.Total+limit-1)/limit), page))
		offset := (page - 1) * limit
		out.Page, out.Limit = page, limit
		out.Links = out.Links[offset:min(offset+limit, out.Total)]
	}
	respond(w, http.StatusOK, out)
}

type mailMappingCommitRequest struct {
	AccountIDs   []string `json:"account_ids"`
	IncludeLinks *bool    `json:"include_links"`
}

type mailMappingCommitResponse struct {
	CreatedAccounts     int                   `json:"created_accounts"`
	CreatedLinks        int                   `json:"created_links"`
	AssociatedLinks     int                   `json:"associated_links"`
	SkippedAccounts     int                   `json:"skipped_accounts"`
	SkippedLinks        int                   `json:"skipped_links"`
	NeedsDetectionLinks int                   `json:"needs_detection_links"`
	Conflicts           []mailMappingConflict `json:"conflicts"`
}

type mailMappingConflict struct {
	SourceKind string `json:"source_kind"`
	SourceID   string `json:"source_id"`
	Email      string `json:"email"`
	Reason     string `json:"reason"`
}

// A snapshot preserves the same credential order as the parent TXT export.
// Public mailbox links are associated by source identity, never copied from the transient URL field.
func mailAccountRaw(account Account) string {
	if account.Provider == "mx" {
		return fmt.Sprintf("%s----MX----%d", account.Email, mxDays(account))
	}
	if account.Provider == "outlook" {
		return account.Email + "----" + account.Password + "----" + account.ClientID + "----" + account.RefreshToken
	}
	return account.Email + "----" + account.Password
}

func (a *App) mailMappingCommit(w http.ResponseWriter, r *http.Request) {
	var req mailMappingCommitRequest
	if !readJSON(w, r, &req) {
		return
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	result, err := a.commitMailMappingLocked(r.Context(), req, a.baseURL(r))
	if err != nil {
		mailError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

// The caller holds store.write. Scheduled and manual verification share this
// synchronization so newly imported accounts and links enter the next run.
func (a *App) commitMailMappingLocked(ctx context.Context, req mailMappingCommitRequest, base string) (mailMappingCommitResponse, error) {
	result := mailMappingCommitResponse{Conflicts: []mailMappingConflict{}}
	includeLinks := true
	if req.IncludeLinks != nil {
		includeLinks = *req.IncludeLinks
	}
	_, accounts, err := a.activeSourceRecords(ctx)
	if err != nil {
		return result, err
	}
	selected := make(map[string]bool, len(accounts))
	if len(req.AccountIDs) == 0 {
		for id := range accounts {
			selected[id] = true
		}
	} else {
		for _, id := range req.AccountIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := accounts[id]; !ok {
				return result, badMail("只能映射 active 状态的账号")
			}
			selected[id] = true
		}
	}
	if len(req.AccountIDs) > 0 && len(selected) == 0 {
		return result, badMail("请选择有效的账号 ID")
	}
	selectedIDs := make([]string, 0, len(selected))
	for id := range selected {
		selectedIDs = append(selectedIDs, id)
	}
	sort.Strings(selectedIDs)

	linkRecords := make(map[string][]Record, len(selected))
	if includeLinks {
		for id := range selected {
			links, _, err := a.store.List(ctx, "kind='link' AND account_id=?", []any{id}, 1000000, 0)
			if err != nil {
				return result, err
			}
			linkRecords[id] = links
		}
	}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	parentIDs := make(map[string]int64, len(selected))
	for _, id := range selectedIDs {
		account := accounts[id]
		var mailboxID int64
		lookupErr := tx.QueryRowContext(ctx, `SELECT mailbox_id FROM mail_source_mappings WHERE source_kind='account' AND source_id=?`, id).Scan(&mailboxID)
		if lookupErr == nil {
			result.SkippedAccounts++
			parentIDs[id] = mailboxID
			continue
		}
		if lookupErr != sql.ErrNoRows {
			return result, lookupErr
		}
		var existing int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM mailboxes WHERE email=? COLLATE NOCASE", account.Email).Scan(&existing); err != nil {
			return result, err
		}
		if existing > 0 {
			result.SkippedAccounts++
			result.Conflicts = append(result.Conflicts, mailMappingConflict{SourceKind: "account", SourceID: id, Email: account.Email, Reason: "验证范围已存在相同邮箱"})
			continue
		}
		raw := mailAccountRaw(account)
		ciphertext, err := seal(a.key, []byte(raw))
		if err != nil {
			return result, err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO mailboxes(email,kind,raw,raw_complete,note,status,position,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, account.Email, "master", ciphertext, completeRaw(raw), "source account:"+id, "available", 0, now, now)
		if err != nil {
			return result, err
		}
		mailboxID, err = res.LastInsertId()
		if err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail_source_mappings(source_kind,source_id,mailbox_id,source_email,created_at) VALUES('account',?,?,?,?)`, id, mailboxID, account.Email, now); err != nil {
			return result, err
		}
		parentIDs[id] = mailboxID
		if err := mailHistory(tx, mailboxID, "admin", "mail.mapping.created", "主邮箱快照映射"); err != nil {
			return result, err
		}
		result.CreatedAccounts++
	}

	if includeLinks {
		for _, id := range selectedIDs {
			for _, record := range linkRecords[id] {
				var link Link
				if err := json.Unmarshal(record.Data, &link); err != nil {
					return result, fmt.Errorf("decode link %s: %w", record.ID, err)
				}
				if strings.TrimSpace(link.Recipient) == "" {
					continue
				}
				var mappedID int64
				lookupErr := tx.QueryRowContext(ctx, `SELECT mailbox_id FROM mail_source_mappings WHERE source_kind='link' AND source_id=?`, record.ID).Scan(&mappedID)
				if lookupErr == nil {
					result.SkippedLinks++
					var eligible bool
					if err := tx.QueryRowContext(ctx, "SELECT status='available' AND link='' AND link_mode='custom' FROM mailboxes WHERE id=?", mappedID).Scan(&eligible); err != nil {
						return result, err
					}
					if eligible {
						source, err := a.sourceLink(ctx, tx, mappedID, base, true)
						if err != nil {
							return result, err
						}
						if source.LinkAvailable {
							if _, err = tx.ExecContext(ctx, "UPDATE mailboxes SET link_mode='source',link_revision=link_revision+1,updated_at=? WHERE id=?", now, mappedID); err != nil {
								return result, err
							}
							if err = mailHistory(tx, mappedID, "system", "mail.link.associated", "验证范围自动关联现有取信链接"); err != nil {
								return result, err
							}
							result.AssociatedLinks++
						} else {
							result.NeedsDetectionLinks++
						}
					}
					continue
				}
				if lookupErr != sql.ErrNoRows {
					return result, lookupErr
				}
				parentID := parentIDs[id]
				var existing int
				if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM mailboxes WHERE email=? COLLATE NOCASE", link.Recipient).Scan(&existing); err != nil {
					return result, err
				}
				if parentID == 0 || existing > 0 {
					reason := "验证范围或其他来源存在相同邮箱"
					if parentID == 0 {
						reason = "主邮箱存在映射冲突"
					}
					result.SkippedLinks++
					result.Conflicts = append(result.Conflicts, mailMappingConflict{SourceKind: "link", SourceID: record.ID, Email: link.Recipient, Reason: reason})
					continue
				}
				res, err := tx.ExecContext(ctx, `INSERT INTO mailboxes(email,kind,parent_id,raw,note,link,status,position,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, link.Recipient, "child", parentID, "", "source link:"+record.ID, "", "available", 0, now, now)
				if err != nil {
					return result, err
				}
				childID, err := res.LastInsertId()
				if err != nil {
					return result, err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO mail_source_mappings(source_kind,source_id,mailbox_id,source_email,created_at) VALUES('link',?,?,?,?)`, record.ID, childID, link.Recipient, now); err != nil {
					return result, err
				}
				result.CreatedLinks++
				if _, err = tx.ExecContext(ctx, "UPDATE mailboxes SET link_mode='source',link_revision=link_revision+1 WHERE id=?", childID); err != nil {
					return result, err
				}
				source, e := a.sourceLink(ctx, tx, childID, base, true)
				if e != nil {
					return result, e
				}
				if !source.LinkAvailable {
					result.NeedsDetectionLinks++
				}
				if err := mailHistory(tx, childID, "admin", "mail.mapping.created", "子邮箱快照映射；自动关联取信链接"); err != nil {
					return result, err
				}
			}
		}
	}
	if associated := result.CreatedLinks + result.AssociatedLinks; associated > 0 {
		if err := recordMailActivity(ctx, tx, MailActivity{Action: "mail.links.associated", Scope: "child", SelectionCount: associated, ChildCount: associated}); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func (a *App) mailSalesStats(w http.ResponseWriter, r *http.Request) {
	stats, err := readMailStats(a.store.db)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, stats)
}
