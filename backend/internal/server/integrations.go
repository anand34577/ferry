package server

// Optional outbound integrations: Gotify push notifications (per user) and a Shortr URL shortener (server-wide).

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var outbound = &http.Client{Timeout: 10 * time.Second}

var errLocalNetwork = errors.New("addresses on the server's own network aren't allowed")

// cgnat is 100.64.0.0/10 (carrier-grade NAT, also Tailscale), which net.IP.IsPrivate doesn't cover.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func localIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || cgnat.Contains(ip)
}

// gotifyClients[public][insecure]: public refuses connections to loopback, private and link-local
// addresses (checked on the resolved IP at connect time, so DNS tricks can't point it at the local
// network); insecure accepts a self-signed certificate. Redirects are not followed: a POST that is
// redirected (typically http → https) would arrive as a GET and be lost.
var gotifyClients = func() (c [2][2]*http.Client) {
	for pub := 0; pub < 2; pub++ {
		for ins := 0; ins < 2; ins++ {
			d := &net.Dialer{Timeout: 10 * time.Second}
			if pub == 1 {
				d.Control = func(_, address string, _ syscall.RawConn) error {
					host, _, _ := net.SplitHostPort(address)
					if localIP(net.ParseIP(host)) {
						return errLocalNetwork
					}
					return nil
				}
			}
			t := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: true}
			if pub == 0 {
				t.Proxy = http.ProxyFromEnvironment
			}
			if ins == 1 {
				t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the user's opt-in for their own Gotify
			}
			c[pub][ins] = &http.Client{Timeout: 15 * time.Second, Transport: t,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		}
	}
	return
}()

// openServer reports whether strangers can get an account (sign-up or SSO auto-create).
func (s *Server) openServer() bool { return s.conf().AllowSignup || s.conf().OIDC.AutoCreate }

// ---------- Gotify ----------

// cleanGotifyURL accepts the server address, also when pasted as ".../message?token=…" from Gotify's docs.
func cleanGotifyURL(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if !strings.Contains(v, "://") {
		v = "https://" + v
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", errf(400, "invalid_url", "Enter the Gotify server address, e.g. https://gotify.example.com")
	}
	u.RawQuery, u.Fragment, u.RawPath = "", "", ""
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/message")
	u.Scheme = strings.ToLower(u.Scheme)
	return strings.TrimRight(u.String(), "/"), nil
}

