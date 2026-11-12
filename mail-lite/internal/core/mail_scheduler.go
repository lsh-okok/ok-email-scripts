package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type inspectionSchedule struct {
	ID            int64             `json:"id"`
	Enabled       bool              `json:"enabled"`
	RunAt         string            `json:"run_at"`
	Scope         string            `json:"scope"`
	FolderID      int64             `json:"folder_id"`
	Sample        int               `json:"sample"`
	IncludeSold   bool              `json:"include_sold"`
	Options       inspectionOptions `json:"options"`
	LastRunDate   string            `json:"last_run_date"`
	LastRunAt     *string           `json:"last_run_at"`
	LastRunTaskID *int64            `json:"last_run_task_id"`
	LastRunStatus string            `json:"last_run_status"`
	LastError     string            `json:"last_error"`
	UpdatedAt     string            `json:"updated_at"`
	Timezone      string            `json:"timezone"`
	NextRunAt     *string           `json:"next_run_at"`
}

type inspectionScheduleInput struct {
	Enabled     bool              `json:"enabled"`
	RunAt       string            `json:"run_at"`
	Scope       string            `json:"scope"`
	FolderID    int64             `json:"folder_id"`
	Sample      int               `json:"sample"`
	IncludeSold bool              `json:"include_sold"`
	Options     inspectionOptions `json:"options"`
}

const inspectionScheduleColumns = `id,enabled,run_at,scope,COALESCE(folder_id,0),sample,include_sold,options,last_run_date,last_run_at,last_run_task_id,
 COALESCE((SELECT status FROM mail_inspections WHERE id=last_run_task_id),last_run_status),last_error,updated_at`

func (a *App) inspectionSchedule() (inspectionSchedule, error) {
	var s inspectionSchedule
	var options string
	err := a.store.db.QueryRow("SELECT "+inspectionScheduleColumns+" FROM mail_inspection_schedules WHERE id=1").Scan(&s.ID, &s.Enabled, &s.RunAt, &s.Scope, &s.FolderID, &s.Sample, &s.IncludeSold, &options, &s.LastRunDate, &s.LastRunAt, &s.LastRunTaskID, &s.LastRunStatus, &s.LastError, &s.UpdatedAt)
	if err == nil {
		err = json.Unmarshal([]byte(options), &s.Options)
	}
	if err == nil {
		err = normalizeInspectionOptions(&s.Options)
	}
	return s, err
}

func (a *App) scheduleResponse(w http.ResponseWriter) {
	s, err := a.inspectionSchedule()
	if err != nil {
		mailError(w, err)
		return
	}
	settings := a.store.Settings(context.Background())
	s.Timezone = settings.TimeZone
	if s.Enabled {
		loc, err := time.LoadLocation(s.Timezone)
		if err != nil {
			mailError(w, err)
			return
		}
		now := time.Now().In(loc)
		target, _ := time.ParseInLocation("2006-01-02 15:04", now.Format("2006-01-02")+" "+s.RunAt, loc)
		if s.LastRunDate >= now.Format("2006-01-02") {
			target = target.AddDate(0, 0, 1)
		}
		if target.Before(now) {
			target = now
		}
		next := target.UTC().Format(time.RFC3339Nano)
		s.NextRunAt = &next
	}
	respond(w, 200, s)
}

func (a *App) getInspectionSchedule(w http.ResponseWriter, r *http.Request) { a.scheduleResponse(w) }

func validRunAt(value string) bool {
	parsed, err := time.Parse("15:04", value)
	return err == nil && parsed.Format("15:04") == value
}

func (a *App) saveInspectionSchedule(w http.ResponseWriter, r *http.Request) {
	if err := a.inspectionPrimary(); err != nil {
		mailError(w, err)
		return
	}
	var in inspectionScheduleInput
	if !readJSON(w, r, &in) {
		return
	}
	if !validRunAt(in.RunAt) {
		fail(w, fmt.Errorf("执行时间需为 00:00–23:59"))
		return
	}
	if in.Scope != "all" && in.Scope != "folder" {
		fail(w, fmt.Errorf("检测范围无效"))
		return
	}
	if in.Scope == "folder" && in.FolderID < 0 {
		fail(w, fmt.Errorf("文件夹无效"))
		return
	}
	if in.Sample != 0 && in.Sample != 1 && in.Sample != 3 && in.Sample != 5 {
		fail(w, fmt.Errorf("请选择全部检测或每组抽样 1 / 3 / 5 个"))
		return
	}
	if err := normalizeInspectionOptions(&in.Options); err != nil {
		mailError(w, err)
		return
	}
	if in.Enabled {
		if _, err := a.smtpSendersForInspection(smtpSettings{}); err != nil {
			mailError(w, err)
			return
		}
	}
	if in.Scope == "all" {
		in.FolderID = 0
	}
	options, _ := json.Marshal(in.Options)
	a.inspectionMu.Lock()
	if a.closing {
		a.inspectionMu.Unlock()
		mailError(w, mailFailure{http.StatusConflict, "服务正在关闭"})
		return
	}
	a.store.write.Lock()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err == nil {
		err = func(tx *sql.Tx) error {
			if in.Scope == "folder" {
				if err := mailCheckFolder(context.Background(), tx, in.FolderID); err != nil {
					return err
				}
			}
			_, err := tx.Exec(`UPDATE mail_inspection_schedules SET enabled=?,run_at=?,scope=?,folder_id=?,sample=?,include_sold=?,options=?,updated_at=? WHERE id=1`, in.Enabled, in.RunAt, in.Scope, nullableID(in.FolderID), in.Sample, in.IncludeSold, string(options), stampNow())
			return err
		}(tx)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		tx.Rollback()
	}
	a.store.write.Unlock()
	a.inspectionMu.Unlock()
	if err != nil {
		mailError(w, err)
		return
	}
	a.scheduleResponse(w)
}

