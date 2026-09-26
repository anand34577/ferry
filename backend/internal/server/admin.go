package server

import (
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := nowMs()
	var users, files, activeShares, devices, onlineDevices, uploads, transfersActive int
	var used int64
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&users)
	s.db.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM files`).Scan(&files, &used)
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM shares WHERE revoked = 0 AND (expires_at = 0 OR expires_at > ?)`, now).Scan(&activeShares)
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM devices`).Scan(&devices)
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM devices WHERE last_seen > ?`, now-10*60*1000).Scan(&onlineDevices)
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM uploads WHERE file_id = ''`).Scan(&uploads)
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM transfers WHERE status IN ('waiting','negotiating','connecting','transferring','verifying')`).Scan(&transfersActive)
	total, free, ok := s.store.Usage()
	resp := map[string]any{
		"users": users, "files": files, "usedBytes": used, "activeShares": activeShares, "devices": devices,
		"onlineDevices": onlineDevices, "pendingUploads": uploads, "activeTransfers": transfersActive,
		"activeDownloads": s.metrics.downloadsActive.Load(), "activeUploads": s.metrics.uploadsActive.Load(),
		"globalQuotaBytes": s.conf().GlobalQuota, "storageHealthy": s.store.Healthy() == nil,
	}
	if ok {
		resp["diskTotalBytes"], resp["diskFreeBytes"] = total, free
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT u.id, u.email, u.name, u.role, u.disabled, u.quota_bytes, u.created_at, u.totp_enabled,
		COALESCE((SELECT SUM(size) FROM files f WHERE f.user_id = u.id), 0),
		COALESCE((SELECT COUNT(*) FROM files f WHERE f.user_id = u.id), 0),
		COALESCE((SELECT MAX(last_seen) FROM sessions se WHERE se.user_id = u.id), 0),
		(SELECT COUNT(*) FROM user_identities i WHERE i.user_id = u.id), CASE WHEN u.password_hash = '' THEN 0 ELSE 1 END
		FROM users u ORDER BY u.created_at`)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	type row struct {
		*User
		UsedBytes      int64 `json:"usedBytes"`
		FileCount      int   `json:"fileCount"`
		LastActive     int64 `json:"lastActive"`
		EffectiveQuota int64 `json:"effectiveQuotaBytes"`
		SSO            int   `json:"ssoLinks"`
		HasPassword    bool  `json:"hasPassword"`
	}
	out := []row{}
	for rows.Next() {
		u := &User{}
		var dis int
		var rw row
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &dis, &u.QuotaBytes, &u.CreatedAt, boolScan{&u.TOTP}, &rw.UsedBytes, &rw.FileCount, &rw.LastActive, &rw.SSO, boolScan{&rw.HasPassword}); err != nil {
			s.writeErr(w, r, err)
			return
		}
		u.Disabled = dis == 1
		rw.User = u
		rw.EffectiveQuota = s.userQuota(u)
		out = append(out, rw)
	}
	writeJSON(w, 200, map[string]any{"users": out, "defaultQuotaBytes": s.conf().DefaultUserQuota})
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email      string `json:"email"`
		Name       string `json:"name"`
		Password   string `json:"password"`
		Role       string `json:"role"`
		QuotaBytes *int64 `json:"quotaBytes"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.Role != "admin" {
		req.Role = "user"
	}
	u, err := CreateUser(r.Context(), s.db, req.Email, req.Name, req.Password, req.Role)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.QuotaBytes != nil && *req.QuotaBytes >= -1 {
		s.db.Exec(r.Context(), `UPDATE users SET quota_bytes = ? WHERE id = ?`, *req.QuotaBytes, u.ID)
		u.QuotaBytes = *req.QuotaBytes
	}
	s.audit(r.Context(), r, userOf(r).ID, "admin_user_created", u.ID, u.Email)
	writeJSON(w, 201, u)
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userOf(r)
	id := r.PathValue("id")
	var req struct {
		Name        *string `json:"name"`
		Role        *string `json:"role"`
		Disabled    *bool   `json:"disabled"`
		QuotaBytes  *int64  `json:"quotaBytes"`
		Password    *string `json:"password"`
		DisableTOTP bool    `json:"disableTotp"` // recovery when a user lost their authenticator
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err != nil {
		s.writeErr(w, r, errNotFound)
		return
	}
	if id == me.ID && ((req.Role != nil && *req.Role != "admin") || (req.Disabled != nil && *req.Disabled)) {
		s.writeErr(w, r, errf(400, "self_lockout", "You can't remove your own admin access or disable yourself."))
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		u.Name = strings.TrimSpace(*req.Name)
		if len(u.Name) > 100 {
			s.writeErr(w, r, errf(400, "invalid_name", "Name must be between 1 and 100 characters."))
			return
		}
	}
	if req.Role != nil && (*req.Role == "admin" || *req.Role == "user") {
		u.Role = *req.Role
	}
	if req.Disabled != nil {
		u.Disabled = *req.Disabled
	}
	if req.QuotaBytes != nil && *req.QuotaBytes >= -1 {
		u.QuotaBytes = *req.QuotaBytes
	}
	if _, err := s.db.Exec(ctx, `UPDATE users SET name = ?, role = ?, disabled = ?, quota_bytes = ?, updated_at = ? WHERE id = ?`,
		u.Name, u.Role, boolInt(u.Disabled), u.QuotaBytes, nowMs(), u.ID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if u.Disabled {
		s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = ?`, u.ID)
		s.streams.cancel("user:" + u.ID) // their public links stop immediately too
	}
	if req.Password != nil && *req.Password != "" {
		if err := SetPassword(ctx, s.db, u.ID, *req.Password, ""); err != nil {
			s.writeErr(w, r, err)
			return
		}
		s.audit(ctx, r, me.ID, "admin_password_reset", u.ID, u.Email)
	}
	if req.DisableTOTP && u.TOTP {
		if err := disableTOTP(ctx, s.db, u.ID); err != nil {
			s.writeErr(w, r, err)
			return
		}
		u.TOTP = false
		s.audit(ctx, r, me.ID, "admin_2fa_disabled", u.ID, u.Email)
	}
	s.audit(ctx, r, me.ID, "admin_user_updated", u.ID, u.Email)
	writeJSON(w, 200, u)
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	me := userOf(r)
	if id == me.ID {
		s.writeErr(w, r, errf(400, "self_delete", "You can't delete your own account."))
		return
	}
	u, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err != nil {
		s.writeErr(w, r, errNotFound)
		return
	}
	s.streams.cancel("user:" + id)
	// Collect blobs first; rows cascade with the user.
	files, _ := scanFiles(s.db.Query(ctx, `SELECT `+fileCols+` FROM files WHERE user_id = ?`, id))
	var ups []string
	if rows, err := s.db.Query(ctx, `SELECT id FROM uploads WHERE user_id = ? AND file_id = ''`, id); err == nil {
		for rows.Next() {
			var x string
			rows.Scan(&x)
			ups = append(ups, x)
		}
		rows.Close()
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	// Explicit deletes for tables without FK cascade (and for SQLite builds without FK enforcement).
	for _, q := range []string{`DELETE FROM files WHERE user_id = ?`, `DELETE FROM folders WHERE user_id = ?`, `DELETE FROM shares WHERE user_id = ?`,
		`DELETE FROM devices WHERE user_id = ?`, `DELETE FROM sessions WHERE user_id = ?`, `DELETE FROM transfers WHERE user_id = ?`, `DELETE FROM uploads WHERE user_id = ?`} {
		s.db.Exec(ctx, q, id)
	}
	for _, f := range files {
		s.store.Remove("blobs/" + f.Blob)
	}
	for _, x := range ups {
		s.store.Remove("uploads/" + x)
	}
	s.audit(ctx, r, me.ID, "admin_user_deleted", id, u.Email)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAdminShares(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT `+shareCols+`, u.email FROM shares s JOIN users u ON u.id = s.user_id ORDER BY s.created_at DESC LIMIT 1000`)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	type row struct {
		*Share
		OwnerEmail string `json:"ownerEmail"`
	}
	out := []row{}
	for rows.Next() {
		var sh *Share
		var email string
		if err := rows.Scan(scanDest(&sh, &email)...); err != nil {
			s.writeErr(w, r, err)
			return
		}
		sh.HasPassword = sh.passwordHash != ""
		sh.Status = sh.status()
		sh.URL = s.shareURL(r, sh)
		out = append(out, row{sh, email})
	}
	writeJSON(w, 200, map[string]any{"shares": out})
}

func (s *Server) handleAdminRevokeShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.db.Exec(r.Context(), `UPDATE shares SET revoked = 1, updated_at = ? WHERE id = ?`, nowMs(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeErr(w, r, errNotFound)
		return
	}
	s.streams.cancel(id)
	s.audit(r.Context(), r, userOf(r).ID, "admin_share_revoked", id, "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAdminDevices(w http.ResponseWriter, r *http.Request) {
	devs, err := s.listDevices(r, "")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"devices": devs})
}

func (s *Server) handleAdminDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.revokeDevice(r, r.PathValue("id"), ""); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, userOf(r).ID, "admin_device_revoked", r.PathValue("id"), "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAdminSystem(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	c := s.conf()
	writeJSON(w, 200, map[string]any{
		"version": s.version, "goVersion": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"uptimeSeconds": int(time.Since(s.started).Seconds()), "memoryBytes": ms.Alloc, "goroutines": runtime.NumGoroutine(),
		"config": map[string]any{ // secrets deliberately omitted
			"siteName": c.SiteName, "publicUrl": c.PublicURL, "dbDriver": c.DBDriver, "storagePath": c.StoragePath,
			"tls": c.TLSCert != "", "allowSignup": c.AllowSignup, "publicSharing": c.PublicSharing,
			"maxUploadBytes": c.MaxUploadBytes, "defaultUserQuotaBytes": c.DefaultUserQuota, "globalQuotaBytes": c.GlobalQuota,
			"maxShareBytes": c.MaxShareBytes, "maxShareFiles": c.MaxShareFiles, "sessionTtl": c.SessionTTL.String(),
			"uploadExpiry": c.UploadExpiry.String(), "downloadWindow": c.DownloadWindow.String(), "cleanupInterval": c.CleanupInterval.String(),
			"rateLimitPerMinute": c.RateLimitPerMinute, "smtp": c.SMTPHost != "", "scanner": c.ScanCommand != "",
			"trustedProxies": len(c.TrustedProxies), "corsOrigins": c.CORSOrigins,
			"oidcIssuer": c.OIDC.Issuer, "oidcAutoCreate": c.OIDC.AutoCreate, "oidcLinkByEmail": c.OIDC.LinkByEmail, "oidcAdminGroup": c.OIDC.AdminGroup,
		},
		"logs": s.logs.Lines(),
	})
}

