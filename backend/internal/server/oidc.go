package server

// Single sign-on with OpenID Connect (authorization code flow + PKCE). Works with any standard
// provider: Keycloak, Authentik, Authelia, Zitadel, Google, Microsoft Entra ID, GitLab, …
//
// Sign-in resolution, in order:
//  1. the provider identity (issuer + subject) is already connected to an account → sign in;
//  2. FERRY_OIDC_LINK_BY_EMAIL and an account has the same, provider-verified email → connect it, sign in;
//  3. FERRY_OIDC_AUTO_CREATE → create a new account, connect it, sign in;
//  4. otherwise the identity is parked in a short-lived signed cookie and the user is asked to sign in
//     to their existing account once, then confirm "Connect" (or an admin creates the account first).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"ferry/internal/db"
)

const (
	oidcStateCookie   = "ferry_oidc_state"
	oidcPendingCookie = "ferry_oidc_pending"
	oidcCookiePath    = "/api/v1/auth/oidc"
)

type oidcClient struct {
	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	ctx      context.Context // carries the HTTP client; long-lived because key refreshes reuse it
}

// reset forgets the discovered provider, e.g. after the SSO settings changed.
func (c *oidcClient) reset() {
	c.mu.Lock()
	c.provider, c.verifier = nil, nil
	c.mu.Unlock()
}

// oidcProvider discovers the provider lazily and caches it, so Ferry starts even when the provider
// is down and recovers on the next sign-in attempt.
func (s *Server) oidcProvider() (context.Context, *oidc.Provider, *oidc.IDTokenVerifier, error) {
	c := &s.oidc
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c.ctx, c.provider, c.verifier, nil
	}
	hc := &http.Client{Timeout: 15 * time.Second}
	if s.conf().OIDC.SkipVerify {
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // opt-in: FERRY_OIDC_SKIP_VERIFY
	}
	ctx := oidc.ClientContext(context.Background(), hc)
	p, err := oidc.NewProvider(ctx, s.conf().OIDC.Issuer)
	if err != nil && strings.Contains(err.Error(), "did not match") {
		// The most common setup mistake is a missing or extra trailing slash; try the other form.
		alt := strings.TrimSuffix(s.conf().OIDC.Issuer, "/")
		if alt == s.conf().OIDC.Issuer {
			alt += "/"
		}
		if p2, err2 := oidc.NewProvider(ctx, alt); err2 == nil {
			s.log.Warn("FERRY_OIDC_ISSUER should be written exactly as the provider's issuer", "use", alt)
			p, err = p2, nil
		}
	}
	if err != nil {
		return nil, nil, nil, err
	}
	c.ctx, c.provider = ctx, p
	c.verifier = p.Verifier(&oidc.Config{ClientID: s.conf().OIDC.ClientID})
	return c.ctx, c.provider, c.verifier, nil
}

func (s *Server) oauthConfig(r *http.Request, p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{ClientID: s.conf().OIDC.ClientID, ClientSecret: s.conf().OIDC.ClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: s.baseURL(r) + "/api/v1/auth/oidc/callback", Scopes: s.conf().OIDC.Scopes}
}

// ---------- signed cookies ----------

func (s *Server) seal(v any) string {
	b, _ := json.Marshal(v)
	p := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte("oidc|" + p))
	return p + "." + hex.EncodeToString(m.Sum(nil))
}

func (s *Server) unseal(r *http.Request, name string, v any) bool {
	c, err := r.Cookie(name)
	if err != nil {
		return false
	}
	p, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte("oidc|" + p))
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(m.Sum(nil)))) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(p)
	return err == nil && json.Unmarshal(b, v) == nil
}

func (s *Server) setOIDCCookie(w http.ResponseWriter, r *http.Request, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: oidcCookiePath, HttpOnly: true, Secure: s.secureCookie(r),
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
}

type oidcState struct {
	State, Nonce, Verifier, Next, Mode, UserID string
	Exp                                        int64
}

type oidcIdentity struct {
	Issuer, Subject, Email, Name string
	Exp                          int64
}

// ---------- handlers ----------

