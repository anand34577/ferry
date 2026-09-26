package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ferry/internal/config"
)

// Link analytics, the audit-everything middleware, admin sign-in-as, and the shortener integration.
func TestAnalyticsAuditImpersonationShortener(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Method == "POST" {
			w.WriteHeader(201)
			io.WriteString(w, `{"id":"L1","shortUrl":"https://sho.rt/abc"}`)
			return
		}
		w.WriteHeader(204)
	}))
	defer short.Close()
	_, ts := newTestServer(t, func(c *config.Config) { c.ShortenerURL, c.ShortenerToken = short.URL, "sk_x" })
	adm := setupAdmin(t, ts)

	fid, _ := adm.tusUpload("/api/v1/uploads", []byte("hello"), 5, meta("filename", "a.txt"))
	sh := adm.json("POST", "/api/v1/shares", map[string]any{"fileIds": []string{fid}}, 201)
	if sh["shortUrl"] != "https://sho.rt/abc" {
		t.Fatalf("short url not set: %v", sh["shortUrl"])
	}
	token, id := sh["token"].(string), sh["id"].(string)

	anon := newClient(t, ts.URL)
	anon.do("GET", "/s/"+token, nil, nil)
	anon.do("GET", "/s/"+token+"/f/"+fid, nil, nil)
	anon.do("GET", "/s/"+token+"/f/"+fid, nil, map[string]string{"Range": "bytes=2-"}) // resumed: not a new download
	a := adm.json("GET", "/api/v1/shares/"+id+"/analytics", nil, 200)
	totals := a["totals"].(map[string]any)
	if totals["view"] != 1.0 || totals["download"] != 1.0 || a["visitors"] != 1.0 {
		t.Fatalf("analytics: %v visitors=%v", totals, a["visitors"])
	}

	// Another user can't read the owner's analytics.
	adm.json("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, 201)
	bob := newClient(t, ts.URL)
	bob.json("POST", "/api/v1/auth/login", map[string]string{"email": "bob@example.com", "password": "bob-password"}, 200)
	bob.json("GET", "/api/v1/shares/"+id+"/analytics", nil, 404)

	// Generic audit: a mutating call without its own audit entry is still recorded.
	bob.json("POST", "/api/v1/transfers/clear", map[string]any{}, 200)
	ev := adm.json("GET", "/api/v1/admin/audit?q=transfers_cleared", nil, 200)["events"].([]any)
	if len(ev) != 1 {
		t.Fatalf("want 1 transfers_cleared event, got %v", ev)
	}

	// Sign in as bob, act, return.
	users := adm.json("GET", "/api/v1/admin/users", nil, 200)["users"].([]any)
	var bobID string
	for _, u := range users {
		if m := u.(map[string]any); m["email"] == "bob@example.com" {
			bobID = m["id"].(string)
		}
	}
	adm.json("POST", "/api/v1/admin/users/"+bobID+"/impersonate", nil, 200)
	if me := adm.json("GET", "/api/v1/me", nil, 200); me["impersonator"] != "admin@example.com" {
		t.Fatalf("impersonator missing: %v", me)
	}
	adm.json("POST", "/api/v1/folders", map[string]string{"name": "via admin"}, 201)
	adm.json("POST", "/api/v1/auth/return", nil, 200)
	if me := adm.json("GET", "/api/v1/me", nil, 200); me["user"].(map[string]any)["role"] != "admin" {
		t.Fatalf("not back to admin: %v", me)
	}
	ev = adm.json("GET", "/api/v1/admin/audit?q=via+admin", nil, 200)["events"].([]any)
	if len(ev) != 1 || !strings.Contains(ev[0].(map[string]any)["detail"].(string), "by admin") {
		t.Fatalf("impersonated action not tagged: %v", ev)
	}

	// Deleting the link deletes its short link.
	adm.json("DELETE", "/api/v1/shares/"+id, nil, 200)
	adm.json("GET", "/api/v1/admin/audit?format=csv", nil, 200)
	for i := 0; i < 50; i++ { // unshorten runs in the background
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= 2 {
			break
		}
		http.Get(short.URL + "/ping") // yields time without sleeping
	}
	mu.Lock()
	defer mu.Unlock()
	if calls[0] != "POST /api/v1/links Bearer sk_x" || !strings.HasPrefix(strings.Join(calls, ","), "POST /api/v1/links Bearer sk_x,") || !strings.Contains(strings.Join(calls, ","), "DELETE /api/v1/links/L1") {
		t.Fatalf("shortener calls: %v", calls)
	}
}

