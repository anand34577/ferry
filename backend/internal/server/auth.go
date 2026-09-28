package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ferry/internal/db"
)

type User struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Disabled   bool   `json:"disabled"`
	QuotaBytes int64  `json:"quotaBytes"` // -1 = server default, 0 = unlimited
	CreatedAt  int64  `json:"createdAt"`
	TOTP       bool   `json:"totpEnabled"`
}

type authInfo struct {
	user      *User
	sessionID string
	deviceID  string
	cookie    bool
}

type ctxKey int

const authKey ctxKey = 1

const sessionCookie = "ferry_session"

// dummyHash is compared against when an email does not exist, so response time does not reveal it.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("ferry-timing-equaliser"), 12)

func authOf(r *http.Request) *authInfo {
	a, _ := r.Context().Value(authKey).(*authInfo)
	return a
}

func userOf(r *http.Request) *User { return authOf(r).user }

const userCols = `id, email, name, role, disabled, quota_bytes, created_at, totp_enabled`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, boolScan{&u.Disabled}, &u.QuotaBytes, &u.CreatedAt, boolScan{&u.TOTP}); err != nil {
		return nil, err
	}
	return u, nil
}

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

func checkPassword(hash, pw string) bool {
	return hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func validatePassword(pw string) error {
	if len(pw) < 8 {
		return errf(400, "weak_password", "Password must be at least 8 characters.")
	}
	if len(pw) > 72 {
		return errf(400, "weak_password", "Password must be at most 72 characters.")
	}
	return nil
}

func normalizeEmail(e string) (string, error) {
	e = strings.ToLower(strings.TrimSpace(e))
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e {
		return "", errf(400, "invalid_email", "Please enter a valid email address.")
	}
	return e, nil
}

// createUser inserts a user; used by setup, signup, admin and CLI.
func CreateUser(ctx context.Context, q db.Q, email, name, password, role string) (*User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	if len(name) > 100 {
		name = clip(name, 100)
	}
	h, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &User{ID: newID(), Email: email, Name: name, Role: role, QuotaBytes: -1, CreatedAt: nowMs()}
	_, err = q.Exec(ctx, `INSERT INTO users (id, email, name, password_hash, role, disabled, quota_bytes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, -1, ?, ?)`,
		u.ID, u.Email, u.Name, h, u.Role, u.CreatedAt, u.CreatedAt)
	if db.IsUnique(err) {
		return nil, errf(409, "email_taken", "An account with this email already exists.")
	}
	return u, err
}

// SetPassword changes a user's password and signs out all their sessions except keepSession.
func SetPassword(ctx context.Context, q db.Q, userID, password, keepSession string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	h, err := hashPassword(password)
	if err != nil {
		return err
	}
	res, err := q.Exec(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, h, nowMs(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	_, err = q.Exec(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keepSession)
	return err
}

func (s *Server) bootstrapAdmin(ctx context.Context) error {
	if s.conf().AdminEmail == "" {
		return nil
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := CreateUser(ctx, s.db, s.conf().AdminEmail, "Admin", s.conf().AdminPassword, "admin")
	if err == nil {
		s.log.Info("created initial admin from FERRY_ADMIN_EMAIL", "email", s.conf().AdminEmail)
	}
	return err
}

func (s *Server) audit(ctx context.Context, r *http.Request, userID, action, target, detail string) {
	ip := ""
	if r != nil {
		ip = s.clientIP(r)
	}
	if f, ok := ctx.Value(auditKey).(*auditFlag); ok {
		f.done = true
	}
	if r != nil {
		if c, err := r.Cookie(adminReturnCookie); err == nil && c.Value != "" {
			detail = strings.TrimSpace(detail + " [by admin while signed in as this user]")
		}
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO audit_log (id, at, user_id, action, target, ip, detail) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newID(), nowMs(), userID, action, target, ip, detail); err != nil {
		s.log.Warn("audit write failed", "err", err)
	}
}

// ---------- middleware ----------

func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v := clientAPIVersion(r); v != 0 && v < MinClientAPIVersion {
			s.writeErr(w, r, errf(426, "client_outdated", "This app version is too old for this server. Please update the app."))
			return
		}
		a, err := s.authenticate(r)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		// CSRF: cookie-authenticated mutations must carry a header a cross-site form cannot set.
		if a.cookie && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions &&
			r.Header.Get("X-Requested-With") != "ferry" {
			s.writeErr(w, r, errf(403, "csrf", "Request blocked for security reasons. Please reload the page."))
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authKey, a)))
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if userOf(r).Role != "admin" {
			s.writeErr(w, r, errForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) sessionToken(r *http.Request) (string, bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:]), false
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value, true
	}
	return "", false
}

