package lan

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// IncomingFile is one file a sender offers.
type IncomingFile struct {
	Name string `json:"name"` // relative path, "/"-separated
	Size int64  `json:"size"`
}

// Incoming is a request to receive files, shown to the user for a decision.
type Incoming struct {
	ID          string         `json:"id"`
	Alias       string         `json:"alias"`
	Model       string         `json:"model"`
	Fingerprint string         `json:"fingerprint"` // empty unless the sender proved it (see prepare)
	Trusted     bool           `json:"trusted"`
	Files       []IncomingFile `json:"files"`
	TotalBytes  int64          `json:"totalBytes"`
}

// Host is what the receiver needs from the app.
type Host interface {
	Self() Info
	Receiving() bool
	PIN() string // "" = no PIN
	IsTrusted(fingerprint string) bool
	// Ask blocks until the user decides (or a timeout); it returns the folder to save into when accepted.
	Ask(in Incoming) (accept bool, saveDir string)
	Seen(p Peer)
	RecvStarted(transferID, peer string, files []IncomingFile, ids []string, saveDir string)
	RecvProgress(transferID, fileID string, bytes int64)
	RecvStatus(transferID, status, note string)
	RecvFileSaved(transferID, fileID, path string)
	RecvFinished(transferID, status, errMsg string)
}

type rfile struct {
	id, name, token, expected string
	dirs                      []string
	size                      int64
	tmp, final                string
	received                  atomic.Int64
	sha                       string
	done, verified            bool
	busy                      atomic.Bool
}

type session struct {
	id, alias, transferID, dir string
	ferry                      bool
	files                      map[string]*rfile
	lastActivity               atomic.Int64
	cancelled                  atomic.Bool
}

func (s *session) active() bool {
	if s.cancelled.Load() || time.Since(time.UnixMilli(s.lastActivity.Load())) > 10*time.Minute {
		return false
	}
	for _, f := range s.files {
		if !f.done {
			return true
		}
	}
	return false
}

// Server is the LocalSend v2 receiver (HTTPS). One incoming session at a time, like LocalSend.
// Nothing is accepted until the user approves (or the sender is a proven, trusted device with auto-accept).
type Server struct {
	id   *Identity
	host Host
	log  *slog.Logger
	Port int

	srv          *http.Server
	mu           sync.Mutex
	sess         *session
	pinFails     int
	pinLockUntil time.Time
}

// listenHost is "" (all interfaces) in the app; tests use loopback so they never trigger a firewall prompt.
var listenHost = ""

// Listen binds the first free port from 53317 (LocalSend's default) upwards and starts serving.
func Listen(id *Identity, host Host, log *slog.Logger) (*Server, error) {
	s := &Server{id: id, host: host, log: log}
	cfg := &tls.Config{Certificates: []tls.Certificate{id.Cert}, MinVersion: tls.VersionTLS12}
	var ln net.Listener
	var err error
	for p := Port; p <= Port+12; p++ {
		if ln, err = tls.Listen("tcp", net.JoinHostPort(listenHost, strconv.Itoa(p)), cfg); err == nil {
			s.Port = p
			break
		}
	}
	if ln == nil {
		return nil, fmt.Errorf("no free port for receiving (%d–%d): %w", Port, Port+12, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.route)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelDebug)}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *Server) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.srv.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

func (s *Server) self() Info {
	i := s.host.Self()
	i.Port = s.Port
	return i
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			s.log.Error("lan handler panic", "panic", v)
			fail(w, 500, "internal error")
		}
	}()
	p := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case strings.HasSuffix(p, "/info"):
		writeJSON(w, 200, s.self())
	case strings.HasSuffix(p, "/register"):
		s.register(w, r)
	case p == API+"/prepare-upload" && r.Method == http.MethodPost:
		s.prepare(w, r)
	case p == API+"/upload" && r.Method == http.MethodPost:
		s.upload(w, r)
	case p == API+"/cancel" && r.Method == http.MethodPost:
		s.cancel(w, r)
	case p == API+"/ferry/offset":
		s.offset(w, r)
	case p == API+"/ferry/verify" && r.Method == http.MethodPost:
		s.verify(w, r)
	default:
		fail(w, 404, "Not found")
	}
}

func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var i Info
	if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&i) == nil && i.Fingerprint != "" && !strings.EqualFold(i.Fingerprint, s.id.Fingerprint) {
		s.host.Seen(peerFrom(i, remoteIP(r), "multicast", Port, true))
	}
	writeJSON(w, 200, s.self())
}

