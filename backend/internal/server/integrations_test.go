package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ferry/internal/config"
)

func TestCleanGotifyURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://gotify.example.com/":                   "https://gotify.example.com",
		"gotify.example.com":                            "https://gotify.example.com",
		"http://10.0.0.5:8080/message?token=abc":        "http://10.0.0.5:8080",
		"HTTPS://push.example.com/gotify/message":       "https://push.example.com/gotify",
		"  https://push.example.com/sub/path/#fragment": "https://push.example.com/sub/path",
		"": "",
	} {
		if got, err := cleanGotifyURL(in); err != nil || got != want {
			t.Errorf("cleanGotifyURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://x", "https://user:pw@host", "https://"} {
		if _, err := cleanGotifyURL(bad); err == nil {
			t.Errorf("cleanGotifyURL(%q) accepted", bad)
		}
	}
}

// Gotify over plain HTTP, HTTPS with a self-signed certificate (refused unless the user opts in),
// redirects and rejected tokens each give a result the user can act on.
func TestGotifyDelivery(t *testing.T) {
	s, ts := newTestServer(t, nil)
	admin := setupAdmin(t, ts)
	me := admin.json("GET", "/api/v1/me", nil, 200)["user"].(map[string]any)
	uid := me["id"].(string)
	var got []string
	gotify := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/message" {
			w.WriteHeader(405)
			return
		}
		if r.Header.Get("X-Gotify-Key") != "app-token" {
			w.WriteHeader(401)
			return
		}
		got = append(got, r.URL.Path)
		w.Write([]byte(`{"id":1}`))
	})
	plain := httptest.NewServer(gotify)
	defer plain.Close()
	secure := httptest.NewTLSServer(gotify)
	defer secure.Close()
	redirect := httptest.NewServer(http.RedirectHandler(secure.URL+"/message", http.StatusMovedPermanently))
	defer redirect.Close()

	set := func(url, tok string, insecure bool) {
		admin.json("PATCH", "/api/v1/me", map[string]any{"gotifyUrl": url, "gotifyToken": tok, "gotifySkipVerify": insecure}, 200)
	}
	send := func() error { return s.gotify(context.Background(), uid, "t", "m") }

	set(plain.URL, "app-token", false)
	if err := send(); err != nil || len(got) != 1 {
		t.Fatalf("http gotify: %v %v", err, got)
	}
	admin.json("POST", "/api/v1/me/gotify/test", nil, 200)

	set(secure.URL, "app-token", false)
	if err := send(); err == nil || !strings.Contains(err.Error(), "self-signed") {
		t.Fatalf("self-signed without opt-in: %v", err)
	}
	set(secure.URL, "app-token", true)
	if err := send(); err != nil {
		t.Fatalf("self-signed with opt-in: %v", err)
	}
	if m := admin.json("GET", "/api/v1/me", nil, 200); m["gotifySkipVerify"] != true {
		t.Fatalf("skip verify not reported: %v", m)
	}

	set(redirect.URL, "app-token", false)
	if err := send(); err == nil || !strings.Contains(err.Error(), "redirects to "+secure.URL) {
		t.Fatalf("redirect: %v", err)
	}
	set(plain.URL, "wrong", false)
	if err := send(); err == nil || !strings.Contains(err.Error(), "token was rejected") {
		t.Fatalf("bad token: %v", err)
	}
	set(plain.URL+"/sub", "app-token", false)
	if err := send(); err == nil || !strings.Contains(err.Error(), "doesn't look like a Gotify server") {
		t.Fatalf("wrong path: %v", err)
	}
	// Clearing the address forgets the token.
	admin.json("PATCH", "/api/v1/me", map[string]any{"gotifyUrl": ""}, 200)
	if m := admin.json("GET", "/api/v1/me", nil, 200); m["gotifyConfigured"] != false {
		t.Fatalf("not cleared: %v", m)
	}
}

