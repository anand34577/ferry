package server

// Account security: two-factor authentication (TOTP), password reset by email, and session management.

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ferry/internal/db"
)

// ---------- TOTP (RFC 6238: SHA-1, 6 digits, 30 s — what every authenticator app supports) ----------

func totpAt(key []byte, step int64) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(b[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(h[o:])&0x7fffffff)%1_000_000)
}

// totpCheck accepts the current code or one step either side (clock drift) newer than lastStep,
// and returns the matched step so it can't be replayed.
func totpCheck(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	code = strings.ReplaceAll(code, " ", "")
	if err != nil || len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / 30
	for st := cur - 1; st <= cur+1; st++ {
		if st > lastStep && subtle.ConstantTimeCompare([]byte(totpAt(key, st)), []byte(code)) == 1 {
			return st, true
		}
	}
	return 0, false
}

// useTOTP verifies a code and records its step atomically, so the same code can't be used twice.
func (s *Server) useTOTP(ctx context.Context, userID, secret, code string, lastStep int64) bool {
	st, ok := totpCheck(secret, code, time.Now(), lastStep)
	if !ok {
		return false
	}
	res, err := s.db.Exec(ctx, `UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, st, userID, st)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func disableTOTP(ctx context.Context, q db.Q, userID string) error {
	_, err := q.Exec(ctx, `UPDATE users SET totp_enabled = 0, totp_secret = '', totp_last_step = 0, updated_at = ? WHERE id = ?`, nowMs(), userID)
	return err
}

// handleTOTPSetup creates a new (not yet active) secret for the authenticator app.
func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u.TOTP {
		s.writeErr(w, r, errf(409, "totp_enabled", "Two-factor authentication is already on. Turn it off first to set up a new app."))
		return
	}
	secret := b32.EncodeToString(randBytes(20))
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET totp_secret = ?, totp_last_step = 0 WHERE id = ?`, secret, u.ID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	label := url.PathEscape(s.conf().SiteName + ":" + u.Email)
	uri := "otpauth://totp/" + label + "?secret=" + secret + "&issuer=" + url.QueryEscape(s.conf().SiteName) + "&algorithm=SHA1&digits=6&period=30"
	writeJSON(w, 200, map[string]string{"secret": secret, "uri": uri})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u := userOf(r)
	if !s.limiter.allow("totp:"+u.ID, 10, 15*time.Minute) {
		s.writeErr(w, r, errf(429, "rate_limited", "Too many attempts. Please try again later."))
		return
	}
	var secret string
	var on int
	if err := s.db.QueryRow(r.Context(), `SELECT totp_secret, totp_enabled FROM users WHERE id = ?`, u.ID).Scan(&secret, &on); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if on == 1 || secret == "" {
		s.writeErr(w, r, errf(409, "totp_not_pending", "Start the setup again."))
		return
	}
	st, ok := totpCheck(secret, req.Code, time.Now(), 0)
	if !ok {
		s.writeErr(w, r, errf(400, "invalid_code", "That code doesn't match. Check the time on your phone and try the current code."))
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET totp_enabled = 1, totp_last_step = ?, updated_at = ? WHERE id = ?`, st, nowMs(), u.ID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "2fa_enabled", u.Email, "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u := userOf(r)
	if !s.limiter.allow("pwchange:"+u.ID, 10, 15*time.Minute) {
		s.writeErr(w, r, errf(429, "rate_limited", "Too many attempts. Please try again later."))
		return
	}
	var hash string
	if err := s.db.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if !checkPassword(hash, req.Password) {
		s.writeErr(w, r, errf(400, "wrong_password", "Your password is incorrect."))
		return
	}
	if err := disableTOTP(r.Context(), s.db, u.ID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "2fa_disabled", u.Email, "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- password reset by email ----------

// resetEnabled needs SMTP and a configured public URL: building the emailed link from the request's
// Host header would let an attacker send victims a valid reset token pointing at their own site.
func (s *Server) resetEnabled() bool { return s.conf().SMTPHost != "" && s.conf().PublicURL != "" }

const resetTTL = time.Hour

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.resetEnabled() {
		s.writeErr(w, r, errf(400, "reset_disabled", "Password reset by email isn't available on this server. Ask your administrator to reset your password."))
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !s.limiter.allow("forgot:"+s.clientIP(r), 5, time.Hour) || !s.limiter.allow("forgotmail:"+email, 3, time.Hour) {
		s.writeErr(w, r, errf(429, "rate_limited", "Too many reset requests. Please try again later."))
		return
	}
	// The response is identical whether or not the account exists, and mail is sent in the background
	// so timing doesn't reveal it either.
	var id, name string
	var disabled int
	err := s.db.QueryRow(r.Context(), `SELECT id, name, disabled FROM users WHERE email = ?`, email).Scan(&id, &name, &disabled)
	if err == nil && disabled == 0 {
		tok := newToken(32)
		now := nowMs()
		s.db.Exec(r.Context(), `DELETE FROM password_resets WHERE user_id = ?`, id)
		if _, err := s.db.Exec(r.Context(), `INSERT INTO password_resets (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
			hashToken(tok), id, now, now+resetTTL.Milliseconds()); err != nil {
			s.writeErr(w, r, err)
			return
		}
		s.audit(r.Context(), r, id, "password_reset_requested", email, "")
		link := s.conf().PublicURL + "/reset?token=" + tok
		body := "Hi " + name + ",\n\nSomeone (hopefully you) asked to reset your " + s.conf().SiteName + " password.\n\n" +
			"Choose a new password here (the link works for 1 hour, once):\n" + link + "\n\n" +
			"If you didn't ask for this, ignore this email — your password stays the same.\n"
		go func() {
			if err := s.mail(email, "Reset your "+s.conf().SiteName+" password", body); err != nil {
				s.log.Warn("password reset email failed", "err", err)
			}
		}()
	} else if err != nil && !db.IsNoRows(err) {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.resetPassword(r, req.Token, req.Password); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// resetPassword uses a one-hour reset token from the emailed link.
func (s *Server) resetPassword(r *http.Request, token, password string) error {
	if !s.limiter.allow("reset:"+s.clientIP(r), 20, time.Hour) {
		return errf(429, "rate_limited", "Too many attempts. Please try again later.")
	}
	invalid := errf(400, "invalid_token", "This reset link is invalid or has expired. Request a new one.")
	var userID, email string
	err := s.db.InTx(r.Context(), func(tx *db.Tx) error {
		var exp int64
		err := tx.QueryRow(r.Context(), `SELECT pr.user_id, pr.expires_at, u.email FROM password_resets pr JOIN users u ON u.id = pr.user_id WHERE pr.id = ?`,
			hashToken(strings.TrimSpace(token))).Scan(&userID, &exp, &email)
		if db.IsNoRows(err) || (err == nil && exp < nowMs()) {
			return invalid
		}
		if err != nil {
			return err
		}
		if err := SetPassword(r.Context(), tx, userID, password, ""); err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), `DELETE FROM password_resets WHERE user_id = ?`, userID)
		return err
	})
	if err == nil {
		s.audit(r.Context(), r, userID, "password_reset", email, "all sessions signed out")
	}
	return err
}

// handleResetPage is the password-reset page when the web app is turned off (API-only servers):
// the link in the reset email must still work.
func (s *Server) handleResetPage(w http.ResponseWriter, r *http.Request) {
	d := &pageData{Title: "Choose a new password", Token: r.URL.Query().Get("token")}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		d.Token = r.PostFormValue("token")
		pw := r.PostFormValue("password")
		if pw != r.PostFormValue("confirm") {
			d.Error = "The passwords don't match."
		} else if err := s.resetPassword(r, d.Token, pw); err != nil {
			var ae *apiErr
			d.Error = "Something went wrong. Please try again."
			if errors.As(err, &ae) {
				d.Error = ae.Message
			}
		} else {
			s.messagePage(w, 200, "Password changed", "You were signed out everywhere. Sign in with your new password in the Ferry app.")
			return
		}
	}
	s.render(w, 200, "reset.html", d)
}