func (s *Server) authenticate(r *http.Request) (*authInfo, error) {
	tok, cookie := s.sessionToken(r)
	return s.authToken(r.Context(), tok, cookie)
}

func (s *Server) authToken(ctx context.Context, tok string, cookie bool) (*authInfo, error) {
	if tok == "" {
		return nil, errUnauthorized
	}
	sid := hashToken(tok)
	var deviceID string
	var expires, lastSeen int64
	row := s.db.QueryRow(ctx, `SELECT s.device_id, s.expires_at, s.last_seen, u.id, u.email, u.name, u.role, u.disabled, u.quota_bytes, u.created_at, u.totp_enabled
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ?`, sid)
	u := &User{}
	var disabled int
	err := row.Scan(&deviceID, &expires, &lastSeen, &u.ID, &u.Email, &u.Name, &u.Role, &disabled, &u.QuotaBytes, &u.CreatedAt, boolScan{&u.TOTP})
	if db.IsNoRows(err) {
		return nil, errf(401, "session_expired", "Your session has ended. Please sign in again.")
	}
	if err != nil {
		return nil, err
	}
	now := nowMs()
	if expires < now {
		s.db.Exec(ctx, `DELETE FROM sessions WHERE id = ?`, sid)
		return nil, errf(401, "session_expired", "Your session has ended. Please sign in again.")
	}
	if disabled == 1 {
		return nil, errf(403, "account_disabled", "This account has been disabled. Contact your administrator.")
	}
	// Sliding expiry, written at most every 5 minutes to keep writes low.
	if now-lastSeen > 5*60*1000 {
		s.db.Exec(ctx, `UPDATE sessions SET last_seen = ?, expires_at = ? WHERE id = ?`, now, now+s.conf().SessionTTL.Milliseconds(), sid)
	}
	// Devices: at most every 30 s, so "online" reflects an open app (it checks in every 15 s).
	if deviceID != "" {
		if last, ok := s.deviceSeen.Load(deviceID); !ok || now-last.(int64) > 30*1000 {
			s.deviceSeen.Store(deviceID, now)
			s.db.Exec(ctx, `UPDATE devices SET last_seen = ? WHERE id = ?`, now, deviceID)
		}
	}
	return &authInfo{user: u, sessionID: sid, deviceID: deviceID, cookie: cookie}, nil
}

// ---------- handlers ----------

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	var n int
	if err := s.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"needed": n == 0})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Code     string `json:"code"` // two-factor code, when the account has 2FA enabled
	// Apps pass a device; the web UI does not and gets a cookie instead of a token.
	Device *struct {
		Name       string `json:"name"`
		Platform   string `json:"platform"`
		AppVersion string `json:"appVersion"`
		DeviceID   string `json:"deviceId"` // re-login of an existing device keeps its identity
	} `json:"device"`
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	// On a fresh server the first visitor becomes the administrator. Only accept that from the local
	// network, so a server that is already reachable from the internet can't be claimed by a stranger.
	if !s.fromLocalNetwork(r) {
		s.writeErr(w, r, errf(403, "setup_local_only", "For security, the first administrator account can only be created from the local network (or set FERRY_ADMIN_EMAIL and FERRY_ADMIN_PASSWORD)."))
		return
	}
	var req loginReq
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var u *User
	err := s.db.InTx(r.Context(), func(tx *db.Tx) error {
		var n int
		if err := tx.QueryRow(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errf(409, "already_setup", "Setup has already been completed. Please sign in.")
		}
		var err error
		u, err = CreateUser(r.Context(), tx, req.Email, req.Name, req.Password, "admin")
		return err
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "setup", u.Email, "initial admin created")
	s.startSession(w, r, u, &req)
}