func (s *Server) handleAdminCleanup(w http.ResponseWriter, r *http.Request) {
	res := s.Cleanup(r.Context())
	s.audit(r.Context(), r, userOf(r).ID, "admin_cleanup", "", "")
	writeJSON(w, 200, res)
}

// ---------- admin powers: sign in as a user, links, sessions, email test ----------

const adminReturnCookie = "ferry_admin_return"

// handleAdminImpersonate signs the admin in as another user (web only). The admin's own session is
// kept in a separate HttpOnly cookie so "Return to admin" restores it; everything done meanwhile is
// audited on the user's account and tagged as an admin session.
func (s *Server) handleAdminImpersonate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := authOf(r)
	if !a.cookie {
		s.writeErr(w, r, errf(400, "web_only", "Signing in as a user is only available in the web app."))
		return
	}
	if _, err := r.Cookie(adminReturnCookie); err == nil {
		s.writeErr(w, r, errf(400, "already_impersonating", "Return to your admin account first."))
		return
	}
	target, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, r.PathValue("id")))
	if err != nil {
		s.writeErr(w, r, errNotFound)
		return
	}
	if target.ID == a.user.ID || target.Disabled {
		s.writeErr(w, r, errf(400, "cannot_impersonate", "You can only sign in as another, enabled account."))
		return
	}
	adminTok, _ := s.sessionToken(r)
	tok := newToken(32)
	ttl := time.Hour
	if err := s.insertSession(ctx, r, tok, target.ID, "", ttl, "Admin session ("+a.user.Email+")"); err != nil {
		s.writeErr(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: adminReturnCookie, Value: adminTok, Path: "/", HttpOnly: true, Secure: s.secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
	s.setSessionCookie(w, r, tok, ttl)
	s.audit(ctx, r, a.user.ID, "admin_signed_in_as", target.ID, target.Email)
	writeJSON(w, 200, map[string]any{"user": target})
}

// handleStopImpersonate ends an admin's "sign in as" session and restores the admin session.
func (s *Server) handleStopImpersonate(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(adminReturnCookie)
	if err != nil {
		s.writeErr(w, r, errf(400, "not_impersonating", "You are not signed in as another user."))
		return
	}
	if r.Header.Get("X-Requested-With") != "ferry" {
		s.writeErr(w, r, errf(403, "csrf", "Request blocked for security reasons. Please reload the page."))
		return
	}
	secure := s.secureCookie(r)
	http.SetCookie(w, &http.Cookie{Name: adminReturnCookie, Value: "", Path: "/", HttpOnly: true, Secure: secure, MaxAge: -1})
	if tok, _ := s.sessionToken(r); tok != "" {
		s.db.Exec(r.Context(), `DELETE FROM sessions WHERE id = ?`, hashToken(tok))
	}
	adm, err := s.authToken(r.Context(), c.Value, true)
	if err != nil || adm.user.Role != "admin" {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
		s.writeErr(w, r, errf(401, "session_expired", "Your admin session has ended. Please sign in again."))
		return
	}
	s.setSessionCookie(w, r, c.Value, s.conf().SessionTTL)
	s.audit(r.Context(), nil, adm.user.ID, "admin_returned", "", "")
	writeJSON(w, 200, map[string]any{"user": adm.user})
}

func (s *Server) handleAdminDeleteShare(w http.ResponseWriter, r *http.Request) {
	sh, err := scanShare(s.db.QueryRow(r.Context(), `SELECT `+shareCols+` FROM shares s WHERE s.id = ?`, r.PathValue("id")))
	if err != nil {
		s.writeErr(w, r, errNotFound)
		return
	}
	if err := s.deleteShare(r.Context(), sh); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, userOf(r).ID, "admin_share_deleted", sh.ID, sh.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAdminSignOutUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, id, authOf(r).sessionID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	n, _ := res.RowsAffected()
	s.streams.cancel("user:" + id)
	s.audit(r.Context(), r, userOf(r).ID, "admin_user_signed_out", id, strconv.FormatInt(n, 10)+" session(s)")
	writeJSON(w, 200, map[string]any{"ok": true, "sessions": n})
}

// handleAdminTestEmail sends a test message to the admin; the SMTP error is shown verbatim to help setup.
func (s *Server) handleAdminTestEmail(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if s.conf().SMTPHost == "" {
		s.writeErr(w, r, errf(400, "email_disabled", "Email is not configured. Set FERRY_SMTP_HOST and restart."))
		return
	}
	if err := s.mail(u.Email, s.conf().SiteName+" test email", "Email delivery from "+s.conf().SiteName+" works.\n"); err != nil {
		s.writeErr(w, r, errf(502, "email_failed", "Sending failed: "+err.Error()))
		return
	}
	s.audit(r.Context(), r, u.ID, "admin_test_email", u.Email, "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}
