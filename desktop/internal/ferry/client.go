// Package ferry is a client for a Ferry server's /api/v1: sign-in as a device, share links, sending to
// your other devices through the server, and receiving from this device's inbox.
package ferry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const APIVersion = 1

// UserAgent identifies the app to the server (checked against the server's minimum client version).
var UserAgent = "windows/dev api=1"

// Error is an error answered by the server, with its user-facing message.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// ErrCancelled is returned when the caller's cancel function reports true.
var ErrCancelled = errors.New("cancelled")

type Client struct {
	Base  string
	Token string
	http  *http.Client
}

var sharedHTTP = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 2 * time.Minute,
	MaxIdleConnsPerHost:   8,
	ForceAttemptHTTP2:     true,
}}

func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, http: sharedHTTP}
}

// NormalizeURL turns "files.example.com" into "https://files.example.com".
func NormalizeURL(in string) (string, bool) {
	u := strings.TrimRight(strings.TrimSpace(in), "/")
	low := strings.ToLower(u)
	switch {
	case strings.HasPrefix(low, "https://"):
		return "https://" + u[8:], true
	case strings.HasPrefix(low, "http://"):
		return "http://" + u[7:], true
	}
	return "https://" + u, false
}

func (c *Client) req(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u = c.Base + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Ferry-Client", UserAgent)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := c.req(ctx, method, path, rd)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return friendlyNet(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		return apiError(res.StatusCode, b)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return errors.New("the server's answer couldn't be read — is this a Ferry server?")
		}
	}
	return nil
}

func apiError(status int, body []byte) error {
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return &Error{status, e.Error.Code, e.Error.Message}
	}
	switch status {
	case 502, 503, 504:
		return &Error{status, "unavailable", "The server is temporarily unavailable. Please try again shortly."}
	case 426:
		return &Error{status, "client_outdated", "This app is too old for the server. Please update Ferry."}
	case 404:
		return &Error{status, "not_found", "Not found on the server. Check the address — it should be your Ferry server's main address."}
	}
	return &Error{status, fmt.Sprintf("http_%d", status), fmt.Sprintf("The server returned an unexpected error (%d).", status)}
}

// friendlyNet explains connection failures in plain words.
func friendlyNet(err error) error {
	var dns *net.DNSError
	var op *net.OpError
	s := err.Error()
	switch {
	case errors.Is(err, context.Canceled):
		return ErrCancelled
	case errors.As(err, &dns):
		return errors.New("server address not found — check the address and your internet connection")
	case strings.Contains(s, "x509") || strings.Contains(s, "certificate"):
		return errors.New("the server's HTTPS certificate isn't trusted by Windows (" + lastPart(s) + ")")
	case strings.Contains(s, "server gave HTTP response to HTTPS client"):
		return errors.New("this server doesn't use HTTPS — enter the address starting with http://")
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(s, "timeout"):
		return errors.New("the server isn't responding — check your connection")
	case errors.As(err, &op):
		return errors.New("can't connect to the server — it may be offline or blocked on this network")
	}
	return err
}

func lastPart(s string) string {
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

// ---------- account ----------

type ServerInfo struct {
	Name, SiteName, Version    string
	APIVersion                 int      `json:"apiVersion"`
	MinClientAPIVersion        int      `json:"minClientApiVersion"`
	Capabilities               []string `json:"capabilities"`
	AllowSignup, PublicSharing bool
	OIDC                       *struct{ Name string } `json:"oidc"`
}

// Info checks that base is a compatible Ferry server.
func (c *Client) Info(ctx context.Context) (*ServerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var i ServerInfo
	if err := c.Do(ctx, http.MethodGet, "/api/v1/info", nil, &i); err != nil {
		return nil, err
	}
	if i.Name != "Ferry" {
		return nil, errors.New("this address isn't a Ferry server")
	}
	if i.MinClientAPIVersion > APIVersion {
		return nil, fmt.Errorf("the server (Ferry %s) needs a newer app — please update Ferry for Windows", i.Version)
	}
	return &i, nil
}

type User struct {
	ID, Email, Name, Role string
	TOTPEnabled           bool `json:"totpEnabled"`
}

type LoginResult struct {
	Token  string `json:"token"`
	User   User   `json:"user"`
	Device struct {
		ID, Name string
	} `json:"device"`
}

// Login signs in as a device. A 401 with Code "totp_required" asks for the two-factor code.
func (c *Client) Login(ctx context.Context, email, password, code, deviceName, deviceID, version string) (*LoginResult, error) {
	var r LoginResult
	body := map[string]any{"email": email, "password": password, "code": code,
		"device": map[string]string{"name": deviceName, "platform": "windows", "appVersion": version, "deviceId": deviceID}}
	if err := c.Do(ctx, http.MethodPost, "/api/v1/auth/login", body, &r); err != nil {
		return nil, err
	}
	if r.Token == "" {
		return nil, errors.New("the server didn't return a session — is it a Ferry server?")
	}
	return &r, nil
}

func (c *Client) Logout(ctx context.Context) {
	c.Do(ctx, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, nil)
}

type Device struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Platform    string   `json:"platform"`
	Online      bool     `json:"online"`
	Current     bool     `json:"current"`
	LastSeen    int64    `json:"lastSeen"`
	LanAddrs    []string `json:"lanAddrs"`
	LanPort     int      `json:"lanPort"`
	Fingerprint string   `json:"fingerprint"`
}

