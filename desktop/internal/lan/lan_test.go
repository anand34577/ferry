package lan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func init() { listenHost = "127.0.0.1" }

type testHost struct {
	dir      string
	pin      string
	accept   bool
	mu       sync.Mutex
	saved    []string
	final    string
	statuses []string
}

func (h *testHost) Self() Info                                                   { return SelfInfo("receiver", "", 0, nil) }
func (h *testHost) Receiving() bool                                              { return true }
func (h *testHost) PIN() string                                                  { return h.pin }
func (h *testHost) IsTrusted(string) bool                                        { return false }
func (h *testHost) Ask(Incoming) (bool, string)                                  { return h.accept, h.dir }
func (h *testHost) Seen(Peer)                                                    {}
func (h *testHost) RecvProgress(string, string, int64)                           {}
func (h *testHost) RecvStarted(string, string, []IncomingFile, []string, string) {}
func (h *testHost) RecvStatus(_, s, _ string) {
	h.mu.Lock()
	h.statuses = append(h.statuses, s)
	h.mu.Unlock()
}
func (h *testHost) RecvFileSaved(_, _, p string) {
	h.mu.Lock()
	h.saved = append(h.saved, p)
	h.mu.Unlock()
}
func (h *testHost) RecvFinished(_, s, _ string) {
	h.mu.Lock()
	h.final = s
	h.mu.Unlock()
}

func startReceiver(t *testing.T, h *testHost) (*Server, Peer) {
	t.Helper()
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Listen(id, h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	p, err := FetchInfo(context.Background(), "127.0.0.1", s.Port, "", "manual", nil, 5e9)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HTTPS || p.CertPin != id.Fingerprint || !p.Ferry {
		t.Fatalf("peer: %+v", p)
	}
	return s, p
}

func TestSendResumeVerify(t *testing.T) {
	h := &testHost{dir: t.TempDir(), accept: true}
	_, p := startReceiver(t, h)
	ctx := context.Background()
	data := bytes.Repeat([]byte("ferry-lan-"), 100_000)
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	self := SelfInfo("sender", "SENDERFP", Port, nil)
	prep, err := Prepare(ctx, p, self, map[string]FileMeta{"f1": {ID: "f1", FileName: "Photos/../trip/CON.txt", Size: int64(len(data))}}, "")
	if err != nil || prep == nil {
		t.Fatalf("prepare: %v %v", prep, err)
	}
	tok := prep.Tokens["f1"]
	// First half, then a "dropped connection", then resume from the receiver's offset.
	half := int64(len(data) / 2)
	if err := Upload(ctx, p, prep.SessionID, "f1", tok, 0, bytes.NewReader(data[:half]), half); err != nil {
		t.Fatal(err)
	}
	off, err := Offset(ctx, p, prep.SessionID, "f1", tok)
	if err != nil || off != half {
		t.Fatalf("offset %d %v", off, err)
	}
	if err := Upload(ctx, p, prep.SessionID, "f1", tok, off, bytes.NewReader(data[off:]), int64(len(data))-off); err != nil {
		t.Fatal(err)
	}
	if err := Verify(ctx, p, prep.SessionID, "f1", tok, sha); err != nil {
		t.Fatal(err)
	}
	if h.final != "completed" || len(h.saved) != 1 {
		t.Fatalf("final=%q saved=%v", h.final, h.saved)
	}
	want := filepath.Join(h.dir, "Photos", "trip", "_CON.txt")
	if h.saved[0] != want {
		t.Fatalf("saved to %s, want %s", h.saved[0], want)
	}
	got, _ := os.ReadFile(want)
	if !bytes.Equal(got, data) {
		t.Fatal("content differs")
	}
	// A pinned connection with the wrong certificate is refused.
	bad := p
	bad.CertPin = "00"
	if _, err := FetchInfo(ctx, p.IP, p.Port, bad.CertPin, "x", nil, 5e9); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("pin mismatch accepted: %v", err)
	}
}

