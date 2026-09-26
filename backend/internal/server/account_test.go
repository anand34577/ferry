package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ferry/internal/config"
)

func TestTOTPVector(t *testing.T) {
	// RFC 6238 appendix B, SHA-1, T=59 → 94287082 (last 6 digits).
	if got := totpAt([]byte("12345678901234567890"), 59/30); got != "287082" {
		t.Fatal(got)
	}
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(59, 0)
	if _, ok := totpCheck(secret, "287 082", now, 0); !ok {
		t.Fatal("valid code rejected")
	}
	if _, ok := totpCheck(secret, "287082", now, 59/30); ok {
		t.Fatal("replayed step accepted")
	}
	if _, ok := totpCheck(secret, "000000", now, 0); ok {
		t.Fatal("wrong code accepted")
	}
}

func TestTwoFactorLogin(t *testing.T) {
	_, ts := newTestServer(t, nil)
	admin := setupAdmin(t, ts)
	setup := admin.json("POST", "/api/v1/me/totp/setup", nil, 200)
	secret := setup["secret"].(string)
	if !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/") {
		t.Fatal(setup)
	}
	admin.json("POST", "/api/v1/me/totp/enable", map[string]string{"code": "000000"}, 400)
	key, _ := b32.DecodeString(secret)
	code := totpAt(key, time.Now().Unix()/30)
	admin.json("POST", "/api/v1/me/totp/enable", map[string]string{"code": code}, 200)
	if me := admin.json("GET", "/api/v1/me", nil, 200); me["user"].(map[string]any)["totpEnabled"] != true {
		t.Fatal("totp flag", me)
	}

	c := newClient(t, ts.URL)
	creds := map[string]string{"email": "admin@example.com", "password": "correct-horse"}
	if m := c.json("POST", "/api/v1/auth/login", creds, 401); m["error"].(map[string]any)["code"] != "totp_required" {
		t.Fatal(m)
	}
	creds["code"] = code // already used when enabling → replay
	if m := c.json("POST", "/api/v1/auth/login", creds, 401); m["error"].(map[string]any)["code"] != "invalid_code" {
		t.Fatal(m)
	}
	creds["code"] = totpAt(key, time.Now().Unix()/30+1) // next step (clock drift window)
	c.json("POST", "/api/v1/auth/login", creds, 200)

	admin.json("POST", "/api/v1/me/totp/disable", map[string]string{"password": "wrong"}, 400)
	admin.json("POST", "/api/v1/me/totp/disable", map[string]string{"password": "correct-horse"}, 200)
	delete(creds, "code")
	newClient(t, ts.URL).json("POST", "/api/v1/auth/login", creds, 200)
}