// fromLocalNetwork reports whether the visitor is on the server's own network. Behind a reverse proxy or
// tunnel that isn't listed in FERRY_TRUSTED_PROXIES the connection itself looks local, so every address
// the proxy forwarded must be local too; a proxy appends the real client, which can't be forged away.
func (s *Server) fromLocalNetwork(r *http.Request) bool {
	local := func(v string) bool {
		ip := net.ParseIP(strings.TrimSpace(v))
		return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
	}
	if !local(s.clientIP(r)) {
		return false
	}
	var forwarded []string
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip"} {
		for _, v := range r.Header.Values(h) {
			forwarded = append(forwarded, strings.Split(v, ",")...)
		}
	}
	for _, v := range r.Header.Values("Forwarded") { // RFC 7239: for=1.2.3.4;proto=https, for="[2001:db8::1]:443"
		for _, part := range strings.FieldsFunc(v, func(c rune) bool { return c == ',' || c == ';' }) {
			if k, f, _ := strings.Cut(strings.TrimSpace(part), "="); strings.EqualFold(k, "for") {
				f = strings.Trim(f, `"`)
				if h, _, err := net.SplitHostPort(f); err == nil {
					f = h
				}
				forwarded = append(forwarded, strings.Trim(f, "[]"))
			}
		}
	}
	for _, v := range forwarded {
		if strings.TrimSpace(v) != "" && !local(v) {
			return false
		}
	}
	return true
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if !s.conf().AllowSignup {
		s.writeErr(w, r, errf(403, "signup_disabled", "Sign-up is disabled on this server. Ask the administrator for an account."))
		return
	}
	if !s.limiter.allow("signup:"+s.clientIP(r), 5, time.Hour) {
		s.writeErr(w, r, errf(429, "rate_limited", "Too many sign-ups from your network. Please try again later."))
		return
	}
	var req loginReq
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u, err := CreateUser(r.Context(), s.db, req.Email, req.Name, req.Password, "user")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "signup", u.Email, "")
	s.startSession(w, r, u, &req)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.limiter.allow("login:"+ip, 20, time.Minute) {
		s.writeErr(w, r, errf(429, "rate_limited", "Too many sign-in attempts. Please wait a minute and try again."))
		return
	}
	var req loginReq
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	// Keyed by email *and* IP: keyed by email alone, anyone could keep any account (even the admin)
	// locked out forever. Distributed guessing is still slowed by bcrypt and the per-IP limit above.
	failKey := "loginfail:" + email + "|" + ip
	if s.limiter.count(failKey) >= 5 {
		s.writeErr(w, r, errf(429, "locked", "Too many failed attempts for this account. Please try again in 15 minutes."))
		return
	}
	var hash, secret string
	var disabled, totpOn int
	var id string
	var lastStep int64
	err := s.db.QueryRow(r.Context(), `SELECT id, password_hash, disabled, totp_enabled, totp_secret, totp_last_step FROM users WHERE email = ?`, email).
		Scan(&id, &hash, &disabled, &totpOn, &secret, &lastStep)
	if err != nil && !db.IsNoRows(err) {
		s.writeErr(w, r, err)
		return
	}
	if err != nil || !checkPassword(hash, req.Password) {
		if err != nil { // equalise timing so emails cannot be enumerated
			bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		}
		s.limiter.allow(failKey, 5, 15*time.Minute)
		s.audit(r.Context(), r, id, "login_failed", email, "")
		s.writeErr(w, r, errf(401, "invalid_credentials", "Email or password is incorrect."))
		return
	}
	if disabled == 1 {
		s.writeErr(w, r, errf(403, "account_disabled", "This account has been disabled. Contact your administrator."))
		return
	}
	if totpOn == 1 {
		if strings.TrimSpace(req.Code) == "" {
			s.writeErr(w, r, errf(401, "totp_required", "Enter the 6-digit code from your authenticator app."))
			return
		}
		if !s.useTOTP(r.Context(), id, secret, req.Code, lastStep) {
			s.limiter.allow(failKey, 5, 15*time.Minute)
			s.audit(r.Context(), r, id, "login_failed", email, "wrong two-factor code")
			s.writeErr(w, r, errf(401, "invalid_code", "That code is incorrect or was already used. Try the next code."))
			return
		}
	}
	s.limiter.reset(failKey)
	u, err := scanUser(s.db.QueryRow(r.Context(), `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "login", u.Email, "")
	s.startSession(w, r, u, &req)
}

// startSession creates a session. Web: HttpOnly cookie. Apps: bearer token + device registration.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *User, req *loginReq) {
	ctx := r.Context()
	tok := newToken(32)
	now := nowMs()
	deviceID := ""
	var device map[string]any
	if req.Device != nil {
		name := cleanName(req.Device.Name)
		if req.Device.Name == "" {
			name = "Device"
		}
		platform := strings.ToLower(strings.TrimSpace(req.Device.Platform))
		if platform == "" {
			platform = "unknown"
		}
		// Re-use an existing device record when the app re-logs in, so pairing and history survive.
		if req.Device.DeviceID != "" {
			var owner string
			if err := s.db.QueryRow(ctx, `SELECT user_id FROM devices WHERE id = ?`, req.Device.DeviceID).Scan(&owner); err == nil && owner == u.ID {
				deviceID = req.Device.DeviceID
				s.db.Exec(ctx, `UPDATE devices SET name = ?, platform = ?, app_version = ?, last_seen = ? WHERE id = ?`, name, platform, req.Device.AppVersion, now, deviceID)
			}
		}
		if deviceID == "" {
			deviceID = newID()
			if _, err := s.db.Exec(ctx, `INSERT INTO devices (id, user_id, name, platform, app_version, last_seen, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				deviceID, u.ID, name, platform, req.Device.AppVersion, now, now); err != nil {
				s.writeErr(w, r, err)
				return
			}
		}
		device = map[string]any{"id": deviceID, "name": name, "platform": platform}
	}
	if err := s.insertSession(ctx, r, tok, u.ID, deviceID, s.conf().SessionTTL, r.UserAgent()); err != nil {
		s.writeErr(w, r, err)
		return
	}
	resp := map[string]any{"user": u}
	if req.Device != nil {
		resp["token"] = tok
		resp["device"] = device
	} else {
		s.setSessionCookie(w, r, tok, s.conf().SessionTTL)
	}
	writeJSON(w, 200, resp)
}

