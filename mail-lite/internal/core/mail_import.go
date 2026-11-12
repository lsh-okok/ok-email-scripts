package core

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const mailImportLimit = 20 << 20

type importFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}
type importChild struct {
	Email string `json:"email"`
	Link  string `json:"link"`
	Note  string `json:"note"`
}
type importGroup struct {
	Email    string        `json:"email"`
	Raw      string        `json:"raw"`
	Note     string        `json:"note"`
	Source   string        `json:"source"`
	Children []importChild `json:"children"`
	Invalid  int           `json:"invalid"`
}
type importIssue struct {
	Source string `json:"source"`
	Email  string `json:"email"`
	Reason string `json:"reason"`
}
type importReport struct {
	AddedMasters     int                 `json:"added_masters"`
	AddedChildren    int                 `json:"added_children"`
	Duplicates       int                 `json:"duplicates"`
	Conflicts        int                 `json:"conflicts"`
	Invalid          int                 `json:"invalid"`
	Restored         int                 `json:"restored"`
	Completed        int                 `json:"completed"`
	Groups           []importGroupReport `json:"groups"`
	Issues           []importIssue       `json:"issues"`
	affectedMasters  int
	affectedChildren int
}
type importGroupReport struct {
	Email   string `json:"email"`
	Source  string `json:"source"`
	Added   int    `json:"added"`
	Skipped int    `json:"skipped"`
}
type importPayload struct {
	Groups  []importGroup `json:"groups"`
	Invalid int           `json:"invalid"`
	Issues  []importIssue `json:"issues"`
}

func (p *importPayload) invalid(source, email, reason string) {
	p.Invalid++
	if len(p.Issues) < 100 {
		p.Issues = append(p.Issues, importIssue{Source: source, Email: email, Reason: reason})
	}
}

func normalizedEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// parseMailImport accepts the same three inventory formats as go-version.
// TXT files contain a master row followed by child email----link rows. CSV and
// JSON use email,parent_email,link,note and optional raw columns.
func parseMailImport(files []importFile) (importPayload, error) {
	p := importPayload{Groups: []importGroup{}, Issues: []importIssue{}}
	if len(files) == 0 || len(files) > 500 {
		return p, errors.New("请选择 1–500 个 TXT、CSV 或 JSON 文件")
	}
	total, records := 0, 0
	for _, file := range files {
		total += len(file.Content)
		if total > mailImportLimit {
			return p, errors.New("导入内容总大小不能超过 20 MB")
		}
		if len(file.Name) > 255 {
			return p, errors.New("文件名称过长")
		}
		content := strings.TrimPrefix(file.Content, "\ufeff")
		source := filepath.Base(file.Name)
		ext := strings.ToLower(filepath.Ext(file.Name))
		switch ext {
		case ".txt":
			lines := []string{}
			for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					lines = append(lines, line)
				}
			}
			if len(lines) == 0 {
				p.invalid(source, "", "文件没有内容")
				continue
			}
			email := normalizedEmail(strings.Split(lines[0], "----")[0])
			if address(email) == "" || len(lines[0]) > 65536 || strings.ContainsAny(lines[0], "\r\n") {
				p.invalid(source, email, "首行主邮箱资料无效")
				continue
			}
			g := importGroup{Email: email, Raw: lines[0], Source: source, Children: []importChild{}}
			for _, line := range lines[1:] {
				parts := strings.SplitN(line, "----", 2)
				child := normalizedEmail(parts[0])
				records++
				if len(parts) != 2 || address(child) == "" || !validLink(strings.TrimSpace(parts[1])) {
					p.invalid(source, child, "子邮箱或 HTTP(S) 接码链接无效")
					g.Invalid++
					continue
				}
				g.Children = append(g.Children, importChild{Email: child, Link: strings.TrimSpace(parts[1])})
			}
			p.Groups = append(p.Groups, g)
		case ".csv", ".json":
			type record struct {
				Email  string `json:"email"`
				Parent string `json:"parent_email"`
				Link   string `json:"link"`
				Note   string `json:"note"`
				Raw    string `json:"raw"`
			}
			list := []record{}
			if ext == ".json" {
				if e := json.Unmarshal([]byte(content), &list); e != nil || list == nil {
					return p, fmt.Errorf("%s：JSON 必须为邮箱对象数组", source)
				}
			} else {
				cr := csv.NewReader(strings.NewReader(content))
				header, e := cr.Read()
				if e != nil {
					return p, fmt.Errorf("%s：CSV 表头无法读取", source)
				}
				columns := map[string]int{}
				for i, v := range header {
					columns[strings.ToLower(strings.TrimSpace(v))] = i
				}
				for _, key := range []string{"email", "parent_email", "link"} {
					if _, ok := columns[key]; !ok {
						return p, fmt.Errorf("%s：CSV 缺少 %s 列", source, key)
					}
				}
				for {
					row, e := cr.Read()
					if e == io.EOF {
						break
					}
					if e != nil {
						return p, fmt.Errorf("%s：CSV 行格式不正确", source)
					}
					value := func(k string) string {
						if i, ok := columns[k]; ok && i < len(row) {
							return row[i]
						}
						return ""
					}
					list = append(list, record{Email: value("email"), Parent: value("parent_email"), Link: value("link"), Note: value("note"), Raw: value("raw")})
				}
			}
			groups := map[string]int{}
			for _, row := range list {
				records++
				email, parent := normalizedEmail(row.Email), normalizedEmail(row.Parent)
				link, note := strings.TrimSpace(row.Link), strings.TrimSpace(row.Note)
				if address(email) == "" || len([]rune(note)) > 1000 {
					p.invalid(source, email, "邮箱或备注无效")
					continue
				}
				if parent == "" {
					raw := strings.TrimSpace(row.Raw)
					if raw == "" {
						raw = email
					}
					if normalizedEmail(strings.Split(raw, "----")[0]) != email || len(raw) > 65536 || strings.ContainsAny(raw, "\r\n") {
						p.invalid(source, email, "主邮箱原始资料无效")
						continue
					}
					if index, ok := groups[email]; ok {
						p.Groups[index].Raw, p.Groups[index].Note = raw, note
					} else {
						groups[email] = len(p.Groups)
						p.Groups = append(p.Groups, importGroup{Email: email, Raw: raw, Note: note, Source: source, Children: []importChild{}})
					}
					continue
				}
				if address(parent) == "" || !validLink(link) {
					p.invalid(source, email, "主邮箱或 HTTP(S) 接码链接无效")
					continue
				}
				index, ok := groups[parent]
				if !ok {
					index = len(p.Groups)
					groups[parent] = index
					p.Groups = append(p.Groups, importGroup{Email: parent, Raw: parent, Source: source, Children: []importChild{}})
				}
				p.Groups[index].Children = append(p.Groups[index].Children, importChild{Email: email, Link: link, Note: note})
			}
		default:
			return p, fmt.Errorf("%s：只支持 TXT、CSV、JSON 文件", source)
		}
		if records > 100000 || len(p.Groups) > 10000 {
			return p, errors.New("单次最多导入 10000 组、100000 条子邮箱")
		}
	}
	return p, nil
}

