// Package config loads Ferry's configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr        string
	PublicURL   string // optional; derived from the request when empty
	SiteName    string
	DataDir     string
	DBDriver    string // sqlite | postgres
	DBDSN       string
	StoragePath string

	TLSCert, TLSKey string
	CookieSecure    string // auto | true | false

	AllowSignup   bool
	PublicSharing bool
	WebApp        bool // false: API-only server (apps, share pages and upload links keep working)
	AdminEmail    string
	AdminPassword string

	MaxUploadBytes     int64 // 0 = unlimited
	DefaultUserQuota   int64 // 0 = unlimited
	GlobalQuota        int64 // 0 = unlimited
	MinFreeSpace       int64 // uploads are refused when the disk would drop below this; 0 = off
	MaxShareBytes      int64
	MaxShareFiles      int
	SessionTTL         time.Duration
	UploadExpiry       time.Duration
	DownloadWindow     time.Duration
	CleanupInterval    time.Duration
	ShareRetention     time.Duration // how long expired/revoked shares stay visible before deletion
	AuditRetention     time.Duration
	RateLimitPerMinute int

	TrustedProxies []*net.IPNet
	CORSOrigins    []string

	SMTPHost, SMTPUser, SMTPPass, SMTPFrom string
	SMTPPort                               int
	SMTPSecurity                           string // auto | starttls | tls | none
	SMTPSkipVerify                         bool   // accept self-signed/mismatched certificates

	OIDC OIDCConfig

	// Optional Shortr (or compatible) URL shortener: POST {url}/api/v1/links with a Bearer API key.
	ShortenerURL, ShortenerToken string

	ScanCommand  string
	MetricsToken string
	LogLevel     string
	LogFormat    string // json | text
	LogFile      string // optional; logs are also appended here (rotated at 10 MB)

	FromEnv map[string]bool // FERRY_* variables that were set; they win over values saved in the admin UI
}

// OIDCConfig configures single sign-on with an OpenID Connect provider (Keycloak, Authentik, Authelia,
// Zitadel, Google, Microsoft Entra ID, …). Disabled when Issuer is empty.
type OIDCConfig struct {
	Issuer, ClientID, ClientSecret string
	Name                           string   // button label
	Scopes                         []string // always includes openid
	AutoCreate                     bool     // create an account on first sign-in
	LinkByEmail                    bool     // connect to an existing account with the same (verified) email
	TrustUnverifiedEmail           bool     // also link/create when the provider doesn't mark the email verified
	AllowedDomains                 []string // email domains allowed to sign in; empty = any
	AdminGroup                     string   // members of this group/role become admins (synced at each sign-in)
	GroupsClaim                    string   // claim holding groups/roles; dotted paths like realm_access.roles work
	SkipVerify                     bool     // accept a self-signed TLS certificate from the provider
}

func (o OIDCConfig) Enabled() bool { return o.Issuer != "" }