// ---------- sessions ----------

type sessionInfo struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceId,omitempty"`
	DeviceName string `json:"deviceName,omitempty"`
	IP         string `json:"ip"`
	UserAgent  string `json:"userAgent"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeen   int64  `json:"lastSeen"`
	ExpiresAt  int64  `json:"expiresAt"`
	Current    bool   `json:"current"`
}

// handleListSessions lists where the account is signed in. IDs are the stored token hashes, which
// can't be used to sign in.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	rows, err := s.db.Query(r.Context(), `SELECT s.id, s.device_id, COALESCE(d.name, ''), s.ip, s.user_agent, s.created_at, s.last_seen, s.expires_at
		FROM sessions s LEFT JOIN devices d ON d.id = s.device_id WHERE s.user_id = ? AND s.expires_at > ? ORDER BY s.last_seen DESC`, a.user.ID, nowMs())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	out := []sessionInfo{}
	for rows.Next() {
		var si sessionInfo
		if err := rows.Scan(&si.ID, &si.DeviceID, &si.DeviceName, &si.IP, &si.UserAgent, &si.CreatedAt, &si.LastSeen, &si.ExpiresAt); err != nil {
			s.writeErr(w, r, err)
			return
		}
		si.Current = si.ID == a.sessionID
		out = append(out, si)
	}
	writeJSON(w, 200, map[string]any{"sessions": out})
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	res, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE id = ? AND user_id = ?`, r.PathValue("id"), a.user.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeErr(w, r, errNotFound)
		return
	}
	s.audit(r.Context(), r, a.user.ID, "session_revoked", "", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	if _, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, a.user.ID, a.sessionID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, a.user.ID, "sessions_revoked", "", "all other sessions")
	writeJSON(w, 200, map[string]bool{"ok": true})
}
