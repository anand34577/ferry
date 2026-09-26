package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return b
}

// newID returns a 26-char lowercase random identifier (128 bits).
func newID() string { return strings.ToLower(b32.EncodeToString(randBytes(16))) }

// newToken returns a URL-safe secret of n random bytes.
func newToken(n int) string { return strings.ToLower(b32.EncodeToString(randBytes(n))) }

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func nowMs() int64 { return time.Now().UnixMilli() }

type apiErr struct {
	Status  int
	Code    string
	Message string
}

func (e *apiErr) Error() string { return e.Message }

func errf(status int, code, msg string) *apiErr { return &apiErr{status, code, msg} }

var (
	errNotFound     = errf(404, "not_found", "We couldn't find that item. It may have been deleted.")
	errUnauthorized = errf(401, "unauthorized", "Please sign in to continue.")
	errForbidden    = errf(403, "forbidden", "You don't have permission to do that.")
	errBadJSON      = errf(400, "bad_request", "The request could not be read.")
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeErr renders any error as the standard JSON error envelope. Unknown errors become a
// generic message; the detail goes to the log, never to the client.
func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiErr
	if !errors.As(err, &ae) {
		s.log.Error("request failed", "route", r.Pattern, "err", err)
		ae = errf(500, "internal", "Something went wrong on the server. Your files are safe — please try again.")
	}
	writeJSON(w, ae.Status, map[string]any{"error": map[string]string{"code": ae.Code, "message": ae.Message}})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errBadJSON
	}
	return nil
}

// clientIP returns the caller's IP, honouring X-Forwarded-For only from trusted proxies.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !s.trusted(ip) {
		if r.Header.Get("X-Forwarded-For") != "" {
			proxyWarning.Do(func() {
				s.log.Warn("requests arrive through a proxy that isn't trusted, so every visitor appears to come from the proxy's address "+
					"(rate limits, sign-in lockouts and logs then apply to everyone together); set FERRY_TRUSTED_PROXIES", "proxy", host)
			})
		}
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		p := net.ParseIP(strings.TrimSpace(parts[i]))
		if p == nil {
			continue
		}
		if !s.trusted(p) {
			return p.String()
		}
	}
	return host
}

var proxyWarning sync.Once

func (s *Server) trusted(ip net.IP) bool {
	for _, n := range s.conf().TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// isHTTPS reports whether the original client connection used TLS.
func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && s.trusted(ip) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

func (s *Server) secureCookie(r *http.Request) bool {
	switch s.conf().CookieSecure {
	case "true":
		return true
	case "false":
		return false
	}
	return s.isHTTPS(r) || strings.HasPrefix(s.conf().PublicURL, "https://")
}

// baseURL is the externally visible origin used to build share links.
func (s *Server) baseURL(r *http.Request) string {
	if s.conf().PublicURL != "" {
		return s.conf().PublicURL
	}
	scheme := "http"
	if s.isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		if rh, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			if ip := net.ParseIP(rh); ip != nil && s.trusted(ip) {
				host = h
			}
		}
	}
	return scheme + "://" + host
}

// windowsReserved names cannot be created on Windows; we prefix them so downloads work everywhere.
var windowsReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// cleanName makes a client-supplied file or folder name safe to store and display.
// Unicode, emoji and spaces are kept; separators, control characters and reserved names are not.
func cleanName(name string) string {
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, "_")
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == 0:
			return '_'
		case unicode.IsControl(r):
			return -1
		case strings.ContainsRune(`<>:"|?*`, r):
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	name = strings.TrimRight(name, ". ")
	if name == "" || name == "." || name == ".." {
		name = "unnamed"
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if windowsReserved[base] {
		name = "_" + name
	}
	// Limit to 240 bytes (filesystems allow 255) without splitting a UTF-8 sequence, keeping the extension.
	const max = 240
	if len(name) > max {
		ext := path.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		stem := name[:len(name)-len(ext)]
		cut := max - len(ext)
		for cut > 0 && !utf8.RuneStart(stem[cut]) {
			cut--
		}
		name = stem[:cut] + ext
	}
	return name
}

// numberedName returns "photo (n).jpg" for "photo.jpg".
func numberedName(name string, n int) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if ext == name { // ".bashrc"
		stem, ext = name, ""
	}
	return stem + " (" + strconv.Itoa(n) + ")" + ext
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(s)) + "%"
}

func humanSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	f, i := float64(n), -1
	for f >= 1024 && i < 4 {
		f /= 1024
		i++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") + " " + [...]string{"KB", "MB", "GB", "TB", "PB"}[i]
}