// importMail applies one payload. Preview calls it in a transaction that is
// rolled back; commit calls it in the same write transaction that consumes the
// preview token.
func (a *App) importMail(tx *sql.Tx, p importPayload, folderID int64, actor string) (importReport, error) {
	out := importReport{Invalid: p.Invalid, Issues: append([]importIssue(nil), p.Issues...), Groups: []importGroupReport{}}
	changedMasters, changedChildren := map[int64]bool{}, map[int64]bool{}
	if err := mailCheckFolder(contextBackground(), tx, folderID); err != nil {
		return out, err
	}
	issue := func(source, email, reason string) {
		if len(out.Issues) < 100 {
			out.Issues = append(out.Issues, importIssue{Source: source, Email: email, Reason: reason})
		}
	}
	for _, g := range p.Groups {
		gr := importGroupReport{Email: g.Email, Source: g.Source, Skipped: g.Invalid}
		var id int64
		var kind, storedRaw, status string
		err := tx.QueryRow("SELECT id,kind,raw,status FROM mailboxes WHERE email=? COLLATE NOCASE", g.Email).Scan(&id, &kind, &storedRaw, &status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if err == nil && kind != "master" {
			out.Conflicts += 1 + len(g.Children)
			gr.Skipped += 1 + len(g.Children)
			issue(g.Source, g.Email, "主邮箱地址已用作子邮箱，跳过整组")
			out.Groups = append(out.Groups, gr)
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			encrypted, e := seal(a.key, []byte(g.Raw))
			if e != nil {
				return out, e
			}
			now := stampNow()
			res, e := tx.Exec("INSERT INTO mailboxes(email,kind,folder_id,raw,raw_complete,note,created_at,updated_at) VALUES(?,'master',?,?,?,?,?,?)", g.Email, nullableID(folderID), encrypted, completeRaw(g.Raw), g.Note, now, now)
			if e != nil {
				return out, e
			}
			id, e = res.LastInsertId()
			if e != nil {
				return out, e
			}
			status = "available"
			out.AddedMasters++
			changedMasters[id] = true
			if e = mailHistory(tx, id, actor, "mail.imported", "导入主邮箱"); e != nil {
				return out, e
			}
		} else {
			plain, e := a.mailMasterRaw(storedRaw)
			if e != nil {
				return out, e
			}
			if !completeRaw(plain) && completeRaw(g.Raw) {
				encrypted, e := seal(a.key, []byte(g.Raw))
				if e != nil {
					return out, e
				}
				if _, e = tx.Exec("UPDATE mailboxes SET raw=?,raw_complete=?,updated_at=? WHERE id=?", encrypted, completeRaw(g.Raw), stampNow(), id); e != nil {
					return out, e
				}
				out.Completed++
				changedMasters[id] = true
				if e = mailHistory(tx, id, actor, "mail.imported", "补全主邮箱资料"); e != nil {
					return out, e
				}
			} else {
				out.Duplicates++
			}
		}
		var position int
		if err = tx.QueryRow("SELECT COALESCE(MAX(position),0) FROM mailboxes WHERE parent_id=?", id).Scan(&position); err != nil {
			return out, err
		}
		for _, c := range g.Children {
			var childID int64
			var owner sql.NullInt64
			var childKind string
			err = tx.QueryRow("SELECT id,kind,parent_id FROM mailboxes WHERE email=? COLLATE NOCASE", c.Email).Scan(&childID, &childKind, &owner)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
			if err == nil {
				if childKind != "child" || (owner.Valid && owner.Int64 != id) {
					out.Conflicts++
					gr.Skipped++
					issue(g.Source, c.Email, "地址身份或主邮箱归属冲突")
					continue
				}
				if owner.Valid {
					out.Duplicates++
					gr.Skipped++
					continue
				}
				position++
				if _, err = tx.Exec("UPDATE mailboxes SET parent_id=?,link=?,link_mode='custom',link_revision=link_revision+1,position=?,updated_at=? WHERE id=?", id, c.Link, position, stampNow(), childID); err != nil {
					return out, err
				}
				out.Restored++
				changedChildren[childID] = true
				if err = mailHistory(tx, childID, actor, "mail.imported", "恢复主邮箱归属，保留出售状态与标识"); err != nil {
					return out, err
				}
				continue
			}
			position++
			now := stampNow()
			res, e := tx.Exec("INSERT INTO mailboxes(email,kind,parent_id,link,note,status,position,created_at,updated_at) VALUES(?,'child',?,?,?,?,?,?,?)", c.Email, id, c.Link, c.Note, status, position, now, now)
			if e != nil {
				return out, e
			}
			childID, e = res.LastInsertId()
			if e != nil {
				return out, e
			}
			out.AddedChildren++
			changedChildren[childID] = true
			gr.Added++
			if e = mailHistory(tx, childID, actor, "mail.imported", "导入子邮箱"); e != nil {
				return out, e
			}
		}
		out.Groups = append(out.Groups, gr)
	}
	out.affectedMasters, out.affectedChildren = len(changedMasters), len(changedChildren)
	return out, nil
}

// Context helper keeps importMail usable from tests and avoids coupling its
// transaction-only work to an HTTP request.
func contextBackground() context.Context { return context.Background() }

func decodeMailUpload(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		respond(w, http.StatusUnsupportedMediaType, map[string]string{"error": "请使用 JSON 请求"})
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "导入请求无效或体积过大"})
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		respond(w, http.StatusBadRequest, map[string]string{"error": "导入请求格式无效"})
		return false
	}
	return true
}