// gotify pushes a message to the user's own Gotify server; a no-op when they haven't set one up.
// Errors are written for the person who configured it.
func (s *Server) gotify(ctx context.Context, userID, title, message string) error {
	var gURL, tok, role string
	var insecure int
	if err := s.db.QueryRow(ctx, `SELECT gotify_url, gotify_token, gotify_skip_verify, role FROM users WHERE id = ?`, userID).Scan(&gURL, &tok, &insecure, &role); err != nil || gURL == "" || tok == "" {
		return err
	}
	pub := 0
	if s.openServer() && role != "admin" {
		pub = 1 // on open servers, users can't make the server reach its local network
	}
	client := gotifyClients[pub][min(insecure, 1)]
	body, _ := json.Marshal(map[string]any{"title": title, "message": message, "priority": 5})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gURL+"/message", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", tok)
	res, err := client.Do(req)
	if err != nil {
		var cv *tls.CertificateVerificationError
		var ua x509.UnknownAuthorityError
		var he x509.HostnameError
		var rh tls.RecordHeaderError
		switch {
		case errors.Is(err, errLocalNetwork):
			return errors.New("this server only sends notifications to Gotify servers on the internet (ask an administrator)")
		case errors.As(err, &cv) || errors.As(err, &ua) || errors.As(err, &he):
			return errors.New("its certificate isn't trusted — turn on \"Accept self-signed certificate\" if it is your own server")
		case errors.As(err, &rh):
			return errors.New("it doesn't speak HTTPS on that address — try http:// instead")
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return errors.New("no answer within 15 seconds — check the address and that this server can reach it")
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // "Post "https://…": " adds nothing the user doesn't know
		}
		return fmt.Errorf("couldn't connect: %w", err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode >= 300 && res.StatusCode < 400:
		if loc, err := res.Location(); err == nil {
			return fmt.Errorf("it redirects to %s — save that address instead", strings.TrimSuffix(loc.String(), "/message"))
		}
		return fmt.Errorf("it answered with a redirect (%d)", res.StatusCode)
	case res.StatusCode == 401 || res.StatusCode == 403:
		return errors.New("the application token was rejected — create an application in Gotify and paste its token (not a client token)")
	case res.StatusCode == 404 || res.StatusCode == 405:
		return fmt.Errorf("%s doesn't look like a Gotify server (%d)", gURL, res.StatusCode)
	case res.StatusCode >= 300:
		return fmt.Errorf("Gotify answered %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	return nil
}

func (s *Server) handleGotifyTest(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if !s.limiter.allow("gotifytest:"+u.ID, 10, time.Minute) {
		s.writeErr(w, r, errf(429, "rate_limited", "Too many test messages. Please wait a minute."))
		return
	}
	var gURL string
	s.db.QueryRow(r.Context(), `SELECT gotify_url FROM users WHERE id = ? AND gotify_token <> ''`, u.ID).Scan(&gURL)
	if gURL == "" {
		s.writeErr(w, r, errf(400, "gotify_not_configured", "Save a Gotify server address and application token first."))
		return
	}
	if err := s.gotify(r.Context(), u.ID, s.conf().SiteName+" test", "Notifications from "+s.conf().SiteName+" are working."); err != nil {
		s.writeErr(w, r, errf(502, "gotify_failed", "The test message wasn't delivered: "+err.Error()+"."))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- URL shortener (Shortr API) ----------

func (s *Server) shortenerOn() bool { return s.conf().ShortenerURL != "" }

// shorten creates a short link for sh and stores it. Failures are logged, never fatal: the full link always works.
func (s *Server) shorten(ctx context.Context, sh *Share, longURL string) error {
	if !s.shortenerOn() {
		return nil
	}
	body, _ := json.Marshal(map[string]any{"targetUrl": longURL, "title": sh.Name, "tags": []string{"ferry"}})
	var out struct {
		ID       string `json:"id"`
		ShortURL string `json:"shortUrl"`
	}
	if err := s.shortenerCall(ctx, http.MethodPost, "/api/v1/links", body, &out); err != nil {
		s.log.Warn("url shortener failed", "err", err)
		return err
	}
	if out.ShortURL == "" {
		return fmt.Errorf("url shortener returned no shortUrl")
	}
	sh.ShortURL, sh.shortID = out.ShortURL, out.ID
	_, err := s.db.Exec(ctx, `UPDATE shares SET short_url = ?, short_id = ? WHERE id = ?`, sh.ShortURL, sh.shortID, sh.ID)
	return err
}

// unshorten deletes a share's short link in the background.
func (s *Server) unshorten(id string) {
	if !s.shortenerOn() || id == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.shortenerCall(ctx, http.MethodDelete, "/api/v1/links/"+url.PathEscape(id), nil, nil); err != nil {
			s.log.Warn("url shortener delete failed", "err", err)
		}
	}()
}

func (s *Server) shortenerCall(ctx context.Context, method, path string, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, s.conf().ShortenerURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.conf().ShortenerToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := outbound.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 300 && !(method == http.MethodDelete && res.StatusCode == 404) {
		return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, clip(string(b), 200))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// handleShortenShare (re)creates the short link, e.g. for links made before the shortener was enabled.
func (s *Server) handleShortenShare(w http.ResponseWriter, r *http.Request) {
	if !s.shortenerOn() {
		s.writeErr(w, r, errf(400, "shortener_disabled", "The URL shortener is not configured on this server."))
		return
	}
	sh, err := s.getShare(r.Context(), userOf(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	old := sh.shortID
	if err := s.shorten(r.Context(), sh, s.shareURL(r, sh)); err != nil {
		s.writeErr(w, r, errf(502, "shortener_failed", "The URL shortener didn't respond. Please try again later."))
		return
	}
	s.unshorten(old)
	sh.URL = s.shareURL(r, sh)
	writeJSON(w, 200, sh)
}
