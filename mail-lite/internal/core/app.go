package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	smtp "github.com/emersion/go-smtp"
	"github.com/robfig/cron/v3"
	"golang.org/x/crypto/bcrypt"
)

type App struct {
	mx                     *mxStore
	smtpServer             *smtp.Server
	smtpListener           net.Listener
	smtpRunning            atomic.Bool
	smtpSessions           sync.WaitGroup
	cfg                    Config
	store                  *Store
	key                    []byte
	assets                 fs.FS
	cache                  *MailCache
	tokens                 *TokenManager
	mail                   *MailService
	adminPages             *adminPageStore
	publicPages            *publicPageStore
	password               []byte
	passwordMu             sync.RWMutex
	authMu                 sync.Mutex
	attempts               map[string]loginAttempt
	mutations              sync.Mutex
	ready                  atomic.Bool
	syncMu                 sync.Mutex
	lastSync               time.Time
	syncError              string
	snapshotMu             sync.Mutex
	snapshots              map[string]*snapshotSession
	lifecycle              sync.Mutex
	cancel                 context.CancelFunc
	workers                sync.WaitGroup
	started                bool
	jobs                   chan jobTask
	jobWG                  sync.WaitGroup
	cron                   *cron.Cron
	cronMu                 sync.Mutex
	refreshMu              sync.Mutex
	inspectionMu           sync.Mutex
	inspectionContext      context.Context
	inspectionJobs         map[int64]context.CancelFunc
	inspectionWG           sync.WaitGroup
	inspectionSendMu       sync.Mutex
	inspectionLastSenderID int64
	inspectionLastSend     time.Time
	inspectionSenderLoaded bool
	inspectionHTTP         *http.Client
	inspectionSend         func(context.Context, smtpSettings, string, string) error
	schedulerStop          chan struct{}
	schedulerWG            sync.WaitGroup
	inspectionStopOnce     sync.Once
	closing                bool
}
type loginAttempt struct {
	count int
	until time.Time
}
type session struct {
	CSRF    string    `json:"csrf"`
	Expires time.Time `json:"expires"`
}