func (s *Server) insertSession(ctx context.Context, r *http.Request, tok, userID, deviceID string, ttl time.Duration, ua string) error {
	now := nowMs()
	_, err := s.db.Exec(ctx, `INSERT INTO sessions (id, user_id, device_id, created_at, expires_at, last_seen, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		hashToken(tok), userID, deviceID, now, now+ttl.Milliseconds(), now, s.clientIP(r), clip(ua, 250))
	return err
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, tok string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: s.secureCookie(r),
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
}

// webSession signs a browser in (used by SSO).
func (s *Server) webSession(w http.ResponseWriter, r *http.Request, u *User) error {
	tok := newToken(32)
	if err := s.insertSession(r.Context(), r, tok, u.ID, "", s.conf().SessionTTL, r.UserAgent()); err != nil {
		return err
	}
	s.setSessionCookie(w, r, tok, s.conf().SessionTTL)
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok, _ := s.sessionToken(r); tok != "" {
		s.db.Exec(r.Context(), `DELETE FROM sessions WHERE id = ?`, hashToken(tok))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	var gURL, gTok, pwHash, prefs string
	var gInsecure int
	s.db.QueryRow(r.Context(), `SELECT gotify_url, gotify_token, gotify_skip_verify, password_hash, prefs FROM users WHERE id = ?`, a.user.ID).
		Scan(&gURL, &gTok, &gInsecure, &pwHash, &prefs)
	if !json.Valid([]byte(prefs)) {
		prefs = "{}"
	}
	resp := map[string]any{"user": a.user, "deviceId": a.deviceID, "gotifyUrl": gURL, "gotifyConfigured": gURL != "" && gTok != "",
		"gotifySkipVerify": gInsecure == 1, "hasPassword": pwHash != "", "prefs": json.RawMessage(prefs)}
	if c, err := r.Cookie(adminReturnCookie); err == nil {
		if adm, err := s.authToken(r.Context(), c.Value, true); err == nil && adm.user.Role == "admin" {
			resp["impersonator"] = adm.user.Email
		}
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name             *string `json:"name"`
		GotifyURL        *string `json:"gotifyUrl"`
		GotifyToken      *string `json:"gotifyToken"`
		GotifySkipVerify *bool   `json:"gotifySkipVerify"`
		// Prefs are merged into the saved ones (theme, sort order, …) so every browser looks the same.
		Prefs map[string]json.RawMessage `json:"prefs"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u := userOf(r)
	ctx := r.Context()
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 100 {
			s.writeErr(w, r, errf(400, "invalid_name", "Name must be between 1 and 100 characters."))
			return
		}
		if _, err := s.db.Exec(ctx, `UPDATE users SET name = ?, updated_at = ? WHERE id = ?`, name, nowMs(), u.ID); err != nil {
			s.writeErr(w, r, err)
			return
		}
		u.Name = name
	}
	if req.Prefs != nil && req.Name == nil && req.GotifyURL == nil {
		if f, ok := ctx.Value(auditKey).(*auditFlag); ok {
			f.done = true // appearance and sort preferences aren't worth an audit entry
		}
	}
	if req.Prefs != nil {
		var cur string
		s.db.QueryRow(ctx, `SELECT prefs FROM users WHERE id = ?`, u.ID).Scan(&cur)
		merged := map[string]json.RawMessage{}
		json.Unmarshal([]byte(cur), &merged)
		for k, v := range req.Prefs {
			if string(v) == "null" {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		b, _ := json.Marshal(merged)
		if len(b) > 8<<10 || len(merged) > 50 {
			s.writeErr(w, r, errf(400, "prefs_too_large", "Too many preferences."))
			return
		}
		if _, err := s.db.Exec(ctx, `UPDATE users SET prefs = ? WHERE id = ?`, string(b), u.ID); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	if req.GotifyURL != nil {
		gURL, err := cleanGotifyURL(*req.GotifyURL)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		q, args := `UPDATE users SET gotify_url = ? WHERE id = ?`, []any{gURL, u.ID}
		if req.GotifyToken != nil || gURL == "" { // clearing the URL also forgets the token
			tok := ""
			if req.GotifyToken != nil && gURL != "" {
				tok = strings.TrimSpace(*req.GotifyToken)
			}
			if len(tok) > 200 || strings.ContainsAny(tok, " \t\r\n") {
				s.writeErr(w, r, errf(400, "invalid_token", "That doesn't look like a Gotify application token."))
				return
			}
			q, args = `UPDATE users SET gotify_url = ?, gotify_token = ? WHERE id = ?`, []any{gURL, tok, u.ID}
		}
		if _, err := s.db.Exec(ctx, q, args...); err != nil {
			s.writeErr(w, r, err)
			return
		}
		s.audit(ctx, r, u.ID, "gotify_updated", "", gURL)
	}
	if req.GotifySkipVerify != nil {
		if _, err := s.db.Exec(ctx, `UPDATE users SET gotify_skip_verify = ? WHERE id = ?`, boolInt(*req.GotifySkipVerify), u.ID); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"user": u})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	a := authOf(r)
	if !s.limiter.allow("pwchange:"+a.user.ID, 10, 15*time.Minute) {
		s.writeErr(w, r, errf(429, "rate_limited", "Too many attempts. Please try again later."))
		return
	}
	var hash string
	if err := s.db.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, a.user.ID).Scan(&hash); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if hash != "" && !checkPassword(hash, req.Current) { // accounts created by SSO have no password yet
		s.writeErr(w, r, errf(400, "wrong_password", "Your current password is incorrect."))
		return
	}
	if err := SetPassword(r.Context(), s.db, a.user.ID, req.New, a.sessionID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, a.user.ID, "password_changed", a.user.Email, "other sessions signed out")
	writeJSON(w, 200, map[string]bool{"ok": true})
}