// Implicit TLS (port 465 style) to a mail server with a self-signed certificate.
func TestSMTPImplicitTLS(t *testing.T) {
	cert := httptest.NewTLSServer(http.NotFoundHandler()).TLS.Certificates
	listen := func() (net.Listener, chan string) {
		ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: cert})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		got := make(chan string, 1)
		go fakeSMTP(ln, "PLAIN", got)
		return ln, got
	}
	for _, skip := range []bool{false, true} {
		ln, got := listen()
		s, _ := newTestServer(t, func(c *config.Config) {
			host, port, _ := net.SplitHostPort(ln.Addr().String())
			c.SMTPHost, c.SMTPPort, c.SMTPSecurity, c.SMTPSkipVerify = host, atoi(port), "tls", skip
			c.SMTPUser, c.SMTPPass, c.SMTPFrom = "me", "secret", "Ferry <ferry@example.com>"
		})
		err := s.sendMail("to@example.com", "Hi", "body ünïcode\nline two")
		if !skip {
			if err == nil || !strings.Contains(err.Error(), "certificate isn't trusted") {
				t.Fatalf("untrusted certificate: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case a := <-got:
			if a != "me/secret" {
				t.Fatalf("auth = %q", a)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no auth")
		}
	}
}

// Behind a reverse proxy or tunnel that isn't in FERRY_TRUSTED_PROXIES every request comes from a local
// address; the forwarded client address must decide whether setup is allowed.
func TestSetupBehindUntrustedProxy(t *testing.T) {
	_, ts := newTestServer(t, nil)
	c := newClient(t, ts.URL)
	body := map[string]string{"email": "x@example.com", "password": "long-enough"}
	for _, h := range []map[string]string{
		{"X-Forwarded-For": "8.8.8.8"},
		{"X-Forwarded-For": "10.0.0.1, 8.8.8.8"},
		{"Cf-Connecting-Ip": "2001:4860::1"},
		{"Forwarded": `for="[2001:4860::1]:443";proto=https`},
	} {
		if resp, _ := c.do("POST", "/api/v1/setup", body, h); resp.StatusCode != 403 {
			t.Fatalf("setup via %v: %d", h, resp.StatusCode)
		}
	}
	resp, _ := c.do("POST", "/api/v1/setup", body, map[string]string{"X-Forwarded-For": "192.168.1.20"})
	if resp.StatusCode != 200 {
		t.Fatalf("LAN setup through proxy: %d", resp.StatusCode)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/files?x=1": "/files?x=1", "//evil.example": "/", "/\\evil.example": "/", "/\t/evil.example": "/",
		"https://evil.example": "/", "/api/v1/me": "/", "": "/", "/s/abc": "/s/abc",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// Accounts created by single sign-on have no password; they turn two-factor off with a current code.
func TestTOTPDisableWithoutPassword(t *testing.T) {
	s, ts := newTestServer(t, nil)
	setupAdmin(t, ts)
	ctx := context.Background()
	u, err := CreateUser(ctx, s.db, "sso@example.com", "", "temporary-pw", "user")
	if err != nil {
		t.Fatal(err)
	}
	secret := "JBSWY3DPEHPK3PXP"
	s.db.Exec(ctx, `UPDATE users SET password_hash = '', totp_secret = ?, totp_enabled = 1, totp_last_step = 0 WHERE id = ?`, secret, u.ID)
	tok := newToken(32)
	req := httptest.NewRequest("GET", "/", nil)
	if err := s.insertSession(ctx, req, tok, u.ID, "", time.Hour, "test"); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, ts.URL)
	auth := map[string]string{"Authorization": "Bearer " + tok}
	if resp, _ := c.do("POST", "/api/v1/me/totp/disable", map[string]string{"code": "000000"}, auth); resp.StatusCode != 400 {
		t.Fatalf("wrong code accepted: %d", resp.StatusCode)
	}
	key, _ := b32.DecodeString(secret)
	code := totpAt(key, time.Now().Unix()/30)
	if resp, body := c.do("POST", "/api/v1/me/totp/disable", map[string]string{"code": code}, auth); resp.StatusCode != 200 {
		t.Fatalf("disable with code: %d %s", resp.StatusCode, body)
	}
}

func TestClipKeepsUTF8(t *testing.T) {
	if got := clip("ab€", 3); got != "ab" {
		t.Fatalf("clip = %q", got)
	}
}

// An upload whose destination folder is deleted meanwhile lands in My files instead of vanishing.
func TestUploadIntoDeletedFolder(t *testing.T) {
	_, ts := newTestServer(t, nil)
	admin := setupAdmin(t, ts)
	fid := admin.json("POST", "/api/v1/folders", map[string]string{"name": "gone"}, 201)["id"].(string)
	resp, _ := admin.do("POST", "/api/v1/uploads", nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": "5",
		"Upload-Metadata": meta("filename", "a.txt", "folderId", fid)})
	if resp.StatusCode != 201 {
		t.Fatal("create", resp.StatusCode)
	}
	admin.json("DELETE", "/api/v1/folders/"+fid, nil, 200)
	resp, _ = admin.do("PATCH", resp.Header.Get("Location"), []byte("hello"), map[string]string{"Tus-Resumable": "1.0.0", "Upload-Offset": "0", "Content-Type": "application/offset+octet-stream"})
	if resp.StatusCode != 204 {
		t.Fatal("patch", resp.StatusCode)
	}
	files := admin.json("GET", "/api/v1/files", nil, 200)["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["name"] != "a.txt" {
		t.Fatalf("file not in My files: %v", files)
	}
}
