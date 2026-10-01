package lan

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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrIdentityChanged means a peer presented a different certificate than the one pinned for it.
var ErrIdentityChanged = errors.New("the device's identity changed — its certificate doesn't match. For your safety the connection was refused")

// clientCert is this device's identity, presented to peers that ask for a client certificate.
var clientCert atomic.Pointer[tls.Certificate]

// client returns an HTTP client for one peer. Peers use self-signed certificates, so trust is pinning:
// the certificate seen on first contact (or the fingerprint in a scanned QR code) must match every later
// connection. seen receives the fingerprint of the certificate actually presented.
//
// Clients that don't need to learn the certificate are shared per pin, so parallel uploads and the calls
// around them reuse TLS connections instead of handshaking every time.
func client(pin string, timeout time.Duration, seen *string) *http.Client {
	if seen != nil {
		return newClient(pin, timeout, seen)
	}
	key := clientKey{pin, timeout}
	pool.Lock()
	defer pool.Unlock()
	if c, ok := pool.m[key]; ok {
		return c
	}
	c := newClient(pin, timeout, nil)
	pool.m[key] = c
	return c
}

type clientKey struct {
	pin     string
	timeout time.Duration
}

var pool = struct {
	sync.Mutex
	m map[clientKey]*http.Client
}{m: map[clientKey]*http.Client{}}

func newClient(pin string, timeout time.Duration, seen *string) *http.Client {
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       3 * time.Second, // shorter than any peer's keep-alive, so a reused connection is never stale
		WriteBufferSize:       256 << 10,       // fewer syscalls per upload than the 4 KiB default
		ReadBufferSize:        64 << 10,
		TLSClientConfig: &tls.Config{
			ClientSessionCache: tls.NewLRUClientSessionCache(16), // resumed handshakes are cheaper on a fresh connection
			// Current LocalSend requires a client certificate (mTLS) and uses it as the sender's identity.
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				if c := clientCert.Load(); c != nil {
					return c, nil
				}
				return &tls.Certificate{}, nil
			},
			InsecureSkipVerify: true, //nolint:gosec // identity is the pinned fingerprint below, not a CA chain or host name
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("no certificate")
				}
				fp := FingerprintOf(raw[0])
				if seen != nil {
					*seen = fp
				}
				if pin != "" && !strings.EqualFold(fp, pin) {
					return ErrIdentityChanged
				}
				return nil
			},
		},
	}
	return &http.Client{Transport: tr}
}

func do(ctx context.Context, c *http.Client, method, u string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return res.StatusCode, peerErr(res.StatusCode, b)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return res.StatusCode, fmt.Errorf("unexpected answer from the device: %w", err)
		}
	}
	return res.StatusCode, nil
}

func peerErr(status int, body []byte) error {
	var m struct {
		Message string `json:"message"`
	}
	json.Unmarshal(body, &m)
	return &PeerError{Status: status, Message: clip(m.Message, 200)}
}

// FetchInfo gets a peer's identity. With https nil it tries HTTPS first, then HTTP (LocalSend with
// encryption off). pin is the expected certificate hash; empty means trust on first use.
func FetchInfo(ctx context.Context, ip string, port int, pin, source string, https *bool, timeout time.Duration) (Peer, error) {
	order := []bool{true, false}
	if https != nil {
		order = []bool{*https}
	}
	var last error
	for _, secure := range order {
		scheme := "http"
		if secure {
			scheme = "https"
		}
		var seen string
		var info Info
		cctx, cancel := context.WithTimeout(ctx, timeout)
		_, err := do(cctx, client(pin, timeout, &seen), http.MethodGet, scheme+"://"+net.JoinHostPort(ip, strconv.Itoa(port))+API+"/info", nil, &info)
		cancel()
		if err == nil {
			p := peerFrom(info, ip, source, port, secure)
			p.Port, p.HTTPS, p.CertPin = port, secure, seen
			return p, nil
		}
		last = err
		if errors.Is(err, ErrIdentityChanged) {
			break
		}
	}
	return Peer{}, last
}

// Register answers a peer's announcement over HTTP (preferred by LocalSend over a multicast reply).
func Register(ctx context.Context, p Peer, self Info) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := do(ctx, client(p.CertPin, 5*time.Second, nil), http.MethodPost, p.BaseURL()+API+"/register", self, nil)
	return err
}

// Prepared is an accepted prepare-upload: one token per file the receiver wants.
type Prepared struct {
	SessionID string
	Tokens    map[string]string
}

// Prepare asks the receiver to accept files and blocks while its user decides (up to ~2 minutes).
// It returns nil when the receiver needs nothing (204). 401 → PIN required, 403 → declined, 409 → busy.
func Prepare(ctx context.Context, p Peer, self Info, files map[string]FileMeta, pin string) (*Prepared, error) {
	u := p.BaseURL() + API + "/prepare-upload"
	if pin != "" {
		u += "?pin=" + url.QueryEscape(pin)
	}
	var out prepareResp
	status, err := do(ctx, client(p.CertPin, 130*time.Second, nil), http.MethodPost, u, prepareReq{Info: self, Files: files}, &out)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	return &Prepared{SessionID: out.SessionID, Tokens: out.Files}, nil
}

func fileQuery(session, fileID, token string) string {
	return "?sessionId=" + url.QueryEscape(session) + "&fileId=" + url.QueryEscape(fileID) + "&token=" + url.QueryEscape(token)
}

// Upload streams length bytes of one file starting at offset.
func Upload(ctx context.Context, p Peer, session, fileID, token string, offset int64, body io.Reader, length int64) error {
	u := p.BaseURL() + API + "/upload" + fileQuery(session, fileID, token)
	if offset > 0 {
		u += "&offset=" + strconv.FormatInt(offset, 10)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return err
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := client(p.CertPin, 10*time.Minute, nil).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 300 {
		return peerErr(res.StatusCode, b)
	}
	return nil
}

// Offset (Ferry extension) is how many bytes of a file the receiver already has.
func Offset(ctx context.Context, p Peer, session, fileID, token string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out struct {
		Offset int64 `json:"offset"`
	}
	_, err := do(ctx, client(p.CertPin, 10*time.Second, nil), http.MethodGet, p.BaseURL()+API+"/ferry/offset"+fileQuery(session, fileID, token), nil, &out)
	return out.Offset, err
}

// Verify (Ferry extension) has the receiver compare our SHA-256 with what it stored.
func Verify(ctx context.Context, p Peer, session, fileID, token, sha string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := do(ctx, client(p.CertPin, 30*time.Second, nil), http.MethodPost, p.BaseURL()+API+"/ferry/verify"+fileQuery(session, fileID, token)+"&sha256="+sha, nil, nil)
	return err
}

// Cancel tells the receiver the sender gave up.
func Cancel(p Peer, session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	do(ctx, client(p.CertPin, 5*time.Second, nil), http.MethodPost, p.BaseURL()+API+"/cancel?sessionId="+url.QueryEscape(session), nil, nil)
}