func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var r struct{ Devices []Device }
	err := c.Do(ctx, http.MethodGet, "/api/v1/devices", nil, &r)
	return r.Devices, err
}

// Presence tells my other devices where this one can be reached on its LAN.
func (c *Client) Presence(ctx context.Context, addrs []string, port int, fingerprint string) error {
	return c.Do(ctx, http.MethodPut, "/api/v1/devices/current/presence",
		map[string]any{"addrs": addrs, "port": port, "protocol": "https", "fingerprint": fingerprint}, nil)
}

func (c *Client) ClearPresence(ctx context.Context) {
	c.Do(ctx, http.MethodDelete, "/api/v1/devices/current/presence", nil, nil)
}

// ---------- files, folders, links ----------

// EnsureFolder returns the ID of the folder named name under parent, creating it when missing.
func (c *Client) EnsureFolder(ctx context.Context, name, parent string) (string, error) {
	var f struct{ ID string }
	err := c.Do(ctx, http.MethodPost, "/api/v1/folders", map[string]string{"name": name, "parentId": parent}, &f)
	var e *Error
	if errors.As(err, &e) && e.Code == "name_taken" {
		var l struct {
			Folders []struct{ ID, Name string }
		}
		if err := c.Do(ctx, http.MethodGet, "/api/v1/files?folder="+url.QueryEscape(parent), nil, &l); err != nil {
			return "", err
		}
		for _, x := range l.Folders {
			if strings.EqualFold(x.Name, name) {
				return x.ID, nil
			}
		}
	}
	return f.ID, err
}

type ShareOpts struct {
	ExpiresIn    int64  `json:"expiresIn"` // seconds; 0 = never
	MaxDownloads int    `json:"maxDownloads"`
	Password     string `json:"password,omitempty"`
	Message      string `json:"message,omitempty"`
	Name         string `json:"name,omitempty"`
}

type Share struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	ShortURL string `json:"shortUrl"`
	Name     string `json:"name"`
}

func (s Share) Link() string {
	if s.ShortURL != "" {
		return s.ShortURL
	}
	return s.URL
}

func (c *Client) CreateShare(ctx context.Context, fileIDs, folderIDs []string, o ShareOpts) (*Share, error) {
	body := map[string]any{"kind": "download", "fileIds": fileIDs, "folderIds": folderIDs, "expiresIn": o.ExpiresIn, "maxDownloads": o.MaxDownloads}
	if o.Password != "" {
		body["password"] = o.Password
	}
	if o.Message != "" {
		body["message"] = o.Message
	}
	if o.Name != "" {
		body["name"] = o.Name
	}
	var s Share
	err := c.Do(ctx, http.MethodPost, "/api/v1/shares", body, &s)
	return &s, err
}

// ---------- transfers (server relay and inbox) ----------

type TransferFile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Transfer struct {
	ID               string         `json:"id"`
	Status           string         `json:"status"`
	Error            string         `json:"error"`
	SourceDeviceName string         `json:"sourceDeviceName"`
	Files            []TransferFile `json:"files"`
}

func (c *Client) CreateTransfer(ctx context.Context, targetDevice string, count int, total int64) (string, error) {
	var t Transfer
	err := c.Do(ctx, http.MethodPost, "/api/v1/transfers", map[string]any{"targetDeviceId": targetDevice, "fileCount": count, "totalBytes": total}, &t)
	return t.ID, err
}

