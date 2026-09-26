// Package config loads Ferry's configuration from environment variables.
package config

import (
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

	ScanCommand  string
	MetricsToken string
	LogLevel     string
	LogFormat    string // json | text
	LogFile      string // optional; logs are also appended here (rotated at 10 MB)
}

func Load() (*Config, error) {
	c := &Config{
		Addr:          env("FERRY_ADDR", ":8080"),
		PublicURL:     strings.TrimRight(env("FERRY_PUBLIC_URL", ""), "/"),
		SiteName:      env("FERRY_SITE_NAME", "Ferry"),
		DataDir:       env("FERRY_DATA_DIR", "./data"),
		DBDriver:      strings.ToLower(env("FERRY_DB_DRIVER", "sqlite")),
		TLSCert:       env("FERRY_TLS_CERT", ""),
		TLSKey:        env("FERRY_TLS_KEY", ""),
		CookieSecure:  strings.ToLower(env("FERRY_COOKIE_SECURE", "auto")),
		AdminEmail:    env("FERRY_ADMIN_EMAIL", ""),
		AdminPassword: env("FERRY_ADMIN_PASSWORD", ""),
		SMTPHost:      env("FERRY_SMTP_HOST", ""),
		SMTPUser:      env("FERRY_SMTP_USER", ""),
		SMTPPass:      env("FERRY_SMTP_PASSWORD", ""),
		SMTPFrom:      env("FERRY_SMTP_FROM", ""),
		ScanCommand:   env("FERRY_SCAN_COMMAND", ""),
		MetricsToken:  env("FERRY_METRICS_TOKEN", ""),
		LogLevel:      strings.ToLower(env("FERRY_LOG_LEVEL", "info")),
		LogFormat:     strings.ToLower(env("FERRY_LOG_FORMAT", "text")),
		LogFile:       env("FERRY_LOG_FILE", ""),
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
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  %s", strings.Join(errs, "\n  "))
	}
	return c, nil
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
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
	if strings.HasSuffix(v, "d") { // time.ParseDuration has no days
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err == nil && n >= 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return def, fmt.Errorf("%s: expected a duration like 30m, 24h or 7d, got %q", k, v)
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
