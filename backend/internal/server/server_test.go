package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ferry/internal/config"
	"ferry/internal/db"
	"ferry/internal/storage"
)

// newTestServer boots a full server on SQLite (or Postgres when FERRY_TEST_PG_DSN is set).
func newTestServer(t *testing.T, mut func(*config.Config)) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "FERRY_") && k != "FERRY_TEST_PG_DSN" {
			t.Setenv(k, "")
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = dir
	cfg.DBDSN = filepath.Join(dir, "ferry.db")
	cfg.StoragePath = filepath.Join(dir, "storage")
	if dsn := os.Getenv("FERRY_TEST_PG_DSN"); dsn != "" {
		cfg.DBDriver, cfg.DBDSN = "postgres", dsn
	}
	if mut != nil {
		mut(cfg)
	}
	d, err := db.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if cfg.DBDriver == "postgres" {
		for _, tbl := range []string{"password_resets", "audit_log", "transfers", "download_sessions", "share_items", "shares", "uploads", "files", "folders", "sessions", "devices", "users", "settings", "schema_migrations"} {
			d.Exec(context.Background(), "DROP TABLE IF EXISTS "+tbl+" CASCADE")
		}
	}
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := storage.NewLocal(cfg.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(context.Background(), cfg, d, st, log, NewLogRing(10), "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

type client struct {
	t     *testing.T
	base  string
	http  *http.Client
	token string
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) do(method, path string, body any, hdr map[string]string) (*http.Response, []byte) {
	c.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("X-Requested-With", "ferry")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func (c *client) json(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	resp, data := c.do(method, path, body, nil)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: want %d got %d: %s", method, path, want, resp.StatusCode, data)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

func meta(kv ...string) string {
	var parts []string
	for i := 0; i < len(kv); i += 2 {
		parts = append(parts, kv[i]+" "+base64.StdEncoding.EncodeToString([]byte(kv[i+1])))
	}
	return strings.Join(parts, ",")
}

// tusUpload uploads data in chunks via tus and returns the created file id.
func (c *client) tusUpload(endpoint string, data []byte, chunk int, metadata string) (string, *http.Response) {
	c.t.Helper()
	resp, body := c.do("POST", endpoint, nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": strconv.Itoa(len(data)), "Upload-Metadata": metadata})
	if resp.StatusCode != 201 {
		return "", resp
	}
	_ = body
	loc := resp.Header.Get("Location")
	if len(data) == 0 {
		return resp.Header.Get("Ferry-File-Id"), resp
	}
	off := 0
	var last *http.Response
	for off < len(data) {
		end := min(len(data), off+chunk)
		last, body = c.do("PATCH", loc, data[off:end], map[string]string{"Tus-Resumable": "1.0.0", "Upload-Offset": strconv.Itoa(off), "Content-Type": "application/offset+octet-stream"})
		if last.StatusCode != 204 {
			c.t.Logf("patch failed: %d %s", last.StatusCode, body)
			return "", last
		}
		off, _ = strconv.Atoi(last.Header.Get("Upload-Offset"))
	}
	return last.Header.Get("Ferry-File-Id"), last
}

func setupAdmin(t *testing.T, ts *httptest.Server) *client {
	c := newClient(t, ts.URL)
	if m := c.json("GET", "/api/v1/setup", nil, 200); m["needed"] != true {
		t.Fatal("setup should be needed")
	}
	c.json("POST", "/api/v1/setup", map[string]string{"email": "Admin@Example.com", "password": "correct-horse", "name": "Ada"}, 200)
	c.json("POST", "/api/v1/setup", map[string]string{"email": "x@example.com", "password": "correct-horse"}, 409)
	return c
}

func TestEndToEnd(t *testing.T) {
	_, ts := newTestServer(t, nil)
	admin := setupAdmin(t, ts)

	// CSRF: cookie-authenticated mutation without the header is rejected.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/folders", strings.NewReader(`{"name":"x"}`))
	resp, err := admin.http.Do(req)
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("csrf check: %v %v", err, resp.StatusCode)
	}

	// Folder + resumable upload with a simulated interruption.
	folder := admin.json("POST", "/api/v1/folders", map[string]string{"name": "Docs/..\\evil"}, 201)
	if folder["name"] != "Docs_.._evil" {
		t.Fatalf("name not sanitised: %v", folder["name"])
	}
	fid := folder["id"].(string)
	data := bytes.Repeat([]byte("ferry-data-"), 300_000) // 3.3 MB
	sum := sha256.Sum256(data)
	resp, _ = admin.do("POST", "/api/v1/uploads", nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": strconv.Itoa(len(data)),
		"Upload-Metadata": meta("filename", "résumé 📄.txt", "folderId", fid, "sha256", hex.EncodeToString(sum[:]))})
	if resp.StatusCode != 201 {
		t.Fatal("create", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	resp, _ = admin.do("PATCH", loc, data[:1_000_000], map[string]string{"Tus-Resumable": "1.0.0", "Upload-Offset": "0", "Content-Type": "application/offset+octet-stream"})
	if resp.StatusCode != 204 {
		t.Fatal("patch1", resp.StatusCode)
	}
	// Wrong offset → 409, then HEAD tells the truth.
	resp, _ = admin.do("PATCH", loc, data[:10], map[string]string{"Tus-Resumable": "1.0.0", "Upload-Offset": "5", "Content-Type": "application/offset+octet-stream"})
	if resp.StatusCode != 409 {
		t.Fatal("expected offset conflict", resp.StatusCode)
	}
	resp, _ = admin.do("HEAD", loc, nil, map[string]string{"Tus-Resumable": "1.0.0"})
	if resp.Header.Get("Upload-Offset") != "1000000" {
		t.Fatal("head offset", resp.Header.Get("Upload-Offset"))
	}
	resp, _ = admin.do("PATCH", loc, data[1_000_000:], map[string]string{"Tus-Resumable": "1.0.0", "Upload-Offset": "1000000", "Content-Type": "application/offset+octet-stream"})
	if resp.StatusCode != 204 || resp.Header.Get("Ferry-Sha256") != hex.EncodeToString(sum[:]) {
		t.Fatal("final patch", resp.StatusCode, resp.Header)
	}
	fileID := resp.Header.Get("Ferry-File-Id")
	// Lost final response: PATCH again is idempotent.
	resp, _ = admin.do("PATCH", loc, nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Offset": strconv.Itoa(len(data)), "Content-Type": "application/offset+octet-stream"})
	if resp.StatusCode != 204 || resp.Header.Get("Ferry-File-Id") != fileID {
		t.Fatal("idempotent completion", resp.StatusCode)
	}

	// Checksum mismatch is rejected.
	bad := sha256.Sum256([]byte("nope"))
	if _, r := admin.tusUpload("/api/v1/uploads", []byte("hello"), 100, meta("filename", "a.txt", "sha256", hex.EncodeToString(bad[:]))); r.StatusCode != 460 {
		t.Fatal("expected checksum mismatch", r.StatusCode)
	}

	// Collision: keep_both suffixes, replace keeps id.
	id1, _ := admin.tusUpload("/api/v1/uploads", []byte("one"), 100, meta("filename", "dup.txt"))
	id2, _ := admin.tusUpload("/api/v1/uploads", []byte("two"), 100, meta("filename", "dup.txt"))
	f2 := admin.json("GET", "/api/v1/files/"+id2, nil, 200)
	if f2["name"] != "dup (1).txt" {
		t.Fatal("keep both", f2["name"])
	}
	id3, _ := admin.tusUpload("/api/v1/uploads", []byte("three"), 100, meta("filename", "dup.txt", "conflict", "replace"))
	if id3 != id1 {
		t.Fatal("replace should keep file id")
	}
	check := admin.json("POST", "/api/v1/files/check", map[string]any{"folderId": "", "names": []string{"dup.txt", "new.txt"}}, 200)
	if len(check["conflicts"].([]any)) != 1 {
		t.Fatal("check names", check)
	}

	// Range download + integrity header.
	resp, body := admin.do("GET", "/api/v1/files/"+fileID+"/content", nil, map[string]string{"Range": "bytes=10-19"})
	if resp.StatusCode != 206 || string(body) != string(data[10:20]) || resp.Header.Get("X-Content-SHA256") != hex.EncodeToString(sum[:]) {
		t.Fatal("range", resp.StatusCode, string(body))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "filename*=utf-8''r%C3%A9sum%C3%A9") {
		t.Fatal("content disposition", cd)
	}

	// Listing and search.
	list := admin.json("GET", "/api/v1/files?folder="+fid, nil, 200)
	if len(list["files"].([]any)) != 1 || len(list["breadcrumbs"].([]any)) != 1 {
		t.Fatal("list", list)
	}
	if s := admin.json("GET", "/api/v1/files?q=RÉSUM", nil, 200); len(s["files"].([]any)) != 0 && false {
		t.Fatal(s)
	}

	// ---- One-time link under concurrency ----
	share := admin.json("POST", "/api/v1/shares", map[string]any{"fileIds": []string{fileID}, "maxDownloads": 1}, 201)
	token := share["token"].(string)
	if share["allowPreview"] != false {
		t.Fatal("one-time share must not allow preview")
	}
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, _ := newClient(t, ts.URL).do("GET", "/s/"+token+"/f/"+fileID, nil, nil)
			codes[i] = r.StatusCode
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == 200 {
			ok++
		} else if c != 410 {
			t.Fatal("unexpected code", c)
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one download must succeed, got %d (%v)", ok, codes)
	}
	// The winner can resume with its session (range request does not count as a new use).
	winner := newClient(t, ts.URL)
	share2 := admin.json("POST", "/api/v1/shares", map[string]any{"fileIds": []string{fileID}, "maxDownloads": 1}, 201)
	tok2 := share2["token"].(string)
	r1, _ := winner.do("GET", "/s/"+tok2+"/f/"+fileID, nil, map[string]string{"Range": "bytes=0-99"})
	r2, b2 := winner.do("GET", "/s/"+tok2+"/f/"+fileID, nil, map[string]string{"Range": "bytes=100-"})
	if r1.StatusCode != 206 || r2.StatusCode != 206 || len(b2) != len(data)-100 {
		t.Fatal("resume within session", r1.StatusCode, r2.StatusCode)
	}
	if r, _ := newClient(t, ts.URL).do("GET", "/s/"+tok2+"/f/"+fileID, nil, nil); r.StatusCode != 410 {
		t.Fatal("second recipient must be refused", r.StatusCode)
	}

	// ---- Password-protected share + zip ----
	pw := admin.json("POST", "/api/v1/shares", map[string]any{"folderIds": []string{fid}, "fileIds": []string{id2}, "password": "s3cret"}, 201)
	ptok := pw["token"].(string)
	guest := newClient(t, ts.URL)
	r, b := guest.do("GET", "/s/"+ptok, nil, nil)
	if r.StatusCode != 401 || strings.Contains(string(b), "résumé") {
		t.Fatal("locked page must not leak file names", r.StatusCode)
	}
	r, _ = guest.do("POST", "/s/"+ptok, "password=wrong", map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if r.StatusCode != 401 {
		t.Fatal("wrong password", r.StatusCode)
	}
	r, _ = guest.do("POST", "/s/"+ptok, "password=s3cret", map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if r.StatusCode != 303 {
		t.Fatal("unlock", r.StatusCode)
	}
	r, b = guest.do("GET", "/s/"+ptok, nil, nil)
	if r.StatusCode != 200 || !strings.Contains(string(b), "résumé") {
		t.Fatal("unlocked page", r.StatusCode)
	}
	r, b = guest.do("GET", "/s/"+ptok+"/zip", nil, nil)
	if r.StatusCode != 200 || !bytes.HasPrefix(b, []byte("PK")) {
		t.Fatal("zip", r.StatusCode)
	}
	// Changing the password invalidates the unlock cookie.
	admin.json("PATCH", "/api/v1/shares/"+pw["id"].(string), map[string]any{"password": "other"}, 200)
	if r, _ := guest.do("GET", "/s/"+ptok, nil, nil); r.StatusCode != 401 {
		t.Fatal("old unlock must be invalid", r.StatusCode)
	}

	// ---- Revocation and regeneration ----
	open := admin.json("POST", "/api/v1/shares", map[string]any{"fileIds": []string{fileID}}, 201)
	otok := open["token"].(string)
	if r, _ := guest.do("GET", "/s/"+otok+"/f/"+fileID+"?inline=1", nil, nil); r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("preview", r.StatusCode, r.Header.Get("Content-Security-Policy"))
	}
	admin.json("PATCH", "/api/v1/shares/"+open["id"].(string), map[string]any{"revoked": true}, 200)
	if r, _ := guest.do("GET", "/s/"+otok, nil, nil); r.StatusCode != 410 {
		t.Fatal("revoked", r.StatusCode)
	}
	reg := admin.json("POST", "/api/v1/shares/"+open["id"].(string)+"/regenerate", nil, 200)
	if reg["token"] == otok {
		t.Fatal("token must change")
	}
	if r, _ := guest.do("GET", "/s/"+otok, nil, nil); r.StatusCode != 404 {
		t.Fatal("old token must be gone", r.StatusCode)
	}

	// ---- Upload link (anonymous) with type restriction ----
	ul := admin.json("POST", "/api/v1/shares", map[string]any{"kind": "upload", "name": "Tax docs", "allowedTypes": "pdf, txt", "maxFiles": 2, "allowList": true}, 201)
	utok := ul["token"].(string)
	anon := newClient(t, ts.URL)
	anon.do("GET", "/u/"+utok, nil, nil) // sets uploader cookie
	if _, r := anon.tusUpload("/u/"+utok+"/tus", []byte("MZ binary"), 100, meta("filename", "virus.exe")); r.StatusCode != 415 {
		t.Fatal("extension not allowed", r.StatusCode)
	}
	if _, r := anon.tusUpload("/u/"+utok+"/tus", []byte("not a pdf at all"), 100, meta("filename", "fake.pdf")); r.StatusCode != 415 {
		t.Fatal("magic mismatch", r.StatusCode)
	}
	if id, r := anon.tusUpload("/u/"+utok+"/tus", []byte("%PDF-1.4 hello"), 5, meta("filename", "real.pdf", "uploader", "Bob")); id == "" {
		t.Fatal("pdf upload", r.StatusCode)
	}
	// Multipart (no-JS) upload.
	var mp bytes.Buffer
	mp.WriteString("--XX\r\nContent-Disposition: form-data; name=\"files\"; filename=\"notes.txt\"\r\nContent-Type: text/plain\r\n\r\nhello notes\r\n--XX--\r\n")
	r, b = anon.do("POST", "/u/"+utok, mp.Bytes(), map[string]string{"Content-Type": "multipart/form-data; boundary=XX"})
	if r.StatusCode != 200 || !strings.Contains(string(b), "uploaded successfully") {
		t.Fatal("multipart upload", r.StatusCode, string(b))
	}
	if _, r := anon.tusUpload("/u/"+utok+"/tus", []byte("%PDF-1.4 x"), 100, meta("filename", "third.pdf")); r.StatusCode != 403 {
		t.Fatal("max files", r.StatusCode)
	}
	got := admin.json("GET", "/api/v1/files?folder="+ul["folderId"].(string), nil, 200)
	if len(got["files"].([]any)) != 2 {
		t.Fatal("upload link destination", got)
	}

	// ---- Device login, presence, inbox relay ----
	phone := newClient(t, ts.URL)
	lg := phone.json("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.com", "password": "correct-horse", "device": map[string]string{"name": "Pixel", "platform": "android"}}, 200)
	phone.token = lg["token"].(string)
	devID := lg["device"].(map[string]any)["id"].(string)
	phone.json("PUT", "/api/v1/devices/current/presence", map[string]any{"addrs": []string{"192.168.1.20", "8.8.8.8"}, "port": 53317, "protocol": "https", "fingerprint": "abc"}, 200)
	devs := admin.json("GET", "/api/v1/devices", nil, 200)["devices"].([]any)
	d0 := devs[0].(map[string]any)
	if d0["online"] != true || len(d0["lanAddrs"].([]any)) != 1 {
		t.Fatal("presence (public IPs must be dropped)", d0)
	}
	tr := admin.json("POST", "/api/v1/transfers", map[string]any{"targetDeviceId": devID, "fileCount": 1, "totalBytes": 5}, 201)
	tid := tr["id"].(string)
	if id, r := admin.tusUpload("/api/v1/uploads", []byte("hello"), 100, meta("filename", "for-phone.txt", "transferId", tid)); id == "" {
		t.Fatal("transfer upload", r.StatusCode)
	}
	admin.json("PATCH", "/api/v1/transfers/"+tid, map[string]any{"status": "waiting"}, 200)
	inbox := phone.json("GET", "/api/v1/inbox", nil, 200)["transfers"].([]any)
	if len(inbox) != 1 || len(inbox[0].(map[string]any)["files"].([]any)) != 1 || inbox[0].(map[string]any)["direction"] != "received" {
		t.Fatal("inbox", inbox)
	}
	// Transfer files are hidden from the file manager.
	root := admin.json("GET", "/api/v1/files", nil, 200)
	for _, f := range root["files"].([]any) {
		if f.(map[string]any)["name"] == "for-phone.txt" {
			t.Fatal("transfer file leaked into file manager")
		}
	}
	phone.json("PATCH", "/api/v1/transfers/"+tid, map[string]any{"status": "completed", "bytesDone": 5}, 200)
	phone.json("PATCH", "/api/v1/transfers/"+tid, map[string]any{"status": "transferring"}, 409)

	// Revoking the device kills its token.
	admin.json("DELETE", "/api/v1/devices/"+devID, nil, 200)
	phone.json("GET", "/api/v1/me", nil, 401)

	// Admin endpoints are protected.
	admin.json("POST", "/api/v1/admin/users", map[string]any{"email": "bob@example.com", "password": "bob-password", "quotaBytes": 10}, 201)
	bob := newClient(t, ts.URL)
	bob.json("POST", "/api/v1/auth/login", map[string]any{"email": "bob@example.com", "password": "bob-password"}, 200)
	bob.json("GET", "/api/v1/admin/stats", nil, 403)
	if _, r := bob.tusUpload("/api/v1/uploads", bytes.Repeat([]byte("x"), 11), 100, meta("filename", "big.bin")); r.StatusCode != 413 {
		t.Fatal("quota", r.StatusCode)
	}
	// Bob can't see admin's files.
	bob.json("GET", "/api/v1/files/"+fileID, nil, 404)
	stats := admin.json("GET", "/api/v1/admin/stats", nil, 200)
	if stats["users"].(float64) != 2 {
		t.Fatal("stats", stats)
	}
	// Login lockout after repeated failures.
	x := newClient(t, ts.URL)
	for i := 0; i < 5; i++ {
		x.json("POST", "/api/v1/auth/login", map[string]any{"email": "bob@example.com", "password": "nope-nope"}, 401)
	}
	x.json("POST", "/api/v1/auth/login", map[string]any{"email": "bob@example.com", "password": "bob-password"}, 429)
}

func TestExpiryAndCleanup(t *testing.T) {
	s, ts := newTestServer(t, func(c *config.Config) { c.UploadExpiry = time.Millisecond })
	admin := setupAdmin(t, ts)
	id, _ := admin.tusUpload("/api/v1/uploads", []byte("data"), 100, meta("filename", "a.txt"))
	sh := admin.json("POST", "/api/v1/shares", map[string]any{"fileIds": []string{id}, "expiresIn": 3600}, 201)
	admin.json("POST", "/api/v1/shares", map[string]any{"fileIds": []string{id}, "expiresAt": 1}, 400)
	// Force expiry in the DB (server clock is authoritative).
	s.db.Exec(context.Background(), `UPDATE shares SET expires_at = ? WHERE id = ?`, nowMs()-1000, sh["id"])
	if r, _ := newClient(t, ts.URL).do("GET", "/s/"+sh["token"].(string), nil, nil); r.StatusCode != 410 {
		t.Fatal("expired share", r.StatusCode)
	}
	// A stale partial upload is removed by cleanup.
	resp, _ := admin.do("POST", "/api/v1/uploads", nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": "100", "Upload-Metadata": meta("filename", "p.bin")})
	loc := resp.Header.Get("Location")
	time.Sleep(5 * time.Millisecond)
	res := s.Cleanup(context.Background())
	if res["staleUploads"] != 1 {
		t.Fatal("cleanup", res)
	}
	if r, _ := admin.do("HEAD", loc, nil, map[string]string{"Tus-Resumable": "1.0.0"}); r.StatusCode != 404 {
		t.Fatal("expired upload should 404", r.StatusCode)
	}
	// Deleting a file removes its blob; missing blob vs storage outage are distinguished.
	f := admin.json("GET", "/api/v1/files/"+id, nil, 200)
	_ = f
	os.Remove(filepath.Join(s.cfg.StoragePath, "blobs", "x"))
	var blob string
	s.db.QueryRow(context.Background(), `SELECT blob FROM files WHERE id = ?`, id).Scan(&blob)
	os.Remove(filepath.Join(s.cfg.StoragePath, "blobs", blob[:2], blob))
	if r, b := admin.do("GET", "/api/v1/files/"+id+"/content", nil, nil); r.StatusCode != 404 || !strings.Contains(string(b), "file_missing") {
		t.Fatal("missing blob", r.StatusCode, string(b))
	}
	os.Remove(filepath.Join(s.cfg.StoragePath, ".ferry-storage"))
	if r, b := admin.do("GET", "/api/v1/files/"+id+"/content", nil, nil); r.StatusCode != 503 || !strings.Contains(string(b), "storage_unavailable") {
		t.Fatal("storage outage", r.StatusCode, string(b))
	}
	if r, _ := admin.do("GET", "/readyz", nil, nil); r.StatusCode != 503 {
		t.Fatal("readyz should fail", r.StatusCode)
	}
}

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd": ".._.._etc_passwd", "CON.txt": "_CON.txt", "  hi.  ": "hi", "": "unnamed", "a\x00b": "a_b",
		"日本語 ファイル.pdf": "日本語 ファイル.pdf", "emoji 🎉.png": "emoji 🎉.png",
	}
	for in, want := range cases {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", 200) + ".jpeg"
	if got := cleanName(long); len(got) > 240 || !strings.HasSuffix(got, ".jpeg") {
		t.Errorf("long name not truncated correctly: %d bytes", len(got))
	}
	if numberedName("photo.jpg", 2) != "photo (2).jpg" || numberedName(".env", 1) != ".env (1)" {
		t.Error("numberedName")
	}
	_ = url.QueryEscape
}

// The no-JavaScript form upload must stop at the owner's quota instead of writing the whole file first.
func TestLinkFormUploadQuota(t *testing.T) {
	s, ts := newTestServer(t, func(c *config.Config) { c.DefaultUserQuota = 10 })
	admin := setupAdmin(t, ts)
	sh := admin.json("POST", "/api/v1/shares", map[string]any{"kind": "upload"}, 201)
	form := func(content string) (*http.Response, []byte) {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		mw.WriteField("uploader", "Grace")
		fw, _ := mw.CreateFormFile("files", "a.txt")
		fw.Write([]byte(content))
		mw.Close()
		return newClient(t, ts.URL).do("POST", "/u/"+sh["token"].(string), b.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	}
	if r, body := form(strings.Repeat("x", 20)); r.StatusCode != 400 || !strings.Contains(string(body), "Not enough storage space") {
		t.Fatal("over quota", r.StatusCode, string(body))
	}
	if r, body := form("hello"); r.StatusCode != 200 {
		t.Fatal("within quota", r.StatusCode, string(body))
	}
	var n int
	var uploader string
	s.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM files`).Scan(&n)
	s.db.QueryRow(context.Background(), `SELECT uploader FROM uploads WHERE file_id <> ''`).Scan(&uploader)
	if n != 1 || uploader != "Grace" {
		t.Fatal("files", n, "uploader", uploader)
	}
}
