package ferry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type memStore map[string]string

func (m memStore) Get(k string) string { return m[k] }
func (m memStore) Set(k, v string) {
	if v == "" {
		delete(m, k)
	} else {
		m[k] = v
	}
}

// Runs against a real Ferry server:
//
//	FERRY_TEST_SERVER=http://127.0.0.1:8080 FERRY_TEST_EMAIL=… FERRY_TEST_PASSWORD=… go test ./internal/ferry/
func TestAgainstServer(t *testing.T) {
	base, email, pw := os.Getenv("FERRY_TEST_SERVER"), os.Getenv("FERRY_TEST_EMAIL"), os.Getenv("FERRY_TEST_PASSWORD")
	if base == "" {
		t.Skip("FERRY_TEST_SERVER not set")
	}
	ctx := context.Background()
	if _, err := New(base, "").Info(ctx); err != nil {
		t.Fatal(err)
	}
	var fe *Error
	if _, err := New(base, "").Login(ctx, email, "wrong-password", "", "PC", "", "test"); !errors.As(err, &fe) || fe.Status != 401 {
		t.Fatalf("wrong password: %v", err)
	}
	// Two devices of the same account: this PC and "phone".
	pc, err := New(base, "").Login(ctx, email, pw, "", "Test PC", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	ph, err := New(base, "").Login(ctx, email, pw, "", "Test phone", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	c, phone := New(base, pc.Token), New(base, ph.Token)
	defer c.Logout(ctx)
	defer phone.Logout(ctx)
	again, err := New(base, "").Login(ctx, email, pw, "", "Test PC", pc.Device.ID, "test")
	if err != nil || again.Device.ID != pc.Device.ID {
		t.Fatalf("re-login should keep the device: %v %v", again, err)
	}
	New(base, again.Token).Logout(ctx)

	dir := t.TempDir()
	data := bytes.Repeat([]byte("ferry-desktop-"), 2_000_000) // 28 MB: two tus chunks
	src := filepath.Join(dir, "big file ü.bin")
	os.WriteFile(src, data, 0o644)
	sum := sha256.Sum256(data)

	// Link: upload into Sent/Trip and share the folder.
	sent, err := c.EnsureFolder(ctx, "Sent", "")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := c.EnsureFolder(ctx, "Sent", ""); err != nil || again != sent {
		t.Fatalf("EnsureFolder not idempotent: %s %s %v", sent, again, err)
	}
	trip, _ := c.EnsureFolder(ctx, "Trip", sent)
	st := memStore{}
	var last int64
	fid, err := c.Upload(ctx, src, "big file ü.bin", map[string]string{"folderId": trip}, "k1", st, func(n int64) { last = n })
	if err != nil || fid == "" || last != int64(len(data)) || len(st) != 0 {
		t.Fatalf("upload: %q %v progress=%d store=%v", fid, err, last, st)
	}
	sh, err := c.CreateShare(ctx, nil, []string{trip}, ShareOpts{ExpiresIn: 3600, MaxDownloads: 1, Password: "pass1234"})
	if err != nil || sh.Link() == "" {
		t.Fatalf("share: %+v %v", sh, err)
	}

	// Relay: PC → phone through the server, then the phone downloads from its inbox.
	devs, err := c.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, d := range devs {
		if d.ID == ph.Device.ID {
			target = d.ID
		}
	}
	if target == "" {
		t.Fatalf("phone not in device list: %+v", devs)
	}
	tid, err := c.CreateTransfer(ctx, target, 1, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(ctx, src, "big file ü.bin", map[string]string{"transferId": tid}, "k2", st, func(int64) {}); err != nil {
		t.Fatal(err)
	}
	if err := c.PatchTransfer(ctx, tid, "waiting", -1, ""); err != nil {
		t.Fatal(err)
	}
	inbox, err := phone.Inbox(ctx)
	if err != nil || len(inbox) == 0 || inbox[0].ID != tid || len(inbox[0].Files) != 1 {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	f := inbox[0].Files[0]
	dst := filepath.Join(dir, "download.part")
	os.WriteFile(dst, data[:5_000_000], 0o644) // an interrupted earlier download resumes with Range
	if err := phone.Download(ctx, f.ID, dst, f.Size, f.SHA256, func(int64) {}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if hex.EncodeToString(sum[:]) != f.SHA256 || !bytes.Equal(got, data) {
		t.Fatal("downloaded content differs")
	}
	if err := phone.PatchTransfer(ctx, tid, "completed", f.Size, ""); err != nil {
		t.Fatal(err)
	}
	if tr, err := c.GetTransfer(ctx, tid); err != nil || tr.Status != "completed" {
		t.Fatalf("sender view: %+v %v", tr, err)
	}
	// A corrupted partial file is detected and fixed on retry.
	os.WriteFile(dst, bytes.Repeat([]byte{0}, 1000), 0o644)
	if err := phone.Download(ctx, f.ID, dst, f.Size, f.SHA256, func(int64) {}); !errors.As(err, &fe) || fe.Code != "checksum_mismatch" {
		t.Fatalf("corrupt partial not detected: %v", err)
	}
	if err := phone.Download(ctx, f.ID, dst, f.Size, f.SHA256, func(int64) {}); err != nil {
		t.Fatalf("retry after corruption: %v", err)
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{"files.example.com/": "https://files.example.com", "HTTP://10.0.0.2:8080": "http://10.0.0.2:8080", " https://x.y ": "https://x.y"} {
		if got, _ := NormalizeURL(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