// handleOIDCStart redirects to the provider. mode=link connects the provider to the signed-in account.
func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	if !s.conf().OIDC.Enabled() {
		s.writeErr(w, r, errNotFound)
		return
	}
	st := oidcState{State: newToken(16), Nonce: newToken(16), Verifier: oauth2.GenerateVerifier(), Next: safeNext(r.URL.Query().Get("next")),
		Mode: "login", Exp: nowMs() + 10*60*1000}
	if r.URL.Query().Get("mode") == "link" {
		a, err := s.authenticate(r)
		if err != nil {
			s.oidcFail(w, r, "Sign in first, then connect your account from Settings.")
			return
		}
		st.Mode, st.UserID, st.Next = "link", a.user.ID, "/settings"
	}
	_, p, _, err := s.oidcProvider()
	if err != nil {
		s.log.Error("oidc discovery failed", "issuer", s.conf().OIDC.Issuer, "err", err)
		s.oidcFail(w, r, s.conf().OIDC.Name+" is not reachable right now. Please try again in a moment or sign in with your password.")
		return
	}
	s.setOIDCCookie(w, r, oidcStateCookie, s.seal(st), 10*time.Minute)
	http.Redirect(w, r, s.oauthConfig(r, p).AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	var st oidcState
	ok := s.unseal(r, oidcStateCookie, &st)
	s.setOIDCCookie(w, r, oidcStateCookie, "", -time.Second)
	if e := q.Get("error"); e != "" {
		msg := "Sign-in with " + s.conf().OIDC.Name + " was cancelled."
		if e != "access_denied" {
			msg = "Sign-in with " + s.conf().OIDC.Name + " failed: " + clip(firstNonEmpty(q.Get("error_description"), e), 200)
		}
		s.oidcFail(w, r, msg)
		return
	}
	if !ok || st.Exp < nowMs() || q.Get("state") == "" || q.Get("state") != st.State {
		s.oidcFail(w, r, "The sign-in took too long or was started in another browser. Please try again.")
		return
	}
	pctx, p, verifier, err := s.oidcProvider()
	if err != nil {
		s.oidcFail(w, r, s.conf().OIDC.Name+" is not reachable right now. Please try again.")
		return
	}
	xctx, cancel := context.WithTimeout(pctx, 20*time.Second)
	defer cancel()
	tok, err := s.oauthConfig(r, p).Exchange(xctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		s.log.Warn("oidc code exchange failed", "err", err)
		s.oidcFail(w, r, "Couldn't complete sign-in with "+s.conf().OIDC.Name+". Check the client ID and secret configured in Ferry.")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := verifier.Verify(xctx, raw)
	if err != nil || idt.Nonce != st.Nonce {
		s.log.Warn("oidc id token rejected", "err", err)
		s.oidcFail(w, r, "The sign-in response from "+s.conf().OIDC.Name+" couldn't be verified. Please try again.")
		return
	}
	claims := map[string]any{}
	idt.Claims(&claims)
	if claimStr(claims, "email") == "" { // some providers (e.g. Entra ID) only return it from UserInfo
		if ui, err := p.UserInfo(xctx, oauth2.StaticTokenSource(tok)); err == nil && ui.Subject == idt.Subject {
			extra := map[string]any{}
			ui.Claims(&extra)
			for k, v := range extra {
				if _, has := claims[k]; !has {
					claims[k] = v
				}
			}
		}
	}
	id := oidcIdentity{Issuer: idt.Issuer, Subject: idt.Subject, Email: strings.ToLower(claimStr(claims, "email")),
		Name: firstNonEmpty(claimStr(claims, "name"), strings.TrimSpace(claimStr(claims, "given_name")+" "+claimStr(claims, "family_name")), claimStr(claims, "preferred_username"))}
	verified := claimBool(claims, "email_verified") || s.conf().OIDC.TrustUnverifiedEmail
	if !s.domainAllowed(id.Email) {
		s.audit(ctx, r, "", "oidc_login_denied", id.Email, "email domain not allowed")
		s.oidcFail(w, r, "Accounts from this email domain can't sign in here.")
		return
	}

	if st.Mode == "link" {
		cur, err := s.authenticate(r)
		if err != nil || cur.user.ID != st.UserID {
			s.oidcFail(w, r, "Your session ended. Sign in again, then connect your account from Settings.")
			return
		}
		if err := s.linkIdentity(ctx, r, cur.user, id); err != nil {
			s.oidcFail(w, r, err.Error())
			return
		}
		http.Redirect(w, r, "/settings?sso=connected", http.StatusFound)
		return
	}

	// 1. Already connected.
	u, err := scanUser(s.db.QueryRow(ctx, `SELECT u.id, u.email, u.name, u.role, u.disabled, u.quota_bytes, u.created_at, u.totp_enabled
		FROM user_identities i JOIN users u ON u.id = i.user_id WHERE i.issuer = ? AND i.subject = ?`, id.Issuer, id.Subject))
	if err != nil && !db.IsNoRows(err) {
		s.oidcFail(w, r, "Something went wrong on the server. Please try again.")
		return
	}
	// 2. Same verified email.
	if u == nil && id.Email != "" && verified && s.conf().OIDC.LinkByEmail {
		if existing, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, id.Email)); err == nil {
			if err := s.linkIdentity(ctx, r, existing, id); err != nil {
				s.oidcFail(w, r, err.Error())
				return
			}
			u = existing
		}
	}
	// 3. New account.
	if u == nil && s.conf().OIDC.AutoCreate && id.Email != "" && verified {
		created, err := s.createSSOUser(ctx, r, id)
		if err == nil {
			u = created
		} else if !errors.Is(err, errEmailTaken) {
			s.oidcFail(w, r, "Couldn't create your account. Please try again.")
			return
		}
	}
	// 4. Ask the person to sign in to their existing account and confirm the connection.
	if u == nil {
		id.Exp = nowMs() + 15*60*1000
		s.setOIDCCookie(w, r, oidcPendingCookie, s.seal(id), 15*time.Minute)
		s.audit(ctx, r, "", "oidc_login_unmatched", firstNonEmpty(id.Email, id.Subject), "asked to connect an existing account")
		http.Redirect(w, r, "/login?sso=connect&next="+url.QueryEscape(st.Next), http.StatusFound)
		return
	}
	if u.Disabled {
		s.oidcFail(w, r, "This account has been disabled. Contact your administrator.")
		return
	}
	s.db.Exec(ctx, `UPDATE user_identities SET last_login = ?, email = ? WHERE issuer = ? AND subject = ?`, nowMs(), id.Email, id.Issuer, id.Subject)
	s.syncAdminRole(ctx, r, u, claims)
	if err := s.webSession(w, r, u); err != nil {
		s.oidcFail(w, r, "Something went wrong on the server. Please try again.")
		return
	}
	s.audit(ctx, r, u.ID, "login", u.Email, "via "+s.conf().OIDC.Name)
	http.Redirect(w, r, st.Next, http.StatusFound)
}