func (c *Client) PatchTransfer(ctx context.Context, id, status string, bytesDone int64, errMsg string) error {
	body := map[string]any{"status": status}
	if bytesDone >= 0 {
		body["bytesDone"] = bytesDone
	}
	if errMsg != "" {
		body["error"] = errMsg
	}
	return c.Do(ctx, http.MethodPatch, "/api/v1/transfers/"+url.PathEscape(id), body, nil)
}

func (c *Client) GetTransfer(ctx context.Context, id string) (*Transfer, error) {
	var t Transfer
	err := c.Do(ctx, http.MethodGet, "/api/v1/transfers/"+url.PathEscape(id), nil, &t)
	return &t, err
}

// Inbox lists transfers sent to this device (from the web app or my other devices).
func (c *Client) Inbox(ctx context.Context) ([]Transfer, error) {
	var r struct{ Transfers []Transfer }
	err := c.Do(ctx, http.MethodGet, "/api/v1/inbox", nil, &r)
	return r.Transfers, err
}

// ---------- resumable upload (tus 1.0) ----------

// ResumeStore remembers upload URLs so an upload continues after the app restarts.
type ResumeStore interface {
	Get(key string) string
	Set(key, value string) // "" deletes
}

const chunk = 16 << 20

// Upload sends a file with tus, resuming where the server stopped. It returns the server file ID and
// verifies the server's SHA-256 against the local one.
func (c *Client) Upload(ctx context.Context, path, name string, meta map[string]string, key string, store ResumeStore, progress func(int64)) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("can't read %q: it may have been moved or deleted", name)
	}
	size := st.Size()
	for attempt := 0; ; attempt++ {
		id, err := c.uploadOnce(ctx, path, name, size, meta, key, store, progress)
		if err == nil {
			return id, nil
		}
		var e *Error
		if errors.Is(err, ErrCancelled) || ctx.Err() != nil {
			return "", ErrCancelled
		}
		if errors.As(err, &e) && e.Status >= 400 && e.Status < 500 && e.Status != 409 && e.Status != 423 && e.Status != 429 {
			store.Set(key, "")
			return "", err
		}
		if attempt >= 8 {
			return "", fmt.Errorf("connection lost — the upload can be resumed: %w", err)
		}
		if !sleepCtx(ctx, time.Duration(min(30, 1<<attempt))*time.Second) {
			return "", ErrCancelled
		}
	}
}