func TestPasswordReset(t *testing.T) {
	s, ts := newTestServer(t, func(c *config.Config) { c.SMTPHost, c.PublicURL = "smtp.invalid", "https://files.example.com" })
	var mu sync.Mutex
	var sent []string
	s.mail = func(to, subject, body string) error {
		mu.Lock()
		sent = append(sent, to+"\n"+body)
		mu.Unlock()
		return nil
	}
	admin := setupAdmin(t, ts)
	anon := newClient(t, ts.URL)
	anon.json("POST", "/api/v1/auth/forgot", map[string]string{"email": "nobody@example.com"}, 200) // same answer, no mail
	anon.json("POST", "/api/v1/auth/forgot", map[string]string{"email": "admin@example.com"}, 200)
	var tok string
	for i := 0; i < 100 && tok == ""; i++ {
		mu.Lock()
		if len(sent) == 1 {
			_, after, _ := strings.Cut(sent[0], "/reset?token=")
			tok = strings.Fields(after)[0]
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	if tok == "" || len(sent) != 1 || !strings.HasPrefix(sent[0], "admin@example.com") {
		t.Fatal("reset mail", sent)
	}
	anon.json("POST", "/api/v1/auth/reset", map[string]string{"token": "bogus", "password": "new-password-1"}, 400)
	anon.json("POST", "/api/v1/auth/reset", map[string]string{"token": tok, "password": "new-password-1"}, 200)
	anon.json("POST", "/api/v1/auth/reset", map[string]string{"token": tok, "password": "new-password-2"}, 400) // single use
	admin.json("GET", "/api/v1/me", nil, 401)                                                                   // sessions signed out
	anon.json("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "new-password-1"}, 200)
}

func TestPasswordResetNeedsPublicURL(t *testing.T) {
	_, ts := newTestServer(t, func(c *config.Config) { c.SMTPHost = "smtp.invalid" })
	setupAdmin(t, ts)
	newClient(t, ts.URL).json("POST", "/api/v1/auth/forgot", map[string]string{"email": "admin@example.com"}, 400)
}

func TestSessions(t *testing.T) {
	_, ts := newTestServer(t, nil)
	admin := setupAdmin(t, ts)
	other := newClient(t, ts.URL)
	other.json("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "correct-horse"}, 200)
	list := admin.json("GET", "/api/v1/me/sessions", nil, 200)["sessions"].([]any)
	if len(list) != 2 {
		t.Fatal(list)
	}
	var otherID string
	for _, x := range list {
		if m := x.(map[string]any); m["current"] != true {
			otherID = m["id"].(string)
		}
	}
	admin.json("DELETE", "/api/v1/me/sessions/"+otherID, nil, 200)
	other.json("GET", "/api/v1/me", nil, 401)
	other.json("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "correct-horse"}, 200)
	admin.json("POST", "/api/v1/me/sessions/revoke-others", nil, 200)
	other.json("GET", "/api/v1/me", nil, 401)
	admin.json("GET", "/api/v1/me", nil, 200)
}

func TestZipTicketAndMissingBlob(t *testing.T) {
	s, ts := newTestServer(t, nil)
	admin := setupAdmin(t, ts)
	a, _ := admin.tusUpload("/api/v1/uploads", []byte("aaa"), 100, meta("filename", "a.txt"))
	b, _ := admin.tusUpload("/api/v1/uploads", []byte("bbb"), 100, meta("filename", "b.txt"))
	url := admin.json("POST", "/api/v1/files/zip", map[string]any{"fileIds": []string{a, b}, "name": "pack"}, 200)["url"].(string)
	if r, body := admin.do("GET", url, nil, nil); r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/zip" || len(body) == 0 {
		t.Fatal("zip via ticket", r.StatusCode)
	}
	if r, _ := newClient(t, ts.URL).do("GET", url, nil, nil); r.StatusCode != 401 {
		t.Fatal("ticket must require the owner's session", r.StatusCode)
	}
	// A one-time link whose file is missing reports it and is not consumed.
	sh := admin.json("POST", "/api/v1/shares", map[string]any{"fileIds": []string{a, b}, "maxDownloads": 1}, 201)
	var blob string
	s.db.QueryRow(context.Background(), `SELECT blob FROM files WHERE id = ?`, b).Scan(&blob)
	os.Remove(filepath.Join(s.conf().StoragePath, "blobs", blob[:2], blob))
	if r, body := newClient(t, ts.URL).do("GET", "/s/"+sh["token"].(string)+"/zip", nil, nil); r.StatusCode != 404 || !strings.Contains(string(body), "missing") {
		t.Fatal("zip with missing blob", r.StatusCode)
	}
	if got := admin.json("GET", "/api/v1/shares/"+sh["id"].(string), nil, 200)["downloadCount"]; got != float64(0) {
		t.Fatal("download consumed", got)
	}
}

func TestTransferFiltersAndExpiryClamp(t *testing.T) {
	_, ts := newTestServer(t, nil)
	admin := setupAdmin(t, ts)
	for i := 0; i < 5; i++ {
		admin.json("POST", "/api/v1/transfers", map[string]any{"direction": "sent", "method": "direct", "status": "completed"}, 201)
	}
	admin.json("POST", "/api/v1/transfers", map[string]any{"direction": "received", "method": "direct", "status": "failed"}, 201)
	get := func(q string) int {
		return len(admin.json("GET", "/api/v1/transfers?"+q, nil, 200)["transfers"].([]any))
	}
	if get("limit=2&status=failed") != 1 || get("direction=received") != 1 || get("direction=sent&limit=3") != 3 {
		t.Fatal("filters")
	}
	id, _ := admin.tusUpload("/api/v1/uploads", []byte("x"), 10, meta("filename", "x.txt"))
	admin.json("POST", "/api/v1/shares", map[string]any{"fileIds": []string{id}, "expiresIn": int64(1) << 60}, 400)
}

func TestDiskReserve(t *testing.T) {
	_, ts := newTestServer(t, func(c *config.Config) { c.MinFreeSpace = 1 << 62 })
	admin := setupAdmin(t, ts)
	if _, r := admin.tusUpload("/api/v1/uploads", []byte("x"), 10, meta("filename", "x.txt")); r.StatusCode != http.StatusInsufficientStorage {
		t.Fatal("disk reserve", r.StatusCode)
	}
}
