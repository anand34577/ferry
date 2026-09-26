package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"ferry/internal/config"
)

// fakeIdP is a minimal OpenID provider: discovery, JWKS, authorize (auto-approves), token with PKCE check.
type fakeIdP struct {
	*httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	claims map[string]any            // claims for the next sign-in
	codes  map[string]map[string]any // code → claims incl. nonce and PKCE challenge
}

func newFakeIdP(t *testing.T) *fakeIdP {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &fakeIdP{key: k, codes: map[string]map[string]any{}}
	mux := http.NewServeMux()
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token",
			"jwks_uri": p.URL + "/jwks", "userinfo_endpoint": p.URL + "/userinfo", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &k.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p.mu.Lock()
		c := map[string]any{}
		for k, v := range p.claims {
			c[k] = v
		}
		c["nonce"], c["_challenge"] = q.Get("nonce"), q.Get("code_challenge")
		code := newToken(8)
		p.codes[code] = c
		p.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		p.mu.Lock()
		c := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if c == nil || base64.RawURLEncoding.EncodeToString(sum[:]) != c["_challenge"] {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		delete(c, "_challenge")
		c["iss"], c["aud"], c["exp"], c["iat"] = p.URL, "ferry", time.Now().Add(time.Hour).Unix(), time.Now().Unix()
		sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: &jose.JSONWebKey{Key: k, KeyID: "k1"}}, (&jose.SignerOptions{}).WithType("JWT"))
		b, _ := json.Marshal(c)
		obj, _ := sig.Sign(b)
		tok, _ := obj.CompactSerialize()
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": tok})
	})
	return p
}

// login runs the whole browser redirect dance and returns where Ferry finally sends the browser.
func (p *fakeIdP) login(t *testing.T, c *client, claims map[string]any, query string) string {
	t.Helper()
	p.mu.Lock()
	p.claims = claims
	p.mu.Unlock()
	resp, _ := c.do("GET", "/api/v1/auth/oidc/start?"+query, nil, nil)
	if resp.StatusCode != 302 {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r2, err := noFollow.Get(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	cb, _ := url.Parse(r2.Header.Get("Location"))
	resp, _ = c.do("GET", cb.RequestURI(), nil, nil)
	if resp.StatusCode != 302 {
		t.Fatalf("callback: %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func TestOIDC(t *testing.T) {
	idp := newFakeIdP(t)
	s, ts := newTestServer(t, func(c *config.Config) {
		c.OIDC = config.OIDCConfig{Issuer: idp.URL, ClientID: "ferry", ClientSecret: "sec", Name: "Keycloak", Scopes: []string{"openid", "email"},
			LinkByEmail: true, AdminGroup: "ferry-admins", GroupsClaim: "realm_access.roles"}
	})
	adm := setupAdmin(t, ts)
	adm.json("POST", "/api/v1/admin/users", map[string]string{"email": "carol@example.com", "password": "carol-password"}, 201)

	// Verified matching email → connected to the existing account and signed in; next is honoured.
	carol := newClient(t, ts.URL)
	if loc := idp.login(t, carol, map[string]any{"sub": "c-1", "email": "Carol@example.com", "email_verified": true}, "next=/links"); loc != "/links" {
		t.Fatalf("carol redirected to %q", loc)
	}
	if me := carol.json("GET", "/api/v1/me", nil, 200); me["user"].(map[string]any)["email"] != "carol@example.com" {
		t.Fatalf("carol not signed in: %v", me)
	}

	// Unverified email is not trusted: the person must sign in to their account and confirm.
	dave := newClient(t, ts.URL)
	adm.json("POST", "/api/v1/admin/users", map[string]string{"email": "dave@example.com", "password": "dave-password"}, 201)
	loc := idp.login(t, dave, map[string]any{"sub": "d-1", "email": "dave@corp.example", "email_verified": false}, "")
	if !strings.HasPrefix(loc, "/login?sso=connect") {
		t.Fatalf("dave: %q", loc)
	}
	if p := dave.json("GET", "/api/v1/auth/oidc/pending", nil, 200)["pending"].(map[string]any); p["email"] != "dave@corp.example" {
		t.Fatalf("pending: %v", p)
	}
	dave.json("POST", "/api/v1/auth/login", map[string]string{"email": "dave@example.com", "password": "dave-password"}, 200)
	dave.json("POST", "/api/v1/auth/oidc/link", nil, 200)
	dave.json("POST", "/api/v1/auth/logout", nil, 200)
	if loc := idp.login(t, dave, map[string]any{"sub": "d-1", "email": "dave@corp.example"}, ""); loc != "/" {
		t.Fatalf("dave second sso login: %q", loc)
	}
	if ids := dave.json("GET", "/api/v1/me/identities", nil, 200)["identities"].([]any); len(ids) != 1 {
		t.Fatalf("identities: %v", ids)
	}

	// Unknown person, auto-create off → asked to connect, nobody signed in.
	eve := newClient(t, ts.URL)
	if loc := idp.login(t, eve, map[string]any{"sub": "e-1", "email": "eve@example.com", "email_verified": true}, ""); !strings.HasPrefix(loc, "/login?sso=connect") {
		t.Fatalf("eve: %q", loc)
	}
	eve.json("GET", "/api/v1/me", nil, 401)

	// Auto-create on → account created without a password; admin role comes from the group claim.
	setConf(s, func(c *config.Config) { c.OIDC.AutoCreate = true })
	frank := newClient(t, ts.URL)
	idp.login(t, frank, map[string]any{"sub": "f-1", "email": "frank@example.com", "email_verified": true, "name": "Frank F",
		"realm_access": map[string]any{"roles": []string{"ferry-admins"}}}, "")
	me := frank.json("GET", "/api/v1/me", nil, 200)
	if u := me["user"].(map[string]any); u["name"] != "Frank F" || u["role"] != "admin" || me["hasPassword"] != false {
		t.Fatalf("frank: %v", me)
	}
	// The only sign-in method can't be removed; setting a password (no current one needed) unlocks it.
	id := frank.json("GET", "/api/v1/me/identities", nil, 200)["identities"].([]any)[0].(map[string]any)["id"].(string)
	frank.json("DELETE", "/api/v1/me/identities/"+id, nil, 400)
	frank.json("POST", "/api/v1/me/password", map[string]string{"new": "frank-password"}, 200)
	frank.json("DELETE", "/api/v1/me/identities/"+id, nil, 200)

	// Tampered state → back to login with an error, no session.
	mallory := newClient(t, ts.URL)
	resp, _ := mallory.do("GET", "/api/v1/auth/oidc/callback?code=x&state=forged", nil, nil)
	if !strings.Contains(resp.Header.Get("Location"), "sso_error=") {
		t.Fatalf("forged callback: %v", resp.Header.Get("Location"))
	}
	// A trailing slash typo in the issuer is tolerated.
	s.oidc.reset()
	setConf(s, func(c *config.Config) { c.OIDC.Issuer = idp.URL + "/" })
	if _, _, _, err := s.oidcProvider(); err != nil {
		t.Fatalf("issuer with trailing slash: %v", err)
	}
	if safeNext("//evil.example") != "/" || safeNext("https://evil.example") != "/" || safeNext("/files?x=1") != "/files?x=1" {
		t.Fatal("safeNext")
	}
}

// setConf swaps in a changed copy of the settings, the way Admin → Settings does.
func setConf(s *Server, f func(*config.Config)) {
	c := *s.conf()
	f(&c)
	s.cfgp.Store(&c)
}