func (c *Client) uploadOnce(ctx context.Context, path, name string, size int64, meta map[string]string, key string, store ResumeStore, progress func(int64)) (string, error) {
	loc := store.Get(key)
	var offset int64
	if loc != "" {
		req, _ := c.req(ctx, http.MethodHead, loc, nil)
		req.Header.Set("Tus-Resumable", "1.0.0")
		res, err := c.http.Do(req)
		if err != nil {
			return "", friendlyNet(err)
		}
		res.Body.Close()
		switch {
		case res.StatusCode == 200:
			offset, _ = strconv.ParseInt(res.Header.Get("Upload-Offset"), 10, 64)
			if done := res.Header.Get("Ferry-File-Id"); done != "" && offset == size {
				store.Set(key, "")
				progress(size)
				return done, nil
			}
		case res.StatusCode >= 400 && res.StatusCode < 500:
			loc = ""
		default:
			return "", apiError(res.StatusCode, nil)
		}
	}
	if loc == "" {
		var parts []string
		meta = cloneWith(meta, "filename", name)
		for k, v := range meta {
			parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(v)))
		}
		req, _ := c.req(ctx, http.MethodPost, "/api/v1/uploads", nil)
		req.Header.Set("Tus-Resumable", "1.0.0")
		req.Header.Set("Upload-Length", strconv.FormatInt(size, 10))
		req.Header.Set("Upload-Metadata", strings.Join(parts, ","))
		res, err := c.http.Do(req)
		if err != nil {
			return "", friendlyNet(err)
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		if res.StatusCode != 201 {
			return "", apiError(res.StatusCode, b)
		}
		loc = res.Header.Get("Location")
		if loc == "" {
			return "", errors.New("the server didn't return an upload location")
		}
		if !strings.HasPrefix(loc, "http") {
			loc = c.Base + loc
		}
		if size == 0 {
			return res.Header.Get("Ferry-File-Id"), nil
		}
		store.Set(key, loc)
		offset = 0
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("can't read %q: it may have been moved or deleted", name)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyN(h, f, offset); err != nil {
		return "", fmt.Errorf("can't read %q: %w", name, err)
	}
	progress(offset)
	for offset < size {
		n := min(int64(chunk), size-offset)
		base := offset
		body := &countingReader{r: io.TeeReader(io.LimitReader(f, n), h), fn: func(done int64) { progress(base + done) }}
		req, _ := c.req(ctx, http.MethodPatch, loc, body)
		req.ContentLength = n
		req.Header.Set("Tus-Resumable", "1.0.0")
		req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
		req.Header.Set("Content-Type", "application/offset+octet-stream")
		res, err := c.http.Do(req)
		if err != nil {
			return "", friendlyNet(err)
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		if res.StatusCode != 204 {
			return "", apiError(res.StatusCode, b)
		}
		offset, _ = strconv.ParseInt(res.Header.Get("Upload-Offset"), 10, 64)
		if offset >= size {
			store.Set(key, "")
			local := hex.EncodeToString(h.Sum(nil))
			if s := res.Header.Get("Ferry-Sha256"); s != "" && s != local {
				return "", &Error{460, "checksum_mismatch", "The file was corrupted in transit. Please retry."}
			}
			return res.Header.Get("Ferry-File-Id"), nil
		}
		if offset != base+n { // the server kept less than we sent: rewind and rebuild the hash
			return "", errors.New("upload offset changed")
		}
	}
	return "", errors.New("upload ended early")
}

func cloneWith(m map[string]string, k, v string) map[string]string {
	out := map[string]string{k: v}
	for a, b := range m {
		out[a] = b
	}
	return out
}

type countingReader struct {
	r    io.Reader
	n    int64
	fn   func(int64)
	last time.Time
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if time.Since(c.last) > 150*time.Millisecond || err != nil {
		c.fn(c.n)
		c.last = time.Now()
	}
	return n, err
}

// ---------- download ----------

// Download saves a server file to dest (a partial file resumes with a Range request) and checks its SHA-256.
func (c *Client) Download(ctx context.Context, fileID string, dest string, size int64, sha string, progress func(int64)) error {
	for attempt := 0; ; attempt++ {
		err := c.downloadOnce(ctx, fileID, dest, size, sha, progress)
		if err == nil || errors.Is(err, ErrCancelled) || ctx.Err() != nil {
			if ctx.Err() != nil {
				return ErrCancelled
			}
			return err
		}
		var e *Error
		if (errors.As(err, &e) && e.Status >= 400 && e.Status < 500 && e.Status != 429) || attempt >= 8 {
			return err
		}
		if !sleepCtx(ctx, time.Duration(min(30, 1<<attempt))*time.Second) {
			return ErrCancelled
		}
	}
}

func (c *Client) downloadOnce(ctx context.Context, fileID, dest string, size int64, sha string, progress func(int64)) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	have, _ := f.Seek(0, io.SeekEnd)
	if have > size {
		f.Truncate(0)
		have = 0
	}
	h := sha256.New()
	f.Seek(0, io.SeekStart)
	if _, err := io.CopyN(h, f, have); err != nil {
		return err
	}
	if have < size {
		req, _ := c.req(ctx, http.MethodGet, "/api/v1/files/"+url.PathEscape(fileID)+"/content", nil)
		if have > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
		}
		res, err := c.http.Do(req)
		if err != nil {
			return friendlyNet(err)
		}
		defer res.Body.Close()
		switch res.StatusCode {
		case 206:
		case 200:
			if have > 0 { // server ignored the range: start over
				f.Truncate(0)
				f.Seek(0, io.SeekStart)
				h.Reset()
				have = 0
			}
		default:
			b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			return apiError(res.StatusCode, b)
		}
		base := have
		if _, err := io.Copy(io.MultiWriter(f, h), &countingReader{r: res.Body, fn: func(n int64) { progress(base + n) }}); err != nil {
			return friendlyNet(err)
		}
	}
	if sha != "" && hex.EncodeToString(h.Sum(nil)) != sha {
		f.Truncate(0)
		return &Error{460, "checksum_mismatch", "The file arrived corrupted (checksum mismatch). Please retry."}
	}
	progress(size)
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