func Load() (*Config, error) {
	envSet = map[string]bool{}
	defer func() { envSet = nil }()
	c := &Config{
		Addr:           env("FERRY_ADDR", ":8080"),
		PublicURL:      strings.TrimRight(env("FERRY_PUBLIC_URL", ""), "/"),
		SiteName:       env("FERRY_SITE_NAME", "Ferry"),
		DataDir:        env("FERRY_DATA_DIR", "./data"),
		DBDriver:       strings.ToLower(env("FERRY_DB_DRIVER", "sqlite")),
		TLSCert:        env("FERRY_TLS_CERT", ""),
		TLSKey:         env("FERRY_TLS_KEY", ""),
		CookieSecure:   strings.ToLower(env("FERRY_COOKIE_SECURE", "auto")),
		AdminEmail:     env("FERRY_ADMIN_EMAIL", ""),
		AdminPassword:  env("FERRY_ADMIN_PASSWORD", ""),
		SMTPHost:       env("FERRY_SMTP_HOST", ""),
		SMTPUser:       env("FERRY_SMTP_USER", ""),
		SMTPPass:       env("FERRY_SMTP_PASSWORD", ""),
		SMTPFrom:       env("FERRY_SMTP_FROM", ""),
		SMTPSecurity:   strings.ToLower(env("FERRY_SMTP_SECURITY", "auto")),
		ShortenerURL:   strings.TrimRight(env("FERRY_SHORTENER_URL", ""), "/"),
		ShortenerToken: env("FERRY_SHORTENER_TOKEN", ""),
		ScanCommand:    env("FERRY_SCAN_COMMAND", ""),
		MetricsToken:   env("FERRY_METRICS_TOKEN", ""),
		LogLevel:       strings.ToLower(env("FERRY_LOG_LEVEL", "info")),
		LogFormat:      strings.ToLower(env("FERRY_LOG_FORMAT", "text")),
		LogFile:        env("FERRY_LOG_FILE", ""),
	}
	var errs []string
	fail := func(err error) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	var err error
	c.AllowSignup, err = boolEnv("FERRY_ALLOW_SIGNUP", false)
	fail(err)
	c.PublicSharing, err = boolEnv("FERRY_PUBLIC_SHARING", true)
	fail(err)
	c.WebApp, err = boolEnv("FERRY_WEB_APP", true)
	fail(err)
	c.MaxUploadBytes, err = sizeEnv("FERRY_MAX_UPLOAD_SIZE", 0)
	fail(err)
	c.DefaultUserQuota, err = sizeEnv("FERRY_DEFAULT_USER_QUOTA", 0)
	fail(err)
	c.GlobalQuota, err = sizeEnv("FERRY_GLOBAL_QUOTA", 0)
	fail(err)
	c.MinFreeSpace, err = sizeEnv("FERRY_MIN_FREE_SPACE", 1<<30)
	fail(err)
	c.MaxShareBytes, err = sizeEnv("FERRY_MAX_SHARE_SIZE", 0)
	fail(err)
	c.MaxShareFiles, err = intEnv("FERRY_MAX_SHARE_FILES", 0)
	fail(err)
	c.SMTPPort, err = intEnv("FERRY_SMTP_PORT", 587)
	fail(err)
	c.SMTPSkipVerify, err = boolEnv("FERRY_SMTP_SKIP_VERIFY", false)
	fail(err)
	c.OIDC = OIDCConfig{
		Issuer:       env("FERRY_OIDC_ISSUER", ""), // exact: must equal the provider's issuer (Authentik's ends in /)
		ClientID:     env("FERRY_OIDC_CLIENT_ID", ""),
		ClientSecret: env("FERRY_OIDC_CLIENT_SECRET", ""),
		Name:         env("FERRY_OIDC_NAME", "Single sign-on"),
		Scopes:       ParseScopes(env("FERRY_OIDC_SCOPES", "openid profile email")),
		AdminGroup:   strings.TrimPrefix(env("FERRY_OIDC_ADMIN_GROUP", ""), "/"),
		GroupsClaim:  env("FERRY_OIDC_GROUPS_CLAIM", "groups"),
	}
	for _, d := range splitList(env("FERRY_OIDC_ALLOWED_DOMAINS", "")) {
		c.OIDC.AllowedDomains = append(c.OIDC.AllowedDomains, strings.ToLower(strings.TrimPrefix(d, "@")))
	}
	c.OIDC.AutoCreate, err = boolEnv("FERRY_OIDC_AUTO_CREATE", false)
	fail(err)
	c.OIDC.LinkByEmail, err = boolEnv("FERRY_OIDC_LINK_BY_EMAIL", true)
	fail(err)
	c.OIDC.TrustUnverifiedEmail, err = boolEnv("FERRY_OIDC_TRUST_UNVERIFIED_EMAIL", false)
	fail(err)
	c.OIDC.SkipVerify, err = boolEnv("FERRY_OIDC_SKIP_VERIFY", false)
	fail(err)
	c.RateLimitPerMinute, err = intEnv("FERRY_RATE_LIMIT", 600)
	fail(err)
	c.SessionTTL, err = durEnv("FERRY_SESSION_TTL", 30*24*time.Hour)
	fail(err)
	c.UploadExpiry, err = durEnv("FERRY_UPLOAD_EXPIRY", 24*time.Hour)
	fail(err)
	c.DownloadWindow, err = durEnv("FERRY_DOWNLOAD_WINDOW", 6*time.Hour)
	fail(err)
	c.CleanupInterval, err = durEnv("FERRY_CLEANUP_INTERVAL", time.Hour)
	fail(err)
	c.ShareRetention, err = durEnv("FERRY_EXPIRED_SHARE_RETENTION", 30*24*time.Hour)
	fail(err)
	c.AuditRetention, err = durEnv("FERRY_AUDIT_RETENTION", 90*24*time.Hour)
	fail(err)

	for _, s := range splitList(env("FERRY_TRUSTED_PROXIES", "")) {
		if !strings.Contains(s, "/") {
			if strings.Contains(s, ":") {
				s += "/128"
			} else {
				s += "/32"
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			errs = append(errs, "FERRY_TRUSTED_PROXIES: invalid entry "+s)
			continue
		}
		c.TrustedProxies = append(c.TrustedProxies, n)
	}
	c.CORSOrigins = splitList(env("FERRY_CORS_ORIGINS", ""))

	switch c.DBDriver {
	case "sqlite":
		c.DBDSN = env("FERRY_DB_DSN", filepath.Join(c.DataDir, "ferry.db"))
	case "postgres", "postgresql":
		c.DBDriver = "postgres"
		c.DBDSN = env("FERRY_DB_DSN", "")
		if c.DBDSN == "" {
			errs = append(errs, "FERRY_DB_DSN is required when FERRY_DB_DRIVER=postgres (e.g. postgres://user:pass@host:5432/ferry)")
		}
	default:
		errs = append(errs, "FERRY_DB_DRIVER must be sqlite or postgres")
	}
	c.StoragePath = env("FERRY_STORAGE_PATH", filepath.Join(c.DataDir, "storage"))
	if (c.TLSCert == "") != (c.TLSKey == "") {
		errs = append(errs, "FERRY_TLS_CERT and FERRY_TLS_KEY must be set together")
	}
	if c.AdminEmail != "" && len(c.AdminPassword) < 8 {
		errs = append(errs, "FERRY_ADMIN_PASSWORD must be at least 8 characters when FERRY_ADMIN_EMAIL is set")
	}
	c.FromEnv = envSet
	if err := c.Validate(); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  %s", strings.Join(errs, "\n  "))
	}
	return c, nil
}