// A plain SMTP relay (no TLS) with credentials works when FERRY_SMTP_SECURITY=none,
// including servers that only offer AUTH LOGIN.
func TestSMTPPlainRelay(t *testing.T) {
	for _, mech := range []string{"PLAIN", "LOGIN"} {
		t.Run(mech, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			got := make(chan string, 1)
			go fakeSMTP(ln, mech, got)
			s, _ := newTestServer(t, func(c *config.Config) {
				host, port, _ := net.SplitHostPort(ln.Addr().String())
				c.SMTPHost, c.SMTPUser, c.SMTPPass, c.SMTPSecurity = host, "me", "secret", "none"
				c.SMTPPort = atoi(port)
			})
			if err := s.sendMail("to@example.com", "Hi", "body"); err != nil {
				t.Fatal(err)
			}
			if auth := <-got; auth != "me/secret" {
				t.Fatalf("auth = %q", auth)
			}
		})
	}
}

func atoi(s string) (n int) {
	json.Unmarshal([]byte(s), &n)
	return
}

func fakeSMTP(ln net.Listener, mech string, got chan<- string) {
	c, err := ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	say := func(s string) { io.WriteString(c, s+"\r\n") }
	read := func() string { l, _ := rd.ReadString('\n'); return strings.TrimRight(l, "\r\n") }
	dec := func(s string) string { b, _ := base64.StdEncoding.DecodeString(s); return string(b) }
	say("220 fake")
	for {
		line := read()
		switch cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0]); cmd {
		case "EHLO", "HELO":
			say("250-fake")
			say("250 AUTH " + mech)
		case "AUTH":
			if mech == "PLAIN" {
				parts := strings.Split(dec(strings.Fields(line)[2]), "\x00")
				got <- parts[1] + "/" + parts[2]
			} else {
				say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
				u := dec(read())
				say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
				got <- u + "/" + dec(read())
			}
			say("235 ok")
		case "DATA":
			say("354 go")
			for read() != "." {
			}
			say("250 queued")
		case "QUIT":
			say("221 bye")
			return
		case "":
			return
		default:
			say("250 ok")
		}
	}
}

func TestPlainAuthOverPlainConnection(t *testing.T) {
	a := plainAuth{smtp.PlainAuth("", "me", "secret", "relay.lan")}
	if _, _, err := a.Start(&smtp.ServerInfo{Name: "relay.lan", Auth: []string{"PLAIN"}}); err != nil {
		t.Fatalf("plain relay rejected: %v", err)
	}
}

// Settings saved in Admin → Settings apply immediately, survive a restart, and never override the environment.
func TestAdminSettings(t *testing.T) {
	s, ts := newTestServer(t, func(c *config.Config) { c.FromEnv = map[string]bool{"FERRY_SITE_NAME": true} })
	adm := setupAdmin(t, ts)
	adm.json("PATCH", "/api/v1/admin/settings", map[string]any{"site_name": "Mine"}, 400) // locked by the environment
	adm.json("PATCH", "/api/v1/admin/settings", map[string]any{"max_upload_size": "banana"}, 400)
	adm.json("PATCH", "/api/v1/admin/settings", map[string]any{"oidc_issuer": "https://idp.example.com"}, 400) // needs a client id too
	adm.json("PATCH", "/api/v1/admin/settings", map[string]any{"max_upload_size": "2 gb", "allow_signup": "true", "smtp_password": "hunter2",
		"oidc_issuer": "https://idp.example.com/realms/x", "oidc_client_id": "ferry", "oidc_name": "Keycloak"}, 200)
	if s.conf().MaxUploadBytes != 2<<30 || !s.conf().AllowSignup || s.conf().SMTPPass != "hunter2" {
		t.Fatalf("not applied: %+v", s.conf())
	}
	info := adm.json("GET", "/api/v1/info", nil, 200)
	if info["oidc"].(map[string]any)["name"] != "Keycloak" || info["allowSignup"] != true {
		t.Fatalf("info not updated: %v", info)
	}
	for _, f := range adm.json("GET", "/api/v1/admin/settings", nil, 200)["settings"].([]any) {
		m := f.(map[string]any)
		switch m["key"] {
		case "smtp_password":
			if m["value"] != "" || m["hasValue"] != true {
				t.Fatalf("secret leaked: %v", m)
			}
		case "max_upload_size":
			if m["value"] != "2GB" || m["overridden"] != true {
				t.Fatalf("size: %v", m)
			}
		case "site_name":
			if m["locked"] != true {
				t.Fatalf("site_name should be locked: %v", m)
			}
		}
	}
	// Survives a restart.
	s.cfgp.Store(s.base)
	if err := s.loadSettings(context.Background()); err != nil || s.conf().MaxUploadBytes != 2<<30 {
		t.Fatalf("reload: %v %d", err, s.conf().MaxUploadBytes)
	}
	// null resets to the default.
	adm.json("PATCH", "/api/v1/admin/settings", map[string]any{"max_upload_size": nil}, 200)
	if s.conf().MaxUploadBytes != 0 {
		t.Fatal("reset failed")
	}
	newClient(t, ts.URL).json("GET", "/api/v1/admin/settings", nil, 401)
}

