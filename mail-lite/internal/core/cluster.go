package core

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type snapshotSession struct {
	ID, NodeID, Path string
	Cursor           int64
	Created          time.Time
	Updated          time.Time
	Total, Sent      int64
	LastKind, LastID string
}
type SnapshotProgress struct {
	Session   string `json:"session"`
	AfterKind string `json:"after_kind"`
	AfterID   string `json:"after_id"`
	Cursor    int64  `json:"cursor"`
	Done      bool   `json:"done"`
}
type SnapshotPage struct {
	Progress SnapshotProgress `json:"progress"`
	Records  []Record         `json:"records"`
}
type PullResponse struct {
	Events           []Event `json:"events"`
	Next             int64   `json:"next"`
	Current          int64   `json:"current"`
	SnapshotRequired bool    `json:"snapshot_required"`
}

func (a *App) registerCluster(m *http.ServeMux) {
	m.HandleFunc("POST /internal/mx/read", a.nodeAuth(a.internalMXRead))
	m.HandleFunc("POST /internal/snapshot", a.nodeAuth(a.snapshot))
	m.HandleFunc("GET /internal/events", a.nodeAuth(a.events))
	m.HandleFunc("POST /internal/heartbeat", a.nodeAuth(a.heartbeat))
	m.HandleFunc("POST /internal/tokens", a.nodeAuth(a.nodeToken))
}
func (a *App) nodeAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Role != "primary" {
			respond(w, 403, map[string]string{"error": "replica is read-only"})
			return
		}
		id := r.Header.Get("X-Node-ID")
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var n Node
		if len(id) > 128 || len(token) > 128 || a.store.AuxGet("node", id, &n) != nil || n.Revoked || n.Token == "" || subtle.ConstantTimeCompare([]byte(n.Token), []byte(token)) != 1 {
			respond(w, 403, map[string]string{"error": "node_revoked_or_unauthorized"})
			return
		}
		if n.Disabled {
			respond(w, 403, map[string]string{"error": "node_disabled"})
			return
		}
		next(w, r)
	}
}
func (a *App) clusterCall(ctx context.Context, method, path string, body, out any) error {
	var data []byte
	if body != nil {
		var e error
		data, e = json.Marshal(body)
		if e != nil {
			return e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, a.cfg.MasterURL+path, bytes.NewReader(data))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.NodeToken)
	req.Header.Set("X-Node-ID", a.cfg.NodeID)
	req.Header.Set("Content-Type", "application/json")
	tr := transport("")
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, Timeout: 60 * time.Second, CheckRedirect: noRedirect}
	res, e := c.Do(req)
	if e != nil {
		return errors.New("无法连接主节点")
	}
	defer res.Body.Close()
	if res.StatusCode == 403 {
		a.ready.Store(false)
		var body struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body) == nil && body.Error == "node_disabled" {
			return errors.New("节点已停用，请在主节点管理台启用")
		}
		return errors.New("节点凭据已撤销或无效")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("主节点同步请求失败（%d）", res.StatusCode)
	}
	data, e = io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if e != nil || len(data) > 16<<20 {
		return errors.New("同步响应超出限制或中断")
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
func (a *App) snapshot(w http.ResponseWriter, r *http.Request) {
	var p SnapshotProgress
	if !readJSON(w, r, &p) {
		return
	}
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()
	for id, s := range a.snapshots {
		if time.Since(s.Created) > 15*time.Minute {
			osRemoveSnapshot(s)
			delete(a.snapshots, id)
		}
	}
	s := a.snapshots[p.Session]
	if p.Session == "" {
		if len(a.snapshots) >= 16 {
			respond(w, 503, map[string]string{"error": "快照任务繁忙"})
			return
		}
		s = &snapshotSession{ID: randomID(), NodeID: r.Header.Get("X-Node-ID"), Created: time.Now()}
		s.Path = filepath.Join(a.cfg.DataDir, "snapshot-"+s.ID+".db")
		if e := a.store.SnapshotFile(s.Path); e != nil {
			fail(w, e)
			return
		}
		copy, e := OpenStore(s.Path, a.key)
		if e != nil {
			osRemoveSnapshot(s)
			fail(w, e)
			return
		}
		s.Cursor = copy.Cursor()
		copy.db.QueryRow("SELECT count(*) FROM records").Scan(&s.Total)
		copy.Close()
		a.snapshots[s.ID] = s
		p = SnapshotProgress{Session: s.ID, Cursor: s.Cursor}
	} else if s == nil || s.NodeID != r.Header.Get("X-Node-ID") {
		respond(w, 409, map[string]string{"error": "snapshot_expired"})
		return
	}
	copy, e := OpenStore(s.Path, a.key)
	if e != nil {
		fail(w, e)
		return
	}
	defer copy.Close()
	records, e := copy.SnapshotPage(r.Context(), p.AfterKind, p.AfterID)
	if e != nil {
		fail(w, e)
		return
	}
	p.Session = s.ID
	p.Cursor = s.Cursor
	p.Done = len(records) < 1000
	if len(records) > 0 {
		last := records[len(records)-1]
		p.AfterKind, p.AfterID = last.Kind, last.ID
		if last.Kind > s.LastKind || (last.Kind == s.LastKind && last.ID > s.LastID) {
			s.Sent += int64(len(records))
			s.LastKind, s.LastID = last.Kind, last.ID
		}
	}
	s.Updated = time.Now()
	respond(w, 200, SnapshotPage{Progress: p, Records: records})
}
func osRemoveSnapshot(s *snapshotSession) {
	os.Remove(s.Path)
	os.Remove(s.Path + "-wal")
	os.Remove(s.Path + "-shm")
}
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	after, e := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if e != nil || after < 0 {
		fail(w, errors.New("invalid replication cursor"))
		return
	}
	values, required, e := a.store.Events(r.Context(), after, 500)
	if e != nil {
		fail(w, e)
		return
	}
	next := after
	if len(values) > 0 {
		next = values[len(values)-1].Cursor
	}
	respond(w, 200, PullResponse{Events: values, Next: next, Current: a.store.Cursor(), SnapshotRequired: required})
}
func (a *App) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cursor int64  `json:"cursor"`
		Error  string `json:"error"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	a.mutations.Lock()
	defer a.mutations.Unlock()
	var n Node
	if e := a.store.AuxGet("node", r.Header.Get("X-Node-ID"), &n); e != nil || n.Revoked {
		respond(w, 403, map[string]string{"error": "node_revoked_or_unauthorized"})
		return
	}
	if n.Disabled {
		respond(w, 403, map[string]string{"error": "node_disabled"})
		return
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip, err := netip.ParseAddr(host); err == nil {
			n.IP = ip.Unmap().String()
		}
	}
	n.LastSeen = time.Now().UTC().Format(time.RFC3339)
	n.Cursor = body.Cursor
	n.Error = body.Error
	if len(n.Error) > 200 {
		n.Error = n.Error[:200]
	}
	if e := a.store.AuxPut("node", n.ID, n); e != nil {
		fail(w, e)
		return
	}
	a.snapshotMu.Lock()
	for id, s := range a.snapshots {
		if s.NodeID == n.ID && n.Cursor >= s.Cursor {
			osRemoveSnapshot(s)
			delete(a.snapshots, id)
		}
	}
	a.snapshotMu.Unlock()
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) nodeToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID string `json:"account_id"`
		Kind      string `json:"kind"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Kind != "graph" && body.Kind != "imap" {
		respond(w, 403, map[string]string{"error": "nodes cannot request write tokens"})
		return
	}
	v, e := a.store.Account(r.Context(), body.AccountID)
	if e != nil || v.Status != "active" {
		fail(w, ErrNotFound)
		return
	}
	token, e := a.tokens.Get(r.Context(), body.AccountID, body.Kind, false)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, token)
}
func (a *App) syncLoop(ctx context.Context) {
	for {
		work, cancel := context.WithTimeout(ctx, 5*time.Minute)
		e := a.syncOnce(work)
		cancel()
		a.syncMu.Lock()
		if e != nil {
			a.syncError = e.Error()
		} else {
			a.syncError = ""
		}
		a.syncMu.Unlock()
		if e != nil && ctx.Err() == nil {
			work, cancel := context.WithTimeout(ctx, 5*time.Second)
			a.clusterCall(work, "POST", "/internal/heartbeat", map[string]any{"cursor": a.store.Cursor(), "error": e.Error()}, nil)
			cancel()
		}
		timer := time.NewTimer(a.cfg.Poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (a *App) syncOnce(ctx context.Context) error {
	if a.store.Meta("initialized") != "true" {
		if e := a.fullSync(ctx); e != nil {
			return e
		}
	}
	for {
		var response PullResponse
		if e := a.clusterCall(ctx, "GET", "/internal/events?after="+strconv.FormatInt(a.store.Cursor(), 10), nil, &response); e != nil {
			return e
		}
		if response.SnapshotRequired {
			if e := a.fullSync(ctx); e != nil {
				return e
			}
			continue
		}
		if len(response.Events) > 0 {
			if e := a.store.Apply(ctx, response.Events, response.Next); e != nil {
				return e
			}
			a.cache.Invalidate("")
			for _, event := range response.Events {
				if event.Record.Kind == "account" {
					a.tokens.Invalidate(event.Record.ID)
				}
			}
		}
		if response.Next >= response.Current || len(response.Events) == 0 {
			return a.ackSync(ctx)
		}
	}
}
func (a *App) ackSync(ctx context.Context) error {
	if e := a.clusterCall(ctx, "POST", "/internal/heartbeat", map[string]any{"cursor": a.store.Cursor(), "error": ""}, nil); e != nil {
		return e
	}
	now := time.Now()
	if e := a.store.SetMeta("last_sync", now.UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	a.syncMu.Lock()
	a.lastSync = now
	a.syncMu.Unlock()
	a.ready.Store(true)
	return nil
}
func (a *App) fullSync(ctx context.Context) error {
	a.ready.Store(false)
	stage, e := OpenStore(filepath.Join(a.cfg.DataDir, "stage.db"), a.key)
	if e != nil {
		return e
	}
	defer stage.Close()
	var p SnapshotProgress
	json.Unmarshal([]byte(stage.Meta("snapshot_progress")), &p)
	if p.Done {
		p = SnapshotProgress{}
		stage.db.Exec("DELETE FROM records; DELETE FROM meta")
	}
	for !p.Done {
		var page SnapshotPage
		e = a.clusterCall(ctx, "POST", "/internal/snapshot", p, &page)
		if e != nil {
			if strings.Contains(e.Error(), "409") {
				stage.db.Exec("DELETE FROM records; DELETE FROM meta")
				p = SnapshotProgress{}
				continue
			}
			return e
		}
		if page.Progress.Session == "" || page.Progress.Cursor < 0 {
			return errors.New("invalid snapshot response")
		}
		if e = stage.Stage(ctx, page.Records, page.Progress); e != nil {
			return e
		}
		p = page.Progress
	}
	if e = a.store.InstallSnapshot(ctx, stage, p.Cursor); e != nil {
		return e
	}
	if e = a.store.SetMeta("initialized", "true"); e != nil {
		return e
	}
	a.cache.Invalidate("")
	return nil
}