var errEmailTaken = errors.New("email taken")

func (s *Server) createSSOUser(ctx context.Context, r *http.Request, id oidcIdentity) (*User, error) {
	email, err := normalizeEmail(id.Email)
	if err != nil {
		return nil, err
	}
	name := clip(strings.TrimSpace(id.Name), 100)
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	u := &User{ID: newID(), Email: email, Name: name, Role: "user", QuotaBytes: -1, CreatedAt: nowMs()}
	err = s.db.InTx(ctx, func(tx *db.Tx) error {
		// No password: the account signs in through the provider until the user sets one in Settings.
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash, role, disabled, quota_bytes, created_at, updated_at) VALUES (?, ?, ?, '', 'user', 0, -1, ?, ?)`,
			u.ID, u.Email, u.Name, u.CreatedAt, u.CreatedAt); err != nil {
			if db.IsUnique(err) {
				return errEmailTaken
			}
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_identities (id, user_id, issuer, subject, email, created_at, last_login) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			newID(), u.ID, id.Issuer, id.Subject, id.Email, u.CreatedAt, u.CreatedAt)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, r, u.ID, "oidc_user_created", u.Email, "via "+s.conf().OIDC.Name)
	return u, nil
}

// linkIdentity connects a provider identity to u. The returned error message is shown to the user.
func (s *Server) linkIdentity(ctx context.Context, r *http.Request, u *User, id oidcIdentity) error {
	var owner string
	err := s.db.QueryRow(ctx, `SELECT user_id FROM user_identities WHERE issuer = ? AND subject = ?`, id.Issuer, id.Subject).Scan(&owner)
	switch {
	case err == nil && owner == u.ID:
		return nil
	case err == nil:
		return errf(409, "identity_in_use", "This "+s.conf().OIDC.Name+" account is already connected to a different Ferry account.")
	case !db.IsNoRows(err):
		return errf(500, "internal", "Something went wrong on the server. Please try again.")
	}
	now := nowMs()
	if _, err := s.db.Exec(ctx, `INSERT INTO user_identities (id, user_id, issuer, subject, email, created_at, last_login) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newID(), u.ID, id.Issuer, id.Subject, id.Email, now, now); err != nil {
		return errf(500, "internal", "Something went wrong on the server. Please try again.")
	}
	s.audit(ctx, r, u.ID, "oidc_connected", firstNonEmpty(id.Email, id.Subject), s.conf().OIDC.Name)
	return nil
}

// syncAdminRole makes group members admins (and removes admin from non-members) when FERRY_OIDC_ADMIN_GROUP
// is set. The last remaining admin is never demoted, so the server can't lose its administrator.
func (s *Server) syncAdminRole(ctx context.Context, r *http.Request, u *User, claims map[string]any) {
	g := s.conf().OIDC.AdminGroup
	if g == "" {
		return
	}
	want := "user"
	for _, v := range claimList(claims, s.conf().OIDC.GroupsClaim) {
		if strings.TrimPrefix(v, "/") == g {
			want = "admin"
		}
	}
	if want == u.Role {
		return
	}
	if want == "user" {
		var admins int
		s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&admins)
		if admins <= 1 {
			return
		}
	}
	if _, err := s.db.Exec(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, want, nowMs(), u.ID); err == nil {
		u.Role = want
		s.audit(ctx, r, u.ID, "oidc_role_synced", u.Email, "role "+want+" from group "+g)
	}
}