func mailSessionDigest(r *http.Request) (string, error) {
	c, err := r.Cookie("ml_session")
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return "", errors.New("session missing")
	}
	return digest(c.Value), nil
}

func (a *App) mailImportPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Files    []importFile `json:"files"`
		FolderID int64        `json:"folder_id"`
	}
	if !decodeMailUpload(w, r, &in) {
		return
	}
	if in.FolderID < 0 {
		fail(w, errors.New("文件夹编号无效"))
		return
	}
	p, err := parseMailImport(in.Files)
	if err != nil {
		fail(w, err)
		return
	}
	if len(p.Groups) == 0 {
		fail(w, errors.New("没有可导入的邮箱分组，请检查首行或表头"))
		return
	}
	sessionToken, err := mailSessionDigest(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
		return
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, err)
		return
	}
	report, err := a.importMail(tx, p, in.FolderID, "admin")
	_ = tx.Rollback()
	if err != nil {
		fail(w, err)
		return
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		fail(w, err)
		return
	}
	sealed, err := seal(a.key, encoded)
	if err != nil {
		fail(w, err)
		return
	}
	token := randomToken()
	expires := time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339Nano)
	tx, err = a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM mail_import_previews WHERE expires_at<=? OR session_token=?", stampNow(), sessionToken); err != nil {
		fail(w, err)
		return
	}
	var size int
	if err = tx.QueryRowContext(r.Context(), "SELECT COALESCE(SUM(length(payload)),0) FROM mail_import_previews").Scan(&size); err != nil {
		fail(w, err)
		return
	}
	if size+len(sealed) > 100<<20 {
		respond(w, http.StatusTooManyRequests, map[string]string{"error": "导入预览繁忙，请稍后重试"})
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO mail_import_previews(token,session_token,payload,folder_id,expires_at) VALUES(?,?,?,?,?)", digest(token), sessionToken, sealed, nullableID(in.FolderID), expires); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"token": token, "expires_at": expires, "report": report})
}

func (a *App) mailImportCommit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	sessionToken, err := mailSessionDigest(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
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
	var encoded string
	var folder sql.NullInt64
	err = tx.QueryRowContext(r.Context(), "SELECT payload,folder_id FROM mail_import_previews WHERE token=? AND session_token=? AND expires_at>?", digest(in.Token), sessionToken, stampNow()).Scan(&encoded, &folder)
	if errors.Is(err, sql.ErrNoRows) {
		respond(w, http.StatusConflict, map[string]string{"error": "预览已过期、已确认或属于其它登录会话，请重新预览"})
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	plain, decryptErr := unseal(a.key, encoded)
	if decryptErr == nil {
		encoded = string(plain)
	}
	var p importPayload
	if err = json.Unmarshal([]byte(encoded), &p); err != nil {
		fail(w, err)
		return
	}
	report, err := a.importMail(tx, p, folder.Int64, "admin")
	if err != nil {
		fail(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM mail_import_previews WHERE token=?", digest(in.Token)); err != nil {
		fail(w, err)
		return
	}
	if err = recordMailActivity(r.Context(), tx, MailActivity{
		Action: "mail.imported", Scope: "master", SelectionCount: len(p.Groups),
		MasterCount: report.affectedMasters, ChildCount: report.affectedChildren,
	}); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, report)
}