func (s *Server) prepare(w http.ResponseWriter, r *http.Request) {
	if !s.host.Receiving() {
		fail(w, 403, "This device is not receiving right now.")
		return
	}
	s.mu.Lock()
	busy := s.sess != nil && s.sess.active()
	if pin := s.host.PIN(); !busy && pin != "" {
		// A short PIN falls to brute force in minutes without a limit: 5 misses lock it for a minute.
		locked := time.Now().Before(s.pinLockUntil)
		if !locked && r.URL.Query().Get("pin") != pin {
			if s.pinFails++; s.pinFails >= 5 {
				s.pinFails, s.pinLockUntil = 0, time.Now().Add(time.Minute)
			}
			locked = true
		}
		if locked {
			s.mu.Unlock()
			fail(w, 401, "PIN required")
			return
		}
		s.pinFails = 0
	}
	s.mu.Unlock()
	if busy {
		fail(w, 409, "Busy with another transfer")
		return
	}
	var req prepareReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		fail(w, 400, "Invalid request")
		return
	}
	if len(req.Files) == 0 {
		w.WriteHeader(204)
		return
	}
	if len(req.Files) > 10000 {
		fail(w, 413, "Too many files")
		return
	}
	var files []*rfile
	var offer []IncomingFile
	var total int64
	for k, m := range req.Files {
		dirs, name := SafeRelativePath(m.FileName)
		id := m.ID
		if id == "" {
			id = k
		}
		size := max(m.Size, 0)
		files = append(files, &rfile{id: id, name: name, dirs: dirs, size: size, token: randHex(16), expected: strings.ToLower(m.SHA256)})
		offer = append(offer, IncomingFile{Name: path.Join(append(append([]string{}, dirs...), name)...), Size: size})
		total += size
	}
	// The fingerprint in the request is self-asserted, and every device multicasts its own, so anyone on the
	// LAN could claim a trusted one. A trusted claim only counts when the sender's HTTPS server proves it
	// holds that certificate; an unproven claim is dropped, so it can neither auto-accept nor show as trusted.
	claimed := req.Info.Fingerprint
	trusted := claimed != "" && s.host.IsTrusted(claimed) && s.proves(r.Context(), remoteIP(r), req.Info.Port, claimed)
	if claimed != "" && s.host.IsTrusted(claimed) && !trusted {
		claimed = ""
	}
	in := Incoming{ID: randHex(8), Alias: clip(firstNonEmpty(strings.TrimSpace(req.Info.Alias), "Unknown device"), 60), Model: clip(req.Info.DeviceModel, 60),
		Fingerprint: claimed, Trusted: trusted, Files: offer, TotalBytes: total}
	ok, dir := s.host.Ask(in)
	if !ok {
		fail(w, 403, "Declined")
		return
	}
	sess := &session{id: randHex(16), alias: in.Alias, ferry: req.Info.Ferry >= 1, files: map[string]*rfile{}, dir: dir}
	sess.transferID = "lan-" + sess.id
	sess.lastActivity.Store(time.Now().UnixMilli())
	tokens := map[string]string{}
	var ids []string
	for _, f := range files {
		sess.files[f.id] = f
		tokens[f.id] = f.token
		ids = append(ids, f.id)
	}
	s.mu.Lock()
	s.sess = sess
	s.mu.Unlock()
	s.host.RecvStarted(sess.transferID, in.Alias, offer, ids, dir)
	writeJSON(w, 200, prepareResp{SessionID: sess.id, Files: tokens})
}

// proves reports whether an HTTPS server at the sender's address presents the certificate whose hash is fp.
func (s *Server) proves(ctx context.Context, ip string, port int, fp string) bool {
	if port <= 0 {
		port = Port
	}
	yes := true
	_, err := FetchInfo(ctx, ip, port, fp, "verify", &yes, 4*time.Second)
	return err == nil
}