func decode(b []byte, v any) error { return json.Unmarshal(b, v) }
func NewApp(cfg Config, assets fs.FS) (*App, error) {
	if cfg.Poll == 0 {
		cfg.Poll = 5 * time.Second
	}
	if cfg.MaxStale == 0 {
		cfg.MaxStale = 120 * time.Second
	}
	if cfg.UpstreamLimit == 0 {
		cfg.UpstreamLimit = 16
	}
	if cfg.GraphURL == "" {
		cfg.GraphURL = "https://graph.microsoft.com/v1.0"
	}
	if cfg.OAuthURL == "" {
		cfg.OAuthURL = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	}
	if e := os.MkdirAll(cfg.DataDir, 0700); e != nil {
		return nil, e
	}
	key, e := loadKey(cfg.DataDir, cfg.DataKey)
	if e != nil {
		return nil, e
	}
	store, e := OpenStore(filepath.Join(cfg.DataDir, "mail-lite.db"), key)
	if e != nil {
		return nil, e
	}
	a := &App{cfg: cfg, store: store, key: key, assets: assets, cache: newMailCache(), adminPages: newAdminPageStore(), publicPages: newPublicPageStore(), attempts: map[string]loginAttempt{}, snapshots: map[string]*snapshotSession{}, jobs: make(chan jobTask, 16), inspectionJobs: map[int64]context.CancelFunc{}, schedulerStop: make(chan struct{})}
	if cfg.Role == "primary" {
		if e = a.migrateMail(); e != nil {
			store.Close()
			return nil, e
		}
	}
	a.tokens = &TokenManager{app: a}
	a.mail = &MailService{app: a, backend: &liveBackend{app: a}, gate: make(chan struct{}, cfg.UpstreamLimit)}
	if cfg.Role == "primary" {
		if e = store.ensureAccountCodes(context.Background()); e != nil {
			store.Close()
			return nil, e
		}
		if e = a.initAdminPassword(); e != nil {
			store.Close()
			return nil, e
		}
		a.mx, e = openMXStore(cfg.DataDir, key)
		if e != nil {
			store.Close()
			return nil, e
		}
		if e = a.cleanMX(context.Background()); e != nil {
			a.mx.db.Close()
			store.Close()
			return nil, e
		}
		if e = a.prepareSMTP(); e != nil {
			a.mx.db.Close()
			store.Close()
			return nil, e
		}
		a.ready.Store(true)
	} else {
		if t, e := time.Parse(time.RFC3339Nano, store.Meta("last_sync")); e == nil {
			a.lastSync = t
			a.ready.Store(store.Meta("initialized") == "true")
		}
	}
	return a, nil
}
func (a *App) Close() {
	a.lifecycle.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.lifecycle.Unlock()
	if a.smtpServer != nil && !a.started {
		a.smtpListener.Close()
	}
	a.inspectionStopOnce.Do(func() { close(a.schedulerStop) })
	a.inspectionMu.Lock()
	a.closing = true
	for _, cancel := range a.inspectionJobs {
		cancel()
	}
	a.inspectionMu.Unlock()
	a.schedulerWG.Wait()
	a.inspectionWG.Wait()
	a.cronMu.Lock()
	if a.cron != nil {
		<-a.cron.Stop().Done()
	}
	a.cronMu.Unlock()
	a.workers.Wait()
	a.smtpSessions.Wait()
	a.jobWG.Wait()
	a.cache.Close()
	a.adminPages.clear()
	a.publicPages.clear()
	a.snapshotMu.Lock()
	for _, s := range a.snapshots {
		osRemoveSnapshot(s)
	}
	a.snapshotMu.Unlock()
	if a.mx != nil {
		a.mx.db.Close()
	}
	a.store.Close()
}
func (a *App) Start(ctx context.Context) {
	a.lifecycle.Lock()
	if a.started {
		a.lifecycle.Unlock()
		return
	}
	a.started = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.lifecycle.Unlock()
	if a.cfg.Role == "replica" {
		a.workers.Add(1)
		go func() { defer a.workers.Done(); a.syncLoop(ctx) }()
		return
	}
	a.startSMTP(ctx)
	a.recoverJobs()
	a.jobWG.Add(1)
	go func() { defer a.jobWG.Done(); a.jobLoop(ctx) }()
	a.configureSchedule()
	if a.cfg.Role == "primary" {
		a.startInspectionScheduler(ctx)
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.maintenance()
			}
		}
	}()
}
func (a *App) isReady() bool {
	if !a.ready.Load() {
		return false
	}
	if a.cfg.Role == "primary" {
		return !a.cfg.SMTPEnabled || a.smtpRunning.Load()
	}
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	return !a.lastSync.IsZero() && time.Since(a.lastSync) <= a.cfg.MaxStale
}
func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"ok": true, "role": a.cfg.Role})
	})
	m.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		status := 200
		if !a.isReady() {
			status = 503
		}
		respond(w, status, map[string]any{"ready": a.isReady(), "role": a.cfg.Role})
	})
	m.HandleFunc("GET /api/session", a.sessionInfo)
	m.HandleFunc("POST /api/login", a.login)
	m.HandleFunc("POST /api/logout", a.admin(a.logout))
	a.registerAdmin(m)
	a.registerCards(m)
	a.registerCluster(m)
	m.HandleFunc("GET /api/mailboxes/{token}/messages", a.publicMessages)
	m.HandleFunc("GET /m/{token}", a.publicPage)
	m.HandleFunc("GET /api/mailboxes/{platform}/{account}/{recipient}/messages", a.publicMessages)
	m.HandleFunc("GET /m/{platform}/{account}/{recipient}", a.publicPage)
	m.HandleFunc("GET /show/{platform}/{account}/{recipient}", a.publicPage)
	m.HandleFunc("GET /admin/", a.adminAssets)
	m.HandleFunc("GET /redeem", a.redemptionPage)
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Role == "replica" {
			respond(w, 200, map[string]any{"service": "mail-lite", "role": "replica", "ready": a.isReady()})
			return
		}
		http.Redirect(w, r, "/admin/accounts", 302)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer a.recordAccess(r)
		defer func() {
			if p := recover(); p != nil {
				slog.Error("request panic", "path", r.URL.Path)
				respond(w, 500, map[string]string{"error": "服务内部错误"})
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/internal/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if a.cfg.Role == "replica" && (strings.HasPrefix(r.URL.Path, "/api/admin/") || strings.HasPrefix(r.URL.Path, "/admin/") || r.URL.Path == "/api/login" || r.URL.Path == "/api/logout") {
			respond(w, 403, map[string]string{"error": "子节点只提供公共取信服务"})
			return
		}
		m.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, e error) {
	status := 400
	if errors.Is(e, ErrNotFound) {
		status = 404
	}
	if errors.Is(e, ErrConflict) || strings.Contains(e.Error(), "UNIQUE constraint") {
		status = 409
	}
	message := e.Error()
	if strings.Contains(message, "constraint") {
		message = "记录重复或仍被引用"
	}
	respond(w, status, map[string]string{"error": message})
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		respond(w, 400, map[string]string{"error": "请求格式错误或超过 32 MiB"})
		return false
	}
	if e := d.Decode(&struct{}{}); e != io.EOF {
		respond(w, 400, map[string]string{"error": "请求包含多余内容"})
		return false
	}
	return true
}
func (a *App) loadSession(r *http.Request) (session, error) {
	var s session
	c, e := r.Cookie("ml_session")
	if e != nil {
		return s, e
	}
	e = a.store.AuxGet("session", digest(c.Value), &s)
	if e != nil {
		return s, e
	}
	if time.Now().After(s.Expires) {
		a.store.AuxDelete("session", digest(c.Value))
		return s, errors.New("session expired")
	}
	return s, nil
}
func (a *App) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host || a.cfg.ExternalOrigin != "" && origin == a.cfg.ExternalOrigin
}
func (a *App) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, e := a.loadSession(r)
		if e != nil {
			respond(w, 401, map[string]string{"error": "请先登录"})
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if !a.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(s.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
				respond(w, 403, map[string]string{"error": "请求验证失败，请刷新页面"})
				return
			}
		}
		next(w, r)
	}
}
func (a *App) sessionInfo(w http.ResponseWriter, r *http.Request) {
	s, e := a.loadSession(r)
	respond(w, 200, map[string]any{"authenticated": e == nil, "csrf": s.CSRF, "role": a.cfg.Role})
}
func (a *App) allowPasswordAttempt(w http.ResponseWriter, r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.authMu.Lock()
	attempt := a.attempts[host]
	if time.Now().After(attempt.until) {
		attempt = loginAttempt{until: time.Now().Add(time.Minute)}
	}
	if attempt.count >= 8 {
		a.authMu.Unlock()
		respond(w, 429, map[string]string{"error": "密码验证尝试过多，请一分钟后重试"})
		return false
	}
	attempt.count++
	if len(a.attempts) > 10000 {
		for k, v := range a.attempts {
			if time.Now().After(v.until) {
				delete(a.attempts, k)
			}
		}
	}
	a.attempts[host] = attempt
	a.authMu.Unlock()
	return true
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		respond(w, 403, map[string]string{"error": "请求来源不匹配"})
		return
	}
	if !a.allowPasswordAttempt(w, r) {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	// Keep verification and session creation together, so a password change
	// cannot be followed by a new session authenticated with the old password.
	a.passwordMu.RLock()
	defer a.passwordMu.RUnlock()
	if bcrypt.CompareHashAndPassword(a.password, []byte(body.Password)) != nil {
		respond(w, 401, map[string]string{"error": "密码不正确"})
		return
	}
	token := randomToken()
	s := session{CSRF: randomToken(), Expires: time.Now().Add(24 * time.Hour)}
	if e := a.store.AuxPut("session", digest(token), s); e != nil {
		fail(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ml_session", Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(a.cfg.ExternalOrigin, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	respond(w, 200, map[string]any{"authenticated": true, "csrf": s.CSRF, "role": a.cfg.Role})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("ml_session"); e == nil {
		a.store.AuxDelete("session", digest(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "ml_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) adminAssets(w http.ResponseWriter, r *http.Request) {
	dist, e := fs.Sub(a.assets, "dist")
	if e != nil {
		http.Error(w, "assets unavailable", 500)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/assets/") {
		w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
		http.StripPrefix("/admin/", http.FileServer(http.FS(dist))).ServeHTTP(w, r)
		return
	}
	data, e := fs.ReadFile(dist, "index.html")
	if e != nil {
		http.Error(w, "assets unavailable", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}
func pagination(r *http.Request) (int, int) {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	n, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if p < 1 {
		p = 1
	}
	if p > 100000 {
		p = 100000
	}
	if n < 1 {
		n = 20
	}
	if n > 100 {
		n = 100
	}
	return p, n
}
func (a *App) publicLookup(ctx context.Context, token string, limit int) (Link, []Message, error) {
	var l Link
	if !a.isReady() {
		return l, nil, errors.New("节点同步未就绪，请稍后重试")
	}
	if len(token) < 32 || len(token) > 128 {
		return l, nil, ErrNotFound
	}
	r, e := a.store.Find(ctx, "kind='link' AND token_hash=?", digest(token))
	if e != nil {
		return l, nil, e
	}
	if e = decode(r.Data, &l); e != nil {
		return l, nil, e
	}
	return a.readPublicLink(ctx, l, limit)
}
func (a *App) publicLinkContext(ctx context.Context, l Link) (Account, *Platform, error) {
	if l.ExpiresAt != "" {
		t, e := time.Parse(time.RFC3339, l.ExpiresAt)
		if e != nil || !time.Now().Before(t) {
			return Account{}, nil, errors.New("链接已过期")
		}
	}
	account, e := a.store.Account(ctx, l.AccountID)
	if e != nil {
		return Account{}, nil, e
	}
	if account.Status != "active" {
		return Account{}, nil, ErrNotFound
	}
	var platform *Platform
	if l.PlatformID != "" {
		r, e := a.store.Get(ctx, "platform", l.PlatformID)
		if e != nil {
			return Account{}, nil, errors.New("链接的平台规则不可用")
		}
		p := new(Platform)
		if e = decode(r.Data, p); e != nil {
			return Account{}, nil, e
		}
		platform = p
	}
	return account, platform, nil
}
func (a *App) readPublicLink(ctx context.Context, l Link, limit int) (Link, []Message, error) {
	account, platform, e := a.publicLinkContext(ctx, l)
	if e != nil {
		return l, nil, e
	}
	if account.Provider == "mx" {
		values, err := a.mxLinkMessages(ctx, account, l, limit)
		l.Token, l.TokenHash = "", ""
		return l, values, err
	}
	if limit == 1 && platform == nil {
		values, err := a.mail.LatestRecipient(ctx, account, l.Recipient)
		if err == nil {
			err = a.checkPublicReadState(ctx, l, account, platform)
		}
		l.Token, l.TokenHash = "", ""
		return l, values, err
	}
	var values []Message
	if platform != nil {
		// Apply the platform rule before truncating. A newer message for a
		// different platform must not hide the newest eligible message.
		values, e = a.mail.ListPlatformRecipient(ctx, account, l.Recipient, *platform, limit)
	} else {
		values, e = a.mail.ListRecipient(ctx, account, l.Recipient, limit)
	}
	if e != nil {
		return l, nil, e
	}
	if e = a.checkPublicReadState(ctx, l, account, platform); e != nil {
		return l, nil, e
	}
	out := []Message{}
	for _, m := range values {
		if !matchesAccountRecipient(account, m, l.Recipient) || (platform != nil && !platformMatches(m, *platform)) {
			continue
		}
		detail, e := a.mail.Detail(ctx, account, m.Source)
		if e != nil {
			return l, nil, e
		}
		if !matchesAccountRecipient(account, detail, l.Recipient) || platform != nil && !platformMatches(detail, *platform) {
			continue
		}
		m.Text, m.HTML = detail.Text, detail.HTML
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	l.Token = ""
	l.TokenHash = ""
	if e = a.checkPublicReadState(ctx, l, account, platform); e != nil {
		return l, nil, e
	}
	return l, out, nil
}

func (a *App) checkPublicReadState(ctx context.Context, l Link, account Account, platform *Platform) error {
	fresh, rules, err := a.publicLinkContext(ctx, l)
	if err != nil {
		return err
	}
	if fresh.Generation != account.Generation || publicPlatformPolicy(rules) != publicPlatformPolicy(platform) {
		return ErrNotFound
	}
	return nil
}
func (a *App) readPublicMessage(ctx context.Context, l Link, q url.Values) (Link, []Message, error) {
	account, platform, e := a.publicLinkContext(ctx, l)
	if e != nil {
		return l, nil, e
	}
	var source MessageRef
	if raw := strings.TrimSpace(q.Get("ref")); raw != "" {
		source, e = parseRef(a.key, raw, l.AccountID)
		if e != nil {
			return l, nil, ErrNotFound
		}
	} else {
		id := strings.TrimSpace(q.Get("id"))
		if id == "" {
			id = strings.TrimSpace(q.Get("message_id"))
		}
		if id == "" || len(id) > 2048 {
			return l, nil, ErrNotFound
		}
		if account.Provider == "mx" {
			source = MessageRef{AccountID: l.AccountID, Protocol: "mx", ID: id, Folder: "inbox"}
		} else if account.Provider == "outlook" {
			source = MessageRef{AccountID: l.AccountID, Protocol: "graph", ID: id, Folder: "inbox"}
		} else {
			return l, nil, ErrNotFound
		}
	}
	var detail Message
	if source.Protocol == "graph" && source.Folder == "inbox" && q.Get("ref") == "" {
		// A raw Graph ID does not carry its folder. Try both public folders.
		for _, folder := range []string{"inbox", "junkemail"} {
			source.Folder = folder
			detail, e = a.mail.Detail(ctx, account, source)
			if e == nil {
				break
			}
		}
	} else {
		detail, e = a.mail.Detail(ctx, account, source)
	}
	if e != nil {
		if source.Protocol == "graph" && strings.Contains(e.Error(), "请求失败（404）") {
			return l, nil, ErrNotFound
		}
		return l, nil, e
	}
	if !matchesAccountRecipient(account, detail, l.Recipient) || (platform != nil && !platformMatches(detail, *platform)) {
		return l, nil, ErrNotFound
	}
	if e = a.checkPublicReadState(ctx, l, account, platform); e != nil {
		return l, nil, e
	}
	l.Token = ""
	l.TokenHash = ""
	return l, []Message{detail}, nil
}
func publicStatus(e error) int {
	if errors.Is(e, ErrNotFound) {
		return 404
	}
	if e.Error() == "链接已过期" {
		return 410
	}
	if strings.Contains(e.Error(), "未就绪") {
		return 503
	}
	if strings.Contains(e.Error(), "繁忙") {
		return 429
	}
	if errors.Is(e, errPublicCursor) || strings.Contains(e.Error(), "order 只能") {
		return 400
	}
	return 502
}
func (a *App) publicMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := clampPublicLimit(q.Get("limit"))
	if q.Get("ref") != "" || q.Get("id") != "" || q.Get("message_id") != "" {
		l, values, e := a.publicRequestLookup(r.Context(), r, 1)
		if e != nil {
			respond(w, publicStatus(e), map[string]string{"error": e.Error()})
			return
		}
		respond(w, 200, map[string]any{"recipient": l.Recipient, "order": "latest", "items": values, "has_more": false, "next_cursor": ""})
		return
	}
	order := "latest"
	var e error
	if strings.TrimSpace(q.Get("cursor")) == "" {
		order, e = validPublicOrder(strings.TrimSpace(q.Get("order")))
		if e != nil {
			respond(w, publicStatus(e), map[string]string{"error": e.Error()})
			return
		}
	}
	l, page, e := a.publicPageRequest(r.Context(), r, limit, order)
	if e != nil {
		respond(w, publicStatus(e), map[string]string{"error": e.Error()})
		return
	}
	respond(w, 200, map[string]any{"recipient": l.Recipient, "order": page.Order, "items": page.Items, "next_cursor": page.NextCursor, "has_more": page.HasMore})
}
func (a *App) publicPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	order, e := validPublicOrder(strings.TrimSpace(q.Get("order")))
	if e != nil {
		order = "latest"
	}
	limit := publicPageSize
	var l Link
	var values []Message
	var page publicPage
	// Public links open on the newest message. The full mailbox is an
	// explicit list view so the first page remains quick and easy to scan.
	// Keep the old recent=1 query working, and treat a cursor/order query as
	// a list request for callers that already use the pagination API.
	hasMessageQuery := q.Get("ref") != "" || q.Get("id") != "" || q.Get("message_id") != ""
	listView := q.Get("list") == "1" || q.Get("recent") == "1" || q.Get("cursor") != "" || q.Get("order") != ""
	details := hasMessageQuery || !listView
	if details {
		l, values, e = a.publicRequestLookup(r.Context(), r, 1)
		page = publicPage{Recipient: l.Recipient, Order: order, Items: values}
	} else {
		l, page, e = a.publicPageRequest(r.Context(), r, limit, order)
		values = page.Items
	}
	status := 200
	notice := ""
	if e != nil {
		status = publicStatus(e)
		notice = e.Error()
	}
	apiPath := r.URL.Path
	if strings.HasPrefix(apiPath, "/show/") {
		apiPath = "/api/mailboxes/" + strings.TrimPrefix(apiPath, "/show/") + "/messages"
	} else if strings.HasPrefix(apiPath, "/m/") {
		apiPath = "/api/mailboxes/" + strings.TrimPrefix(apiPath, "/m/") + "/messages"
	}
	data := map[string]any{"Recipient": l.Recipient, "Messages": values, "Notice": notice, "Order": page.Order, "NextCursor": page.NextCursor, "HasMore": page.HasMore, "APIURL": apiPath, "Details": details}
	t, e := template.New("mailbox.html").Funcs(template.FuncMap{"body": emailDocument, "query": url.QueryEscape}).ParseFS(a.assets, "templates/mailbox.html")
	if e != nil {
		http.Error(w, "template error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(status)
	t.Execute(w, data)
}