// handleOIDCPending tells the login page about an identity waiting to be connected.
func (s *Server) handleOIDCPending(w http.ResponseWriter, r *http.Request) {
	var id oidcIdentity
	if !s.unseal(r, oidcPendingCookie, &id) || id.Exp < nowMs() {
		writeJSON(w, 200, map[string]any{"pending": nil})
		return
	}
	writeJSON(w, 200, map[string]any{"pending": map[string]any{"provider": s.conf().OIDC.Name, "email": id.Email, "name": id.Name},
		"autoCreate": s.conf().OIDC.AutoCreate})
}

func (s *Server) handleOIDCClearPending(w http.ResponseWriter, r *http.Request) {
	s.setOIDCCookie(w, r, oidcPendingCookie, "", -time.Second)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleOIDCLinkPending connects the waiting identity to the account that just signed in.
func (s *Server) handleOIDCLinkPending(w http.ResponseWriter, r *http.Request) {
	var id oidcIdentity
	if !s.unseal(r, oidcPendingCookie, &id) || id.Exp < nowMs() {
		s.writeErr(w, r, errf(400, "nothing_to_connect", "The "+s.conf().OIDC.Name+" sign-in expired. Sign in with "+s.conf().OIDC.Name+" again to connect it."))
		return
	}
	if err := s.linkIdentity(r.Context(), r, userOf(r), id); err != nil {
		s.writeErr(w, r, errf(409, "identity_in_use", err.Error()))
		return
	}
	s.setOIDCCookie(w, r, oidcPendingCookie, "", -time.Second)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleListIdentities(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT id, email, created_at, last_login FROM user_identities WHERE user_id = ? ORDER BY created_at`, userOf(r).ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, email string
		var created, last int64
		rows.Scan(&id, &email, &created, &last)
		out = append(out, map[string]any{"id": id, "email": email, "provider": s.conf().OIDC.Name, "createdAt": created, "lastLogin": last})
	}
	writeJSON(w, 200, map[string]any{"identities": out})
}

func (s *Server) handleDeleteIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	var hash string
	var n int
	s.db.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&hash)
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM user_identities WHERE user_id = ?`, u.ID).Scan(&n)
	if hash == "" && n <= 1 {
		s.writeErr(w, r, errf(400, "last_sign_in_method", "Set a password first — otherwise you couldn't sign in anymore."))
		return
	}
	res, err := s.db.Exec(ctx, `DELETE FROM user_identities WHERE id = ? AND user_id = ?`, r.PathValue("id"), u.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if k, _ := res.RowsAffected(); k == 0 {
		s.writeErr(w, r, errNotFound)
		return
	}
	s.audit(ctx, r, u.ID, "oidc_disconnected", u.Email, s.conf().OIDC.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAdminDeleteIdentities(w http.ResponseWriter, r *http.Request) {
	res, err := s.db.Exec(r.Context(), `DELETE FROM user_identities WHERE user_id = ?`, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	n, _ := res.RowsAffected()
	s.audit(r.Context(), r, userOf(r).ID, "admin_sso_disconnected", r.PathValue("id"), "")
	writeJSON(w, 200, map[string]any{"ok": true, "removed": n})
}

// ---------- helpers ----------

func (s *Server) oidcFail(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(msg), http.StatusFound)
}

func (s *Server) domainAllowed(email string) bool {
	if len(s.conf().OIDC.AllowedDomains) == 0 {
		return true
	}
	_, d, _ := strings.Cut(email, "@")
	for _, a := range s.conf().OIDC.AllowedDomains {
		if d == a {
			return true
		}
	}
	return false
}

// safeNext keeps redirects on this site ("/files", not "//evil.example" or "https://…").
func safeNext(n string) string {
	if !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.HasPrefix(n, "/\\") || strings.HasPrefix(n, "/api/") {
		return "/"
	}
	return n
}

func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

func claimStr(c map[string]any, k string) string {
	v, _ := c[k].(string)
	return v
}

// claimBool accepts true and "true" (some providers send strings).
func claimBool(c map[string]any, k string) bool {
	switch v := c[k].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

// claimList reads a string or string array at a dotted path, e.g. "groups" or "realm_access.roles".
func claimList(c map[string]any, path string) []string {
	var cur any = c
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	switch v := cur.(type) {
	case string:
		return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