func (s *Server) authorize(r *http.Request) (*session, *rfile) {
	q := r.URL.Query()
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess == nil || sess.id != q.Get("sessionId") || sess.cancelled.Load() {
		return nil, nil
	}
	// No IP check: per-file tokens are secrets, and the sender's IP may change after a network switch.
	f := sess.files[q.Get("fileId")]
	if f == nil || f.token != q.Get("token") {
		return nil, nil
	}
	return sess, f
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	sess, f := s.authorize(r)
	if f == nil {
		fail(w, 403, "Invalid session or token")
		return
	}
	if f.done {
		writeJSON(w, 200, map[string]any{})
		return
	}
	if r.ContentLength < 0 {
		fail(w, 411, "Content-Length required")
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if offset != 0 && offset != f.received.Load() {
		writeJSON(w, 409, map[string]int64{"offset": f.received.Load()})
		return
	}
	if offset+r.ContentLength > f.size {
		fail(w, 400, "More data than declared")
		return
	}
	if !f.busy.CompareAndSwap(false, true) {
		fail(w, 409, "Upload already in progress")
		return
	}
	defer f.busy.Store(false)
	sess.lastActivity.Store(time.Now().UnixMilli())
	s.host.RecvStatus(sess.transferID, "transferring", "")
	if err := s.receive(r, sess, f, offset); err != nil {
		if sess.cancelled.Load() {
			s.cleanup(sess)
		} else if errors.Is(err, errChecksum) {
			s.host.RecvFinished(sess.transferID, "failed", fmt.Sprintf("%q was corrupted in transit (checksum mismatch).", f.name))
		} else {
			// Partial data is kept: a Ferry sender resumes from f.received for 10 minutes.
			s.log.Warn("receive interrupted", "file", f.name, "err", err)
			s.host.RecvStatus(sess.transferID, "interrupted", "Connection lost — waiting for the sender to resume…")
		}
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{})
}

var errChecksum = errors.New("checksum mismatch")

func (s *Server) receive(r *http.Request, sess *session, f *rfile, offset int64) error {
	if f.tmp == "" {
		dir := filepath.Join(append([]string{sess.dir}, f.dirs...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		f.tmp = filepath.Join(dir, f.name+"."+sess.id[:8]+".ferrypart")
	}
	flag := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flag |= os.O_TRUNC
	}
	out, err := os.OpenFile(f.tmp, flag, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	h := sha256.New()
	if offset > 0 { // rebuild the hash of what we already have
		in, err := os.Open(f.tmp)
		if err != nil {
			return err
		}
		n, err := io.CopyN(h, in, offset)
		in.Close()
		if err != nil || n != offset {
			return fmt.Errorf("partial data is missing")
		}
		if err := out.Truncate(offset); err != nil {
			return err
		}
		if _, err := out.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	f.received.Store(offset)
	buf := make([]byte, 256<<10)
	left := r.ContentLength
	lastReport := time.Now()
	for left > 0 {
		if sess.cancelled.Load() {
			return errors.New("cancelled")
		}
		n, rerr := r.Body.Read(buf[:min(int64(len(buf)), left)])
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			h.Write(buf[:n])
			left -= int64(n)
			f.received.Add(int64(n))
			sess.lastActivity.Store(time.Now().UnixMilli())
			if time.Since(lastReport) > 150*time.Millisecond {
				s.host.RecvProgress(sess.transferID, f.id, f.received.Load())
				lastReport = time.Now()
			}
		}
		if rerr != nil {
			if rerr == io.EOF && left == 0 {
				break
			}
			if rerr == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return rerr
		}
	}
	s.host.RecvProgress(sess.transferID, f.id, f.received.Load())
	if f.received.Load() < f.size {
		return nil // more parts follow (resumed upload)
	}
	if err := out.Close(); err != nil {
		return err
	}
	f.sha = hex.EncodeToString(h.Sum(nil))
	if f.expected != "" && f.expected != f.sha {
		os.Remove(f.tmp)
		f.tmp = ""
		f.received.Store(0)
		return errChecksum
	}
	final, err := uniquePath(filepath.Join(filepath.Dir(f.tmp), f.name))
	if err != nil {
		return err
	}
	if err := os.Rename(f.tmp, final); err != nil {
		return err
	}
	f.final, f.tmp = final, ""
	s.mu.Lock()
	f.done = true
	s.mu.Unlock()
	s.host.RecvFileSaved(sess.transferID, f.id, final)
	s.maybeFinish(sess)
	return nil
}

func (s *Server) maybeFinish(sess *session) {
	s.mu.Lock()
	all, verified := true, true
	for _, f := range sess.files {
		all = all && f.done
		verified = verified && f.verified
	}
	s.mu.Unlock()
	switch {
	case !all:
	case sess.ferry && !verified: // Ferry senders confirm each file's SHA-256; stock LocalSend can't
		s.host.RecvStatus(sess.transferID, "verifying", "Verifying integrity…")
	default:
		s.host.RecvFinished(sess.transferID, "completed", "")
	}
}

func (s *Server) offset(w http.ResponseWriter, r *http.Request) {
	_, f := s.authorize(r)
	if f == nil {
		fail(w, 403, "Invalid session or token")
		return
	}
	writeJSON(w, 200, map[string]any{"offset": f.received.Load(), "done": f.done})
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	sess, f := s.authorize(r)
	if f == nil {
		fail(w, 403, "Invalid session or token")
		return
	}
	if !f.done {
		fail(w, 409, "Not complete")
		return
	}
	if !strings.EqualFold(r.URL.Query().Get("sha256"), f.sha) {
		os.Remove(f.final)
		s.mu.Lock()
		f.done, f.final = false, ""
		s.mu.Unlock()
		f.received.Store(0)
		s.host.RecvFinished(sess.transferID, "failed", fmt.Sprintf("%q was corrupted in transit (checksum mismatch).", f.name))
		fail(w, 409, "Checksum mismatch")
		return
	}
	s.mu.Lock()
	f.verified = true
	s.mu.Unlock()
	s.maybeFinish(sess)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess != nil && sess.id == r.URL.Query().Get("sessionId") {
		sess.cancelled.Store(true)
		s.cleanup(sess)
		s.host.RecvFinished(sess.transferID, "cancelled", "The sender cancelled the transfer.")
	}
	writeJSON(w, 200, map[string]any{})
}

// CancelActive stops an incoming transfer from the UI.
func (s *Server) CancelActive(transferID string) {
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess != nil && sess.transferID == transferID {
		sess.cancelled.Store(true)
		s.cleanup(sess)
	}
}

// cleanup removes partial files; completed files stay.
func (s *Server) cleanup(sess *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range sess.files {
		if !f.done && f.tmp != "" && !f.busy.Load() {
			os.Remove(f.tmp)
			f.tmp = ""
		}
	}
	if s.sess == sess {
		s.sess = nil
	}
}

// ---------- names ----------

var reserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}

func init() {
	for i := 1; i <= 9; i++ {
		reserved["COM"+strconv.Itoa(i)], reserved["LPT"+strconv.Itoa(i)] = true, true
	}
}

// SafeName makes a peer-supplied name safe for Windows: no separators, control or reserved characters,
// reserved device names or trailing dots, and at most 200 bytes.
func SafeName(n string) string {
	n = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == 0:
			return '_'
		case unicode.IsControl(r):
			return ' '
		case strings.ContainsRune(`<>:"|?*`, r):
			return '_'
		}
		return r
	}, strings.ToValidUTF8(n, "_"))
	n = strings.TrimRight(strings.TrimSpace(n), ". ")
	if n == "" || n == "." || n == ".." {
		n = "file"
	}
	if base, _, _ := strings.Cut(n, "."); reserved[strings.ToUpper(base)] {
		n = "_" + n
	}
	if len(n) > 200 {
		ext := filepath.Ext(n)
		if len(ext) > 16 {
			ext = ""
		}
		stem := n[:len(n)-len(ext)]
		for len(stem)+len(ext) > 200 {
			_, size := lastRune(stem)
			stem = stem[:len(stem)-size]
		}
		n = stem + ext
	}
	return n
}

func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xC0 != 0x80 {
			return []rune(s[i:])[0], len(s) - i
		}
	}
	return 0, 1
}

// SafeRelativePath splits "a/b/c.txt" into safe folder names and a file name, dropping "..", "." and empty parts.
func SafeRelativePath(p string) (dirs []string, name string) {
	var parts []string
	for _, s := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if s = strings.TrimSpace(s); s != "" && s != "." && s != ".." {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return nil, "file"
	}
	for _, d := range parts[:len(parts)-1] {
		if len(dirs) < 8 {
			dirs = append(dirs, SafeName(d))
		}
	}
	return dirs, SafeName(parts[len(parts)-1])
}

// uniquePath returns p, or "name (n).ext" when p exists.
func uniquePath(p string) (string, error) {
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for i := 1; i < 10000; i++ {
		c := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(c); errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
	}
	return "", fmt.Errorf("too many files named %s", filepath.Base(p))
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// MimeOf guesses a file's MIME type from its name.
func MimeOf(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}