// A device that has the app open (it checks in regularly) shows as online; preferences follow the account.
func TestDeviceOnlineAndPrefs(t *testing.T) {
	_, ts := newTestServer(t, nil)
	adm := setupAdmin(t, ts)
	app := newClient(t, ts.URL)
	m := app.json("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.com", "password": "correct-horse",
		"device": map[string]string{"name": "Phone", "platform": "android"}}, 200)
	app.token = m["token"].(string)
	app.json("GET", "/api/v1/inbox", nil, 200)
	devs := adm.json("GET", "/api/v1/devices", nil, 200)["devices"].([]any)
	if len(devs) != 1 || devs[0].(map[string]any)["online"] != true {
		t.Fatalf("device should be online: %v", devs)
	}

	adm.json("PATCH", "/api/v1/me", map[string]any{"prefs": map[string]any{"theme": "nord", "sort": "date:desc"}}, 200)
	adm.json("PATCH", "/api/v1/me", map[string]any{"prefs": map[string]any{"sort": nil}}, 200)
	other := newClient(t, ts.URL) // another browser, same account
	other.json("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "correct-horse"}, 200)
	p := other.json("GET", "/api/v1/me", nil, 200)["prefs"].(map[string]any)
	if p["theme"] != "nord" || p["sort"] != nil {
		t.Fatalf("prefs: %v", p)
	}
	if ev := adm.json("GET", "/api/v1/admin/audit?q=profile_updated", nil, 200)["events"].([]any); len(ev) != 0 {
		t.Fatalf("preference changes shouldn't be audited: %v", ev)
	}
}

