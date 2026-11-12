package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

type jobTask struct {
	job Job
	run func(context.Context, *Job)
}
type ImportFile struct {
	Name string `json:"name"`
	Text string `json:"text"`
}
type ImportRequest struct {
	Kind       string       `json:"kind"`
	Text       string       `json:"text"`
	Files      []ImportFile `json:"files"`
	AccountID  string       `json:"account_id"`
	GroupID    string       `json:"group_id"`
	PlatformID string       `json:"platform_id"`
	ExpiresAt  string       `json:"expires_at"`
}

func (a *App) enqueue(kind string, run func(context.Context, *Job)) (Job, error) {
	job := Job{ID: time.Now().UTC().Format("20060102T150405.000000000") + "-" + randomID()[:8], Kind: kind, Status: "queued", Errors: []string{}, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if e := a.store.AuxPut("job", job.ID, job); e != nil {
		return job, e
	}
	select {
	case a.jobs <- jobTask{job, run}:
		return job, nil
	default:
		a.store.AuxDelete("job", job.ID)
		return job, errors.New("后台任务队列已满，请稍后重试")
	}
}
func (a *App) jobLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-a.jobs:
			func() {
				job := task.job
				defer func() {
					if p := recover(); p != nil {
						job.Status = "failed"
						job.Errors = append(job.Errors, "任务内部错误")
					}
					if job.Status == "running" {
						job.Status = "completed"
						if job.Failed > 0 {
							job.Status = "partial_failed"
						}
					}
					job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
					a.store.AuxPut("job", job.ID, job)
				}()
				job.Status = "running"
				a.store.AuxPut("job", job.ID, job)
				task.run(ctx, &job)
			}()
		}
	}
}
func (a *App) recoverJobs() {
	rs, _ := a.store.AuxList("job")
	for _, r := range rs {
		var j Job
		if decode(r, &j) == nil && (j.Status == "queued" || j.Status == "running") {
			j.Status = "failed"
			j.Errors = append(j.Errors, "服务重启，任务已中断；可重新提交，已有记录会复用")
			j.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			a.store.AuxPut("job", j.ID, j)
		}
	}
}
func jobError(j *Job, s string) {
	j.Failed++
	if len(j.Errors) < 100 {
		j.Errors = append(j.Errors, s)
	}
}
func (a *App) progress(j *Job) {
	if j.Done%100 == 0 || j.Done == j.Total {
		a.store.AuxPut("job", j.ID, *j)
	}
}
func parseAccountLine(line string) (Account, error) {
	parts := strings.Split(strings.TrimSpace(line), "----")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	v := Account{ID: randomID(), Status: "active"}
	if len(parts) < 2 {
		return v, errors.New("需要邮箱及凭据，以 ---- 分隔")
	}
	v.Email = parts[0]
	if len(parts) == 3 && strings.EqualFold(parts[1], "MX") {
		days, err := strconv.Atoi(parts[2])
		if err != nil {
			return v, errors.New("MX 保留天数格式不正确")
		}
		v.Provider = "mx"
		v.MXRetentionDays = &days
		return v, validateAccount(&v)
	}
	switch len(parts) {
	case 2:
		v.Provider = "icloud"
		v.Password = parts[1]
	case 3:
		v.Provider = "outlook"
		v.ClientID = parts[1]
		v.RefreshToken = parts[2]
	case 4:
		v.Provider = "outlook"
		v.Password = parts[1]
		v.ClientID = parts[2]
		v.RefreshToken = parts[3]
	default:
		return v, errors.New("邮箱凭据格式错误，Outlook 应为 邮箱----密码----Client ID----Refresh Token")
	}
	return v, validateAccount(&v)
}
func (a *App) importJob(w http.ResponseWriter, r *http.Request) {
	var body ImportRequest
	if !readJSON(w, r, &body) {
		return
	}
	if body.Kind != "accounts" && body.Kind != "links" {
		fail(w, errors.New("未知导入类型"))
		return
	}
	if !validExpiry(body.ExpiresAt) {
		fail(w, errors.New("有效期格式错误"))
		return
	}
	if len(body.Files) > 20 {
		fail(w, errors.New("每次最多导入 20 个 TXT 文件"))
		return
	}
	if len(body.Files) == 0 {
		body.Files = []ImportFile{{Name: "文本", Text: body.Text}}
	}
	for kind, id := range map[string]string{"account": body.AccountID, "group": body.GroupID, "platform": body.PlatformID} {
		if id != "" {
			if _, e := a.store.Get(r.Context(), kind, id); e != nil {
				fail(w, errors.New("指定的主邮箱、分组或平台不存在"))
				return
			}
		}
	}
	job, e := a.enqueue("import_"+body.Kind, func(ctx context.Context, j *Job) { a.runImport(ctx, j, body) })
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 202, job)
}
func (a *App) runImport(ctx context.Context, j *Job, body ImportRequest) {
	if body.Kind == "links" {
		a.runLinksImport(ctx, j, body)
		return
	}
	for _, f := range body.Files {
		j.Total += len(strings.Split(strings.TrimSpace(f.Text), "\n"))
	}
	a.store.AuxPut("job", j.ID, *j)
	for _, file := range body.Files {
		lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(file.Text, "\ufeff")), "\n")
		accountID := body.AccountID
		for index, line := range lines {
			if ctx.Err() != nil {
				j.Status = "failed"
				jobError(j, "任务已中断")
				return
			}
			line = strings.TrimSpace(line)
			j.Done++
			if line == "" {
				a.progress(j)
				continue
			}
			label := fmt.Sprintf("%s 第 %d 行", file.Name, index+1)
			if body.Kind == "accounts" {
				v, e := parseAccountLine(line)
				if e != nil {
					jobError(j, label+"："+e.Error())
					a.progress(j)
					continue
				}
				v.GroupID = body.GroupID
				if existing, e := a.store.Find(ctx, "kind='account' AND email=? COLLATE NOCASE", v.Email); e == nil {
					old, _ := a.store.Account(ctx, existing.ID)
					if (old.Provider == "mx" || v.Provider == "mx") && old.Provider != v.Provider {
						jobError(j, label+"：MX 与其他类型不能互相转换")
						continue
					}
					v.ID = old.ID
					v.Generation = old.Generation + 1
					version := old.Version
					_, e = a.store.Save(ctx, v, &version)
					if e != nil {
						jobError(j, label+"：账号更新失败")
					}
				} else if _, e = a.store.Save(ctx, v, nil); e != nil {
					jobError(j, label+"：账号保存失败")
				}
				a.cache.Invalidate(v.ID)
				a.tokens.Invalidate(v.ID)
			} else {
				if accountID == "" {
					main := strings.TrimSpace(strings.Split(line, "----")[0])
					rec, e := a.store.Find(ctx, "kind='account' AND email=? COLLATE NOCASE", main)
					if e != nil {
						v, err := parseAccountLine(line)
						if err != nil {
							jobError(j, label+"：首行需要已导入的主邮箱，或包含凭据的主邮箱行")
							break
						}
						v.GroupID = body.GroupID
						if _, err = a.store.Save(ctx, v, nil); err != nil {
							jobError(j, label+"：创建主邮箱失败")
							break
						}
						accountID = v.ID
					} else {
						accountID = rec.ID
					}
					a.progress(j)
					continue
				}
				for _, recipient := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == ';' }) {
					recipient = strings.TrimSpace(strings.Split(recipient, "----")[0])
					normalized := address(recipient)
					if normalized == "" {
						jobError(j, label+"：邮箱地址格式错误")
						continue
					}
					if _, e := a.store.Find(ctx, "kind='link' AND account_id=? AND recipient=? COLLATE NOCASE AND platform_id=?", accountID, normalized, body.PlatformID); e == nil {
						continue
					}
					token := randomToken()
					v := Link{ID: randomID(), AccountID: accountID, Recipient: normalized, Token: token, TokenHash: digest(token), PlatformID: body.PlatformID, ExpiresAt: body.ExpiresAt, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
					if _, e := a.store.Save(ctx, v, nil); e != nil {
						jobError(j, label+"：链接保存失败，请检查主邮箱和平台")
					}
				}
			}
			a.progress(j)
		}
	}
}
func (a *App) refreshJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.IDs) > 10000 {
		fail(w, errors.New("选中邮箱过多"))
		return
	}
	j, e := a.enqueue("refresh", func(ctx context.Context, j *Job) { a.runRefresh(ctx, j, body.IDs, false) })
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 202, j)
}
func (a *App) runRefresh(ctx context.Context, j *Job, ids []string, scheduled bool) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if len(ids) == 0 {
		rs, e := a.store.All(ctx, "account")
		if e != nil {
			jobError(j, "读取邮箱失败")
			return
		}
		for _, r := range rs {
			ids = append(ids, r.ID)
		}
	}
	j.Total = len(ids)
	settings := a.store.Settings(ctx)
	for _, id := range ids {
		if ctx.Err() != nil {
			j.Status = "failed"
			jobError(j, "任务已中断")
			return
		}
		v, e := a.store.Account(ctx, id)
		if e != nil {
			j.Done++
			jobError(j, "邮箱已不存在")
			continue
		}
		if v.Provider != "outlook" || v.Status != "active" {
			j.Done++
			continue
		}
		if scheduled && v.LastRefresh != "" {
			last, _ := time.Parse(time.RFC3339, v.LastRefresh)
			if time.Since(last) < time.Duration(settings.RefreshDays)*24*time.Hour {
				j.Done++
				continue
			}
		}
		work, cancel := context.WithTimeout(ctx, 45*time.Second)
		// Match the original project: every scheduled refresh validates the
		// refresh token against Microsoft, even when an access token is cached.
		// Manual refresh is also an explicit force=true operation.
		_, e = a.tokens.Get(work, id, "graph", true)
		if e != nil {
			if _, imapErr := a.tokens.Get(work, id, "imap", true); imapErr != nil {
				e = fmt.Errorf("Graph：%v；IMAP：%w", e, imapErr)
			} else {
				e = nil
			}
		}
		cancel()
		for retry := 0; retry < 5; retry++ {
			fresh, err := a.store.Account(ctx, id)
			if err != nil {
				break
			}
			fresh.RefreshError = ""
			if e != nil {
				fresh.RefreshError = e.Error()
			} else {
				fresh.LastRefresh = time.Now().UTC().Format(time.RFC3339)
			}
			expected := fresh.Version
			if _, err = a.store.Save(ctx, fresh, &expected); err == nil {
				break
			} else if !errors.Is(err, ErrConflict) {
				break
			}
		}
		if e != nil {
			jobError(j, v.Email+"："+e.Error())
		}
		j.Done++
		a.progress(j)
	}
}
func (a *App) configureSchedule() {
	a.cronMu.Lock()
	defer a.cronMu.Unlock()
	if a.cron != nil {
		a.cron.Stop()
	}
	settings := a.store.Settings(context.Background())
	if !settings.RefreshEnabled {
		return
	}
	c := cron.New(cron.WithLocation(location(settings.TimeZone)), cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	if _, e := c.AddFunc(settings.RefreshCron, func() {
		a.enqueue("scheduled_refresh", func(ctx context.Context, j *Job) { a.runRefresh(ctx, j, nil, true) })
	}); e != nil {
		return
	}
	a.cron = c
	c.Start()
}

func location(name string) *time.Location {
	v, e := time.LoadLocation(name)
	if e != nil {
		return time.UTC
	}
	return v
}