// Stock LocalSend uploads with chunked encoding and no Content-Length.
func TestChunkedUpload(t *testing.T) {
	h := &testHost{dir: t.TempDir(), accept: true}
	_, p := startReceiver(t, h)
	ctx := context.Background()
	data := bytes.Repeat([]byte("chunked-"), 50_000)
	self := SelfInfo("localsend", "LSFP", Port, nil)
	files := map[string]FileMeta{"a": {ID: "a", FileName: "a.bin", Size: int64(len(data))}, "b": {ID: "b", FileName: "b.bin", Size: int64(len(data))}}
	prep, err := Prepare(ctx, p, self, files, "")
	if err != nil || prep == nil {
		t.Fatalf("prepare: %v %v", prep, err)
	}
	// Longer than declared is refused and nothing is saved for that file.
	if err := Upload(ctx, p, prep.SessionID, "b", prep.Tokens["b"], 0, bytes.NewReader(append(data, 'x')), -1); err == nil {
		t.Fatal("oversized chunked upload accepted")
	}
	if err := Upload(ctx, p, prep.SessionID, "a", prep.Tokens["a"], 0, bytes.NewReader(data), -1); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(h.dir, "a.bin"))
	if !bytes.Equal(got, data) {
		t.Fatal("content differs")
	}
}

func TestDeclineAndPIN(t *testing.T) {
	h := &testHost{dir: t.TempDir(), accept: false, pin: "1234"}
	_, p := startReceiver(t, h)
	files := map[string]FileMeta{"a": {ID: "a", FileName: "a.txt", Size: 1}}
	self := SelfInfo("sender", "FP", Port, nil)
	var pe *PeerError
	if _, err := Prepare(context.Background(), p, self, files, ""); !errors.As(err, &pe) || pe.Status != 401 {
		t.Fatalf("no pin: %v", err)
	}
	if _, err := Prepare(context.Background(), p, self, files, "1234"); !errors.As(err, &pe) || pe.Status != 403 {
		t.Fatalf("declined: %v", err)
	}
}

func TestPairCode(t *testing.T) {
	for _, c := range []struct {
		ip   string
		port int
	}{{"192.168.1.23", Port}, {"10.0.0.1", Port}, {"255.255.255.255", Port}, {"0.0.0.0", Port}, {"172.16.254.3", 53318}} {
		code := EncodePair(c.ip, c.port)
		ip, port, ok := DecodePair(code)
		if !ok || ip != c.ip || port != c.port {
			t.Fatalf("%v → %q → %s:%d", c, code, ip, port)
		}
	}
	if len(EncodePair("192.168.1.23", Port)) != 8 {
		t.Fatal("default port code should be XXX-XXXX")
	}
	tg, err := ParseTarget("ferry://peer?h=192.168.1.5%2C10.0.0.2&p=53318&f=ABC&n=Phone&s=https")
	if err != nil || len(tg.Hosts) != 2 || tg.Port != 53318 || tg.Fingerprint != "ABC" || tg.HTTPS == nil || !*tg.HTTPS {
		t.Fatalf("qr: %+v %v", tg, err)
	}
	if tg, err := ParseTarget("192.168.1.9:8000"); err != nil || tg.Hosts[0] != "192.168.1.9" || tg.Port != 8000 {
		t.Fatalf("manual: %+v %v", tg, err)
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"a/b": "a_b", "con.txt": "_con.txt", "x.  ": "x", "": "file", "..": "file", "ok é.txt": "ok é.txt"} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Current LocalSend refuses clients that present no certificate.
func TestClientPresentsCertificate(t *testing.T) {
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srvID, _ := LoadIdentity(t.TempDir())
	clientCert.Store(&id.Cert) // LoadIdentity of the second identity replaced it
	var got string
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{srvID.Cert}, ClientAuth: tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error { got = FingerprintOf(raw[0]); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, Info{Alias: "x", Fingerprint: "F"}) })}
	go srv.Serve(ln)
	defer srv.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	yes := true
	if _, err := FetchInfo(context.Background(), "127.0.0.1", port, "", "manual", &yes, 5e9); err != nil {
		t.Fatal(err)
	}
	if got != id.Fingerprint {
		t.Fatalf("server saw client cert %q, want %q", got, id.Fingerprint)
	}
}