// Parallel uploads: parts are uploaded independently and joined into one verified file.
func TestParallelUpload(t *testing.T) {
	_, ts := newTestServer(t, nil)
	c := setupAdmin(t, ts)
	data := make([]byte, 3<<20+123)
	for i := range data {
		data[i] = byte(i * 7)
	}
	half := len(data) / 2
	var urls []string
	for _, part := range [][]byte{data[:half], data[half:]} {
		resp, _ := c.do("POST", "/api/v1/uploads", nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": strconv.Itoa(len(part)), "Upload-Concat": "partial"})
		if resp.StatusCode != 201 {
			t.Fatalf("create part: %d", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		resp, _ = c.do("PATCH", loc, part, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Offset": "0", "Content-Type": "application/offset+octet-stream"})
		if resp.StatusCode != 204 || resp.Header.Get("Ferry-File-Id") != "" {
			t.Fatalf("patch part: %d %v", resp.StatusCode, resp.Header)
		}
		urls = append(urls, ts.URL+loc)
	}
	resp, body := c.do("POST", "/api/v1/uploads", nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Concat": "final;" + strings.Join(urls, " "),
		"Upload-Metadata": meta("filename", "joined.bin")})
	if resp.StatusCode != 201 {
		t.Fatalf("final: %d %s", resp.StatusCode, body)
	}
	sum := sha256.Sum256(data)
	if resp.Header.Get("Ferry-Sha256") != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum %s", resp.Header.Get("Ferry-Sha256"))
	}
	_, got := c.do("GET", "/api/v1/files/"+resp.Header.Get("Ferry-File-Id")+"/content", nil, nil)
	if !bytes.Equal(got, data) {
		t.Fatal("joined content differs")
	}
	if ups := c.json("GET", "/api/v1/uploads", nil, 200)["uploads"].([]any); len(ups) != 0 {
		t.Fatalf("parts left behind: %v", ups)
	}
	// Parts can't be joined twice, and upload links don't accept parts.
	resp, _ = c.do("POST", "/api/v1/uploads", nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Concat": "final;" + strings.Join(urls, " "), "Upload-Metadata": meta("filename", "again.bin")})
	if resp.StatusCode != 404 {
		t.Fatalf("rejoin: %d", resp.StatusCode)
	}
}

// ZIP downloads have a known size and can be resumed with Range requests.
func TestResumableZip(t *testing.T) {
	s, ts := newTestServer(t, nil)
	c := setupAdmin(t, ts)
	big := bytes.Repeat([]byte("ferry-"), 200000)
	id1, _ := c.tusUpload("/api/v1/uploads", big, 1<<20, meta("filename", "big.txt"))
	id2, _ := c.tusUpload("/api/v1/uploads", []byte("héllo"), 16, meta("filename", "Grüße 😀.txt"))
	id3, _ := c.tusUpload("/api/v1/uploads", nil, 16, meta("filename", "empty.txt"))
	s.db.Exec(context.Background(), `UPDATE files SET crc32 = -1 WHERE id = ?`, id2) // uploaded before CRCs were stored
	url := "/api/v1/files/zip?files=" + id1 + "," + id2 + "," + id3 + "&name=bundle"
	resp, full := c.do("GET", url, nil, nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Length") != strconv.Itoa(len(full)) || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("zip: %d %v", resp.StatusCode, resp.Header)
	}
	zr, err := zip.NewReader(bytes.NewReader(full), int64(len(full)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{"big.txt": big, "Grüße 😀.txt": []byte("héllo"), "empty.txt": {}}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc) // also verifies the CRC-32
		rc.Close()
		if err != nil || !bytes.Equal(b, want[f.Name]) {
			t.Fatalf("%s: %v", f.Name, err)
		}
		delete(want, f.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing: %v", want)
	}
	// Resume from the middle of the first file.
	resp, tail := c.do("GET", url, nil, map[string]string{"Range": "bytes=500000-", "If-Range": resp.Header.Get("ETag")})
	if resp.StatusCode != 206 || !bytes.Equal(tail, full[500000:]) {
		t.Fatalf("range: %d, %d bytes", resp.StatusCode, len(tail))
	}
}

// Files over 4 GB get ZIP64 records that standard readers understand.
func TestZipPlanZip64(t *testing.T) {
	const size = 5 << 30
	plan, err := planZip([]entry{{Path: "huge.bin", File: &File{ID: "f", Blob: "b", Size: size, UpdatedAt: 1}}}, map[string]uint32{"f": 1234})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(planReaderAt{plan}, plan.size)
	if err != nil {
		t.Fatal(err)
	}
	if f := zr.File[0]; f.UncompressedSize64 != size || f.CRC32 != 1234 {
		t.Fatalf("got %d bytes, crc %d", f.UncompressedSize64, f.CRC32)
	}
}

// planReaderAt reads a plan with zeros for file data (enough to parse the archive's structure).
type planReaderAt struct{ p *zipPlan }

func (r planReaderAt) ReadAt(b []byte, off int64) (int, error) {
	n := 0
	for n < len(b) && off+int64(n) < r.p.size {
		pos := off + int64(n)
		for _, sg := range r.p.segs {
			if pos >= sg.start && pos < sg.start+sg.size {
				if sg.blob == "" {
					n += copy(b[n:], sg.mem[pos-sg.start:])
				} else {
					k := min(int64(len(b)-n), sg.start+sg.size-pos)
					clear(b[n : n+int(k)])
					n += int(k)
				}
				break
			}
		}
	}
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}
