// Package server implements Ferry's HTTP API, public share pages and embedded web UI.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ferry/internal/config"
	"ferry/internal/db"
	"ferry/internal/storage"
)

const (
	APIVersion          = 1
	MinClientAPIVersion = 1
)

//go:embed all:webui
var webuiFS embed.FS

//go:embed templates
var templatesFS embed.FS

type Server struct {
	cfgp       atomic.Pointer[config.Config] // current settings; replaced when an admin saves settings
	base       *config.Config                // settings from the environment, before admin overrides
	cfgMu      sync.Mutex                    // serializes settings changes
	db         *db.DB
	store      storage.Backend
	log        *slog.Logger
	logs       *LogRing
	version    string
	started    time.Time
	secret     []byte
	tmpl       *template.Template
	limiter    *limiter
	streams    *streams
	metrics    metrics
	mux        *http.ServeMux
	scanMu     sync.Mutex
	zips       sync.Map                             // ticket → *zipTicket
	deviceSeen sync.Map                             // device ID → last last_seen write (ms)
	mail       func(to, subject, body string) error // sendMail; replaced in tests
	oidc       oidcClient
}

type metrics struct {
	requests, bytesIn, bytesOut, uploadsActive, downloadsActive, errors5xx atomic.Int64
}

func New(ctx context.Context, cfg *config.Config, d *db.DB, st storage.Backend, log *slog.Logger, logs *LogRing, version string) (*Server, error) {
	s := &Server{base: cfg, db: d, store: st, log: log, logs: logs, version: version, started: time.Now(),
		limiter: newLimiter(), streams: newStreams()}
	secret, err := s.loadSecret(ctx)
	if err != nil {
		return nil, err
	}
	s.secret = secret
	s.cfgp.Store(cfg)
	if err := s.loadSettings(ctx); err != nil {
		log.Error("saved settings are invalid; using the environment only until they are fixed in Admin → Settings", "err", err)
	}
	s.mail = s.sendMail
	s.tmpl, err = template.New("").Funcs(template.FuncMap{
		"size": humanSize,
		"ext":  fileExt,
		"kind": fileKind,
		"date": func(ms int64) string { return time.UnixMilli(ms).UTC().Format("2 Jan 2006, 15:04 UTC") },
	}).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	if err := s.bootstrapAdmin(ctx); err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

// conf returns the current settings. Read it once per use; it may be replaced at any time.
func (s *Server) conf() *config.Config { return s.cfgp.Load() }

// loadSecret returns the HMAC key used for share cookies, creating it on first start.
func (s *Server) loadSecret(ctx context.Context) ([]byte, error) {
	var v string
	err := s.db.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'secret'`).Scan(&v)
	if db.IsNoRows(err) {
		v = hex.EncodeToString(randBytes(32))
		if _, err := s.db.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('secret', ?) ON CONFLICT (key) DO NOTHING`, v); err != nil {
			return nil, err
		}
		err = s.db.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'secret'`).Scan(&v)
	}
	if err != nil {
		return nil, fmt.Errorf("load server secret: %w", err)
	}
	return hex.DecodeString(v)
}

func (s *Server) Handler() http.Handler {
	return s.recoverer(s.logRequests(s.securityHeaders(s.cors(s.mux))))
}

func (s *Server) routes() {
	m := http.NewServeMux()
	s.mux = m
	h := func(pattern string, fn http.HandlerFunc) { m.HandleFunc(pattern, fn) }
	a := func(pattern string, fn http.HandlerFunc) { m.HandleFunc(pattern, s.requireUser(s.auditAPI(fn))) }
	adm := func(pattern string, fn http.HandlerFunc) {
		m.HandleFunc(pattern, s.requireUser(s.requireAdmin(s.auditAPI(fn))))
	}

	h("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	h("GET /readyz", s.handleReady)
	h("GET /metrics", s.handleMetrics)

	h("GET /api/v1/info", s.handleInfo)
	h("GET /api/v1/setup", s.handleSetupStatus)
	h("POST /api/v1/setup", s.handleSetup)
	h("POST /api/v1/auth/login", s.handleLogin)
	h("POST /api/v1/auth/signup", s.handleSignup)
	h("POST /api/v1/auth/logout", s.handleLogout)
	h("POST /api/v1/auth/forgot", s.handleForgotPassword)
	h("POST /api/v1/auth/reset", s.handleResetPassword)
	h("POST /api/v1/auth/return", s.handleStopImpersonate)
	h("GET /api/v1/auth/oidc/start", s.handleOIDCStart)
	h("GET /api/v1/auth/oidc/callback", s.handleOIDCCallback)
	h("GET /api/v1/auth/oidc/pending", s.handleOIDCPending)
	h("DELETE /api/v1/auth/oidc/pending", s.handleOIDCClearPending)
	a("POST /api/v1/auth/oidc/link", s.handleOIDCLinkPending)
	a("GET /api/v1/me/identities", s.handleListIdentities)
	a("DELETE /api/v1/me/identities/{id}", s.handleDeleteIdentity)

	a("GET /api/v1/me", s.handleMe)
	a("PATCH /api/v1/me", s.handleUpdateMe)
	a("POST /api/v1/me/password", s.handleChangePassword)
	a("GET /api/v1/me/usage", s.handleUsage)
	a("GET /api/v1/me/sessions", s.handleListSessions)
	a("DELETE /api/v1/me/sessions/{id}", s.handleRevokeSession)
	a("POST /api/v1/me/sessions/revoke-others", s.handleRevokeOtherSessions)
	a("POST /api/v1/me/totp/setup", s.handleTOTPSetup)
	a("POST /api/v1/me/totp/enable", s.handleTOTPEnable)
	a("POST /api/v1/me/totp/disable", s.handleTOTPDisable)
	a("POST /api/v1/me/gotify/test", s.handleGotifyTest)

	a("GET /api/v1/files", s.handleListFiles)
	a("POST /api/v1/files/check", s.handleCheckNames)
	a("POST /api/v1/files/batch", s.handleBatch)
	a("GET /api/v1/files/zip", s.handleOwnerZip)
	a("POST /api/v1/files/zip", s.handleCreateZip)
	a("GET /api/v1/files/{id}", s.handleGetFile)
	a("PATCH /api/v1/files/{id}", s.handleUpdateFile)
	a("DELETE /api/v1/files/{id}", s.handleDeleteFile)
	a("GET /api/v1/files/{id}/content", s.handleFileContent)
	a("POST /api/v1/folders", s.handleCreateFolder)
	a("PATCH /api/v1/folders/{id}", s.handleUpdateFolder)
	a("DELETE /api/v1/folders/{id}", s.handleDeleteFolder)

	h("OPTIONS /api/v1/uploads", s.tusOptions)
	h("OPTIONS /api/v1/uploads/{id}", s.tusOptions)
	a("GET /api/v1/uploads", s.handleListUploads)
	a("POST /api/v1/uploads", s.tusCreateUser)
	a("HEAD /api/v1/uploads/{id}", s.tusHeadUser)
	a("PATCH /api/v1/uploads/{id}", s.tusPatchUser)
	a("DELETE /api/v1/uploads/{id}", s.tusDeleteUser)

	a("GET /api/v1/shares", s.handleListShares)
	a("POST /api/v1/shares", s.handleCreateShare)
	a("GET /api/v1/shares/{id}", s.handleGetShare)
	a("PATCH /api/v1/shares/{id}", s.handleUpdateShare)
	a("DELETE /api/v1/shares/{id}", s.handleDeleteShare)
	a("POST /api/v1/shares/{id}/regenerate", s.handleRegenerateShare)
	a("POST /api/v1/shares/{id}/email", s.handleEmailShare)
	a("POST /api/v1/shares/{id}/shorten", s.handleShortenShare)
	a("GET /api/v1/shares/{id}/analytics", s.handleShareAnalytics)

	a("GET /api/v1/devices", s.handleListDevices)
	a("PUT /api/v1/devices/current/presence", s.handlePresence)
	a("DELETE /api/v1/devices/current/presence", s.handleClearPresence)
	a("PATCH /api/v1/devices/{id}", s.handleUpdateDevice)
	a("DELETE /api/v1/devices/{id}", s.handleDeleteDevice)

	a("GET /api/v1/transfers", s.handleListTransfers)
	a("POST /api/v1/transfers", s.handleCreateTransfer)
	a("POST /api/v1/transfers/clear", s.handleClearTransfers)
	a("GET /api/v1/transfers/{id}", s.handleGetTransfer)
	a("PATCH /api/v1/transfers/{id}", s.handleUpdateTransfer)
	a("DELETE /api/v1/transfers/{id}", s.handleHideTransfer)
	a("GET /api/v1/transfers/{id}/files", s.handleTransferFiles)
	a("GET /api/v1/inbox", s.handleInbox)

	adm("GET /api/v1/admin/stats", s.handleAdminStats)
	adm("GET /api/v1/admin/users", s.handleAdminUsers)
	adm("POST /api/v1/admin/users", s.handleAdminCreateUser)
	adm("PATCH /api/v1/admin/users/{id}", s.handleAdminUpdateUser)
	adm("DELETE /api/v1/admin/users/{id}", s.handleAdminDeleteUser)
	adm("POST /api/v1/admin/users/{id}/impersonate", s.handleAdminImpersonate)
	adm("POST /api/v1/admin/users/{id}/signout", s.handleAdminSignOutUser)
	adm("DELETE /api/v1/admin/users/{id}/identities", s.handleAdminDeleteIdentities)
	adm("GET /api/v1/admin/shares", s.handleAdminShares)
	adm("POST /api/v1/admin/shares/{id}/revoke", s.handleAdminRevokeShare)
	adm("DELETE /api/v1/admin/shares/{id}", s.handleAdminDeleteShare)
	adm("POST /api/v1/admin/test-email", s.handleAdminTestEmail)
	adm("GET /api/v1/admin/settings", s.handleAdminSettings)
	adm("PATCH /api/v1/admin/settings", s.handleAdminSaveSettings)
	adm("GET /api/v1/admin/devices", s.handleAdminDevices)
	adm("DELETE /api/v1/admin/devices/{id}", s.handleAdminDeleteDevice)
	adm("GET /api/v1/admin/audit", s.handleAdminAudit)
	adm("GET /api/v1/admin/system", s.handleAdminSystem)
	adm("POST /api/v1/admin/cleanup", s.handleAdminCleanup)

	h("GET /s/{token}", s.handleSharePage)
	h("POST /s/{token}", s.handleSharePassword)
	h("GET /s/{token}/f/{fileId}", s.handleShareDownload)
	h("GET /s/{token}/zip", s.handleShareZip)
	h("GET /u/{token}", s.handleUploadPage)
	h("POST /u/{token}", s.handleUploadPost)
	h("POST /u/{token}/tus", s.tusCreateLink)
	h("OPTIONS /u/{token}/tus", s.tusOptions)
	h("HEAD /u/{token}/tus/{id}", s.tusHeadLink)
	h("PATCH /u/{token}/tus/{id}", s.tusPatchLink)
	h("DELETE /u/{token}/tus/{id}", s.tusDeleteLink)
	h("POST /u/{token}/delete/{fileId}", s.handleUploadDelete)
	h("GET /_ferry/{file}", s.handlePublicAsset)

	h("/api/", func(w http.ResponseWriter, r *http.Request) { s.writeErr(w, r, errNotFound) })
	if s.conf().WebApp {
		h("/", s.handleSPA)
	} else {
		h("GET /reset", s.handleResetPage)
		h("POST /reset", s.handleResetPage)
		h("/", func(w http.ResponseWriter, r *http.Request) {
			s.messagePage(w, 404, "Nothing here", "This server doesn't offer a web app. Use the Ferry app to sign in; shared links still open in any browser.")
		})
	}
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	caps := []string{"tus", "range", "shares", "upload-links", "one-time", "device-inbox", "presence", "zip", "transfers", "totp", "sessions", "link-analytics", "gotify"}
	if s.shortenerOn() {
		caps = append(caps, "shortener")
	}
	var sso any
	if s.conf().OIDC.Enabled() {
		caps = append(caps, "oidc")
		sso = map[string]any{"name": s.conf().OIDC.Name, "autoCreate": s.conf().OIDC.AutoCreate}
	}
	if s.conf().SMTPHost != "" {
		caps = append(caps, "email")
	}
	if s.resetEnabled() {
		caps = append(caps, "password-reset")
	}
	writeJSON(w, 200, map[string]any{
		"name":                "Ferry",
		"siteName":            s.conf().SiteName,
		"version":             s.version,
		"apiVersion":          APIVersion,
		"minClientApiVersion": MinClientAPIVersion,
		"capabilities":        caps,
		"serverTime":          nowMs(),
		"allowSignup":         s.conf().AllowSignup,
		"publicSharing":       s.conf().PublicSharing,
		"maxUploadBytes":      s.conf().MaxUploadBytes,
		"oidc":                sso,
		"webApp":              s.conf().WebApp,
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	res := map[string]string{"database": "ok", "storage": "ok"}
	status := 200
	if err := s.db.Ping(ctx); err != nil {
		res["database"] = "unavailable"
		status = 503
	}
	if err := s.store.Healthy(); err != nil {
		res["storage"] = "unavailable"
		status = 503
	}
	writeJSON(w, status, res)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.conf().MetricsToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.conf().MetricsToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	m := &s.metrics
	fmt.Fprintf(w, "ferry_http_requests_total %d\nferry_http_5xx_total %d\nferry_bytes_received_total %d\nferry_bytes_sent_total %d\nferry_uploads_active %d\nferry_downloads_active %d\nferry_uptime_seconds %d\n",
		m.requests.Load(), m.errors5xx.Load(), m.bytesIn.Load(), m.bytesOut.Load(), m.uploadsActive.Load(), m.downloadsActive.Load(), int(time.Since(s.started).Seconds()))
}

// assetVer changes whenever the public page assets do, so browsers never keep a stale cached copy.
var assetVer = func() string {
	h := sha256.New()
	for _, n := range []string{"public.js", "public.css"} {
		b, _ := templatesFS.ReadFile("templates/" + n)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}()

func (s *Server) handlePublicAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if name != "public.js" && name != "public.css" {
		http.NotFound(w, r)
		return
	}
	b, err := templatesFS.ReadFile("templates/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".js") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(b)
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sub, _ := fs.Sub(webuiFS, "webui")
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p != "" && p != "index.html" {
		if st, err := fs.Stat(sub, p); err == nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, sub, p)
			return
		}
		if path.Ext(p) != "" && !strings.Contains(p, "@") { // missing static file, don't mask with index
			http.NotFound(w, r)
			return
		}
	}
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

// ---------- middleware ----------

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ReadFrom keeps the underlying writer's zero-copy path (sendfile on Linux) for file downloads.
func (w *statusWriter) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := io.Copy(w.ResponseWriter, src)
	w.bytes += n
	return n, err
}
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		s.metrics.requests.Add(1)
		s.metrics.bytesOut.Add(sw.bytes)
		if sw.status >= 500 {
			s.metrics.errors5xx.Add(1)
		}
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		// The route pattern is logged instead of the path: share tokens in URLs are secrets.
		route := r.Pattern
		if route == "" {
			route = r.Method + " (unmatched)"
		}
		lvl := slog.LevelDebug
		if sw.status >= 500 {
			lvl = slog.LevelError
		} else if strings.HasPrefix(route, "GET /assets") || strings.HasPrefix(route, "GET /_ferry") {
			lvl = slog.LevelDebug - 1
		} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
			lvl = slog.LevelInfo
		}
		s.log.Log(r.Context(), lvl, "http", "route", route, "status", sw.status, "bytes", sw.bytes,
			"ms", time.Since(start).Milliseconds(), "ip", s.clientIP(r))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic", "route", r.Pattern, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				s.writeErr(w, r, fmt.Errorf("panic: %v", v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

const appCSP = "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; " +
	"script-src 'self' 'wasm-unsafe-eval'; connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Content-Security-Policy", appCSP)
		if s.isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		// Global per-IP limit; generous so large folder uploads (many requests) are not throttled.
		// FERRY_RATE_LIMIT=0 disables it (otherwise a limit of 0 would reject every request).
		if s.conf().RateLimitPerMinute > 0 && !s.limiter.allow("ip:"+s.clientIP(r), s.conf().RateLimitPerMinute*5, time.Minute) {
			h.Set("Retry-After", "60")
			s.writeErr(w, r, errf(429, "rate_limited", "Too many requests. Please wait a minute and try again."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	if len(s.conf().CORSOrigins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		for _, o := range s.conf().CORSOrigins {
			if o == "*" || strings.EqualFold(o, origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Vary", "Origin")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Ferry-Client, X-Requested-With, Tus-Resumable, Upload-Length, Upload-Offset, Upload-Metadata")
				h.Set("Access-Control-Expose-Headers", "Location, Upload-Offset, Upload-Length, Tus-Resumable, Ferry-File-Id, Ferry-Sha256")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, HEAD, OPTIONS")
				if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
					w.WriteHeader(204)
					return
				}
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}

// countingReader tracks inbound bytes for metrics.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// clientAPIVersion parses "X-Ferry-Client: android/1.0.0 api=1"; 0 when absent.
func clientAPIVersion(r *http.Request) int {
	for _, part := range strings.Fields(r.Header.Get("X-Ferry-Client")) {
		if v, ok := strings.CutPrefix(part, "api="); ok {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

// ---------- rate limiter ----------

// In-memory fixed windows, per instance. Use a shared store if running replicas.
type limiter struct {
	mu sync.Mutex
	m  map[string]*window
}
type window struct {
	start time.Time
	per   time.Duration
	n     int
}

func newLimiter() *limiter { return &limiter{m: map[string]*window{}} }

// allow counts one hit for key and reports whether it is within max per window.
func (l *limiter) allow(key string, max int, per time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w := l.m[key]
	if w == nil || now.Sub(w.start) >= w.per {
		w = &window{start: now, per: per}
		l.m[key] = w
	}
	w.n++
	return w.n <= max
}

// count returns hits in the current window without counting a new one.
func (l *limiter) count(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w := l.m[key]; w != nil && time.Since(w.start) < w.per {
		return w.n
	}
	return 0
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}

func (l *limiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, w := range l.m {
		if now.Sub(w.start) >= w.per {
			delete(l.m, k)
		}
	}
}

// ---------- active download registry (for immediate revocation) ----------

type streams struct {
	mu   sync.Mutex
	next int64
	m    map[string]map[int64]context.CancelFunc
}

func newStreams() *streams { return &streams{m: map[string]map[int64]context.CancelFunc{}} }

// add registers an active stream for key (a share ID or user ID) and returns its unregister func.
// In-process only; with multiple replicas revocation would also need a pub/sub fan-out.
func (st *streams) add(keys []string, cancel context.CancelFunc) func() {
	st.mu.Lock()
	st.next++
	id := st.next
	for _, k := range keys {
		if st.m[k] == nil {
			st.m[k] = map[int64]context.CancelFunc{}
		}
		st.m[k][id] = cancel
	}
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		for _, k := range keys {
			delete(st.m[k], id)
			if len(st.m[k]) == 0 {
				delete(st.m, k)
			}
		}
		st.mu.Unlock()
	}
}

func (st *streams) cancel(key string) {
	st.mu.Lock()
	for _, c := range st.m[key] {
		c()
	}
	st.mu.Unlock()
}

// ---------- log ring for the admin UI ----------

type LogRing struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func NewLogRing(max int) *LogRing { return &LogRing{max: max} }

func (l *LogRing) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.lines = append(l.lines, strings.TrimRight(string(p), "\n"))
	if len(l.lines) > l.max {
		l.lines = l.lines[len(l.lines)-l.max:]
	}
	l.mu.Unlock()
	return len(p), nil
}

func (l *LogRing) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}