func (a *App) runInspectionSchedule(w http.ResponseWriter, r *http.Request) {
	if _, err := a.runScheduledInspection(time.Now(), true); err != nil {
		mailError(w, err)
		return
	}
	a.scheduleResponse(w)
}

func (a *App) startInspectionScheduler(ctx context.Context) {
	a.inspectionMu.Lock()
	defer a.inspectionMu.Unlock()
	if a.cfg.Role != "primary" || a.closing {
		return
	}
	if ctx.Err() != nil {
		a.closing = true
		return
	}
	a.inspectionContext = ctx
	a.schedulerWG.Add(1)
	go func() {
		defer a.schedulerWG.Done()
		defer func() {
			a.inspectionMu.Lock()
			a.closing = true
			for _, cancel := range a.inspectionJobs {
				cancel()
			}
			a.inspectionMu.Unlock()
		}()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.schedulerStop:
				return
			default:
			}
			if _, err := a.runScheduledInspection(time.Now(), false); err != nil {
				log.Printf("inspection scheduler: %v", err)
			}
			select {
			case <-ticker.C:
			case <-a.schedulerStop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Serialize task creation with manual inspections and configuration changes.
// The date claim and task snapshot commit together, so restarts cannot resend a daily run.
func (a *App) runScheduledInspection(now time.Time, manual bool) (int64, error) {
	if err := a.inspectionPrimary(); err != nil {
		return 0, err
	}
	a.inspectionMu.Lock()
	defer a.inspectionMu.Unlock()
	if a.closing {
		return 0, mailFailure{http.StatusConflict, "服务正在关闭"}
	}
	a.store.write.Lock()
	defer a.store.write.Unlock()
	s, err := a.inspectionSchedule()
	if err != nil {
		return 0, err
	}
	if !manual && !s.Enabled {
		return 0, nil
	}
	settings := a.store.Settings(context.Background())
	loc, err := time.LoadLocation(settings.TimeZone)
	if err != nil {
		return 0, err
	}
	local := now.In(loc)
	date := local.Format("2006-01-02")
	if !manual && (local.Format("15:04") < s.RunAt || s.LastRunDate >= date) {
		return 0, nil
	}
	if len(a.inspectionJobs) >= 4 {
		return 0, mailFailure{http.StatusConflict, "已有 4 个运行任务，请等待后再执行"}
	}
	if s.LastRunStatus == "queued" || s.LastRunStatus == "running" {
		return 0, mailFailure{http.StatusConflict, "上一次定时检测仍在运行，请等待或停止任务"}
	}
	senders, err := a.smtpSendersForInspection(smtpSettings{})
	if err != nil {
		return 0, a.recordScheduleFailure(s, now, date, manual, err)
	}
	base := strings.TrimRight(settings.PublicBaseURL, "/")
	if base == "" {
		base = a.cfg.ExternalOrigin
	}
	if base == "" {
		listen := a.cfg.Listen
		if strings.HasPrefix(listen, ":") {
			listen = "127.0.0.1" + listen
		}
		listen = strings.Replace(listen, "0.0.0.0:", "127.0.0.1:", 1)
		listen = strings.Replace(listen, "[::]:", "[::1]:", 1)
		if listen == "" {
			listen = "127.0.0.1:8080"
		}
		scheme := "http://"
		if a.cfg.TLSCert != "" {
			scheme = "https://"
		}
		base = scheme + listen
	}
	if _, err := a.commitMailMappingLocked(context.Background(), mailMappingCommitRequest{}, base); err != nil {
		return 0, a.recordScheduleFailure(s, now, date, manual, err)
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	in := inspectionInput{Mode: "inbox", Scope: s.Scope, FolderID: s.FolderID, Sample: s.Sample, IncludeSold: s.IncludeSold, Options: s.Options}
	candidates, err := a.inspectionCandidates(tx, in, base)
	if err != nil {
		_ = tx.Rollback()
		return 0, a.recordScheduleFailure(s, now, date, manual, err)
	}
	id, err := insertInspection(tx, "inbox", s.Scope, s.FolderID, s.Options, candidates, "系统定时任务")
	if manual {
		date = s.LastRunDate
	}
	if err == nil {
		_, err = tx.Exec("UPDATE mail_inspection_schedules SET last_run_date=?,last_run_at=?,last_run_task_id=?,last_run_status='queued',last_error='' WHERE id=1", date, now.UTC().Format(time.RFC3339Nano), id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return 0, err
	}
	a.launchInspectionWithSenders(id, "inbox", s.Options, senders, candidates, false)
	return id, nil
}

// runScheduledInspection holds inspectionMu and the parent's writer lock.
func (a *App) recordScheduleFailure(s inspectionSchedule, now time.Time, date string, manual bool, cause error) error {
	if manual {
		date = s.LastRunDate
	}
	_, err := a.store.db.Exec("UPDATE mail_inspection_schedules SET last_run_date=?,last_run_at=?,last_run_task_id=NULL,last_run_status='failed',last_error=? WHERE id=1", date, now.UTC().Format(time.RFC3339Nano), cause.Error())
	if err != nil {
		return err
	}
	return cause
}