// envSet records which settings came from the environment during Load; those are locked in the admin UI.
var envSet map[string]bool

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
		if envSet != nil {
			envSet[k] = true
		}
		return strings.TrimSpace(v)
	}
	return def
}

// Validate checks settings that depend on each other. It runs at startup and whenever an
// administrator saves settings.
func (c *Config) Validate() error {
	var errs []string
	switch c.SMTPSecurity {
	case "auto", "starttls", "tls", "none":
	default:
		errs = append(errs, "email security must be auto, starttls, tls or none")
	}
	if c.OIDC.Enabled() {
		if !strings.HasPrefix(c.OIDC.Issuer, "https://") && !strings.HasPrefix(c.OIDC.Issuer, "http://") {
			errs = append(errs, "the SSO issuer must be a URL like https://keycloak.example.com/realms/myrealm")
		}
		if c.OIDC.ClientID == "" {
			errs = append(errs, "SSO needs a client ID")
		}
	}
	if (c.ShortenerURL == "") != (c.ShortenerToken == "") {
		errs = append(errs, "the URL shortener needs both its address and an API key")
	}
	if c.PublicURL != "" && !strings.HasPrefix(c.PublicURL, "https://") && !strings.HasPrefix(c.PublicURL, "http://") {
		errs = append(errs, "the public address must start with https:// or http://")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolEnv(k string, def bool) (bool, error) {
	v := env(k, "")
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def, fmt.Errorf("%s: expected true/false, got %q", k, v)
	}
	return b, nil
}

func intEnv(k string, def int) (int, error) {
	v := env(k, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def, fmt.Errorf("%s: expected a non-negative number, got %q", k, v)
	}
	return n, nil
}

func durEnv(k string, def time.Duration) (time.Duration, error) {
	v := env(k, "")
	if v == "" {
		return def, nil
	}
	d, err := ParseDuration(v)
	if err != nil {
		return def, fmt.Errorf("%s: %v", k, err)
	}
	return d, nil
}

func sizeEnv(k string, def int64) (int64, error) {
	v := env(k, "")
	if v == "" {
		return def, nil
	}
	n, err := ParseSize(v)
	if err != nil {
		return def, fmt.Errorf("%s: %v", k, err)
	}
	return n, nil
}

// ParseSize parses "500MB", "10GiB", "1024" (bytes). Decimal and binary units are both 1024-based,
// which is what people mean when configuring limits.
func ParseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	units := []struct {
		suffix string
		mult   int64
	}{{"TIB", 1 << 40}, {"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10},
		{"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}}
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			mult = u.mult
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("expected a size like 500MB or 10GB")
	}
	return int64(f * float64(mult)), nil
}
