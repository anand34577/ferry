package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Field is a setting administrators can change in the web UI (Admin → Settings) without a restart.
// Values saved there are stored in the database; a FERRY_* variable set in the environment wins
// and locks the field.
type Field struct {
	Key     string   `json:"key"`
	Env     string   `json:"env"`
	Group   string   `json:"group"`
	Label   string   `json:"label"`
	Help    string   `json:"help,omitempty"`
	Kind    string   `json:"kind"` // text | url | secret | bool | int | size | duration | choice | list
	Choices []string `json:"choices,omitempty"`
	get     func(*Config) string
	set     func(*Config, string) error
}

func (f Field) Get(c *Config) string { return f.get(c) }

// Set parses v into c.
func (f Field) Set(c *Config, v string) error {
	if err := f.set(c, strings.TrimSpace(v)); err != nil {
		return fmt.Errorf("%s: %w", f.Label, err)
	}
	return nil
}

func textF(key, env, group, label, help string, p func(*Config) *string) Field {
	return Field{Key: key, Env: env, Group: group, Label: label, Help: help, Kind: "text",
		get: func(c *Config) string { return *p(c) },
		set: func(c *Config, v string) error { *p(c) = v; return nil }}
}

func urlF(key, env, group, label, help string, p func(*Config) *string) Field {
	f := textF(key, env, group, label, help, p)
	f.Kind = "url"
	f.set = func(c *Config, v string) error {
		if v != "" && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "http://") {
			return fmt.Errorf("enter a full address starting with https://")
		}
		*p(c) = strings.TrimRight(v, "/")
		return nil
	}
	return f
}

func secretF(key, env, group, label, help string, p func(*Config) *string) Field {
	f := textF(key, env, group, label, help, p)
	f.Kind = "secret"
	return f
}

func boolF(key, env, group, label, help string, p func(*Config) *bool) Field {
	return Field{Key: key, Env: env, Group: group, Label: label, Help: help, Kind: "bool",
		get: func(c *Config) string { return strconv.FormatBool(*p(c)) },
		set: func(c *Config, v string) error {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("expected true or false")
			}
			*p(c) = b
			return nil
		}}
}

func intF(key, env, group, label, help string, p func(*Config) *int) Field {
	return Field{Key: key, Env: env, Group: group, Label: label, Help: help, Kind: "int",
		get: func(c *Config) string { return strconv.Itoa(*p(c)) },
		set: func(c *Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return fmt.Errorf("expected a whole number, 0 or more")
			}
			*p(c) = n
			return nil
		}}
}

func sizeF(key, env, group, label, help string, p func(*Config) *int64) Field {
	return Field{Key: key, Env: env, Group: group, Label: label, Help: help, Kind: "size",
		get: func(c *Config) string { return FormatSize(*p(c)) },
		set: func(c *Config, v string) error {
			n, err := ParseSize(v)
			if err != nil {
				return err
			}
			*p(c) = n
			return nil
		}}
}

func durationF(key, env, group, label, help string, p func(*Config) *time.Duration) Field {
	return Field{Key: key, Env: env, Group: group, Label: label, Help: help, Kind: "duration",
		get: func(c *Config) string { return FormatDuration(*p(c)) },
		set: func(c *Config, v string) error {
			d, err := ParseDuration(v)
			if err != nil {
				return err
			}
			*p(c) = d
			return nil
		}}
}

func listF(key, env, group, label, help string, p func(*Config) *[]string) Field {
	return Field{Key: key, Env: env, Group: group, Label: label, Help: help, Kind: "list",
		get: func(c *Config) string { return strings.Join(*p(c), ", ") },
		set: func(c *Config, v string) error { *p(c) = splitList(v); return nil }}
}

// Fields lists every setting that can be managed in the web UI, in display order.
var Fields = []Field{
	textF("site_name", "FERRY_SITE_NAME", "general", "Site name", "Shown in the browser tab, on share pages and in emails.", func(c *Config) *string { return &c.SiteName }),
	urlF("public_url", "FERRY_PUBLIC_URL", "general", "Public address", "The address people use to reach Ferry, e.g. https://files.example.com. Used for links, emails, password reset and single sign-on.", func(c *Config) *string { return &c.PublicURL }),
	boolF("allow_signup", "FERRY_ALLOW_SIGNUP", "general", "Anyone can create an account", "Off: only administrators add users (or single sign-on creates them).", func(c *Config) *bool { return &c.AllowSignup }),
	boolF("public_sharing", "FERRY_PUBLIC_SHARING", "general", "Public links", "Off: every link requires signing in to this server.", func(c *Config) *bool { return &c.PublicSharing }),

	sizeF("max_upload_size", "FERRY_MAX_UPLOAD_SIZE", "limits", "Largest file", "e.g. 10GB. 0 = no limit.", func(c *Config) *int64 { return &c.MaxUploadBytes }),
	sizeF("default_user_quota", "FERRY_DEFAULT_USER_QUOTA", "limits", "Storage per user", "Default for users without their own quota. 0 = no limit.", func(c *Config) *int64 { return &c.DefaultUserQuota }),
	sizeF("global_quota", "FERRY_GLOBAL_QUOTA", "limits", "Total storage", "For all users together. 0 = no limit.", func(c *Config) *int64 { return &c.GlobalQuota }),
	sizeF("max_share_size", "FERRY_MAX_SHARE_SIZE", "limits", "Largest share link", "Total size of the files in one link. 0 = no limit.", func(c *Config) *int64 { return &c.MaxShareBytes }),
	intF("max_share_files", "FERRY_MAX_SHARE_FILES", "limits", "Most files per link", "0 = no limit.", func(c *Config) *int { return &c.MaxShareFiles }),

	textF("smtp_host", "FERRY_SMTP_HOST", "email", "Mail server", "e.g. smtp.gmail.com. Empty turns email off.", func(c *Config) *string { return &c.SMTPHost }),
	intF("smtp_port", "FERRY_SMTP_PORT", "email", "Port", "587 (STARTTLS), 465 (TLS) or 25.", func(c *Config) *int { return &c.SMTPPort }),
	{Key: "smtp_security", Env: "FERRY_SMTP_SECURITY", Group: "email", Label: "Connection security", Kind: "choice", Choices: []string{"auto", "starttls", "tls", "none"},
		Help: "auto: TLS on port 465, STARTTLS when offered. none: plain connection, for a local relay or proxy.",
		get:  func(c *Config) string { return c.SMTPSecurity },
		set:  func(c *Config, v string) error { c.SMTPSecurity = strings.ToLower(v); return nil }},
	boolF("smtp_skip_verify", "FERRY_SMTP_SKIP_VERIFY", "email", "Accept self-signed certificates", "", func(c *Config) *bool { return &c.SMTPSkipVerify }),
	textF("smtp_user", "FERRY_SMTP_USER", "email", "Username", "", func(c *Config) *string { return &c.SMTPUser }),
	secretF("smtp_password", "FERRY_SMTP_PASSWORD", "email", "Password", "For Gmail and similar, use an app password.", func(c *Config) *string { return &c.SMTPPass }),
	textF("smtp_from", "FERRY_SMTP_FROM", "email", "Sender address", "Defaults to the username.", func(c *Config) *string { return &c.SMTPFrom }),

	urlF("oidc_issuer", "FERRY_OIDC_ISSUER", "sso", "Issuer URL", "e.g. https://keycloak.example.com/realms/myrealm. Empty turns single sign-on off.", func(c *Config) *string { return &c.OIDC.Issuer }),
	textF("oidc_client_id", "FERRY_OIDC_CLIENT_ID", "sso", "Client ID", "", func(c *Config) *string { return &c.OIDC.ClientID }),
	secretF("oidc_client_secret", "FERRY_OIDC_CLIENT_SECRET", "sso", "Client secret", "Leave empty for a public client.", func(c *Config) *string { return &c.OIDC.ClientSecret }),
	textF("oidc_name", "FERRY_OIDC_NAME", "sso", "Button label", "Shown as “Sign in with …”, e.g. Keycloak.", func(c *Config) *string { return &c.OIDC.Name }),
	boolF("oidc_auto_create", "FERRY_OIDC_AUTO_CREATE", "sso", "Create accounts automatically", "Off: only people who already have an account can sign in; they connect it once with their password.", func(c *Config) *bool { return &c.OIDC.AutoCreate }),
	boolF("oidc_link_by_email", "FERRY_OIDC_LINK_BY_EMAIL", "sso", "Connect accounts with the same email", "Uses the email only when the provider marks it verified.", func(c *Config) *bool { return &c.OIDC.LinkByEmail }),
	boolF("oidc_trust_unverified_email", "FERRY_OIDC_TRUST_UNVERIFIED_EMAIL", "sso", "Trust unverified emails", "Only when users can't choose their own email in the provider (e.g. Microsoft Entra ID).", func(c *Config) *bool { return &c.OIDC.TrustUnverifiedEmail }),
	listF("oidc_allowed_domains", "FERRY_OIDC_ALLOWED_DOMAINS", "sso", "Allowed email domains", "Comma-separated, e.g. example.com. Empty allows all.", func(c *Config) *[]string { return &c.OIDC.AllowedDomains }),
	textF("oidc_admin_group", "FERRY_OIDC_ADMIN_GROUP", "sso", "Administrator group or role", "Members become administrators at each sign-in. Empty: manage roles here.", func(c *Config) *string { return &c.OIDC.AdminGroup }),
	textF("oidc_groups_claim", "FERRY_OIDC_GROUPS_CLAIM", "sso", "Groups claim", "groups, or realm_access.roles for Keycloak realm roles.", func(c *Config) *string { return &c.OIDC.GroupsClaim }),
	{Key: "oidc_scopes", Env: "FERRY_OIDC_SCOPES", Group: "sso", Label: "Scopes", Kind: "text", Help: "openid is always included.",
		get: func(c *Config) string { return strings.Join(c.OIDC.Scopes, " ") },
		set: func(c *Config, v string) error { c.OIDC.Scopes = ParseScopes(v); return nil }},
	boolF("oidc_skip_verify", "FERRY_OIDC_SKIP_VERIFY", "sso", "Accept a self-signed provider certificate", "For testing only.", func(c *Config) *bool { return &c.OIDC.SkipVerify }),

	urlF("shortener_url", "FERRY_SHORTENER_URL", "shortener", "Shortr address", "e.g. https://s.example.com. Empty turns short links off.", func(c *Config) *string { return &c.ShortenerURL }),
	secretF("shortener_token", "FERRY_SHORTENER_TOKEN", "shortener", "API key", "A Shortr API key with the links:write scope.", func(c *Config) *string { return &c.ShortenerToken }),

	durationF("share_retention", "FERRY_EXPIRED_SHARE_RETENTION", "retention", "Keep expired links for", "How long expired or disabled links stay visible before they're removed, e.g. 30d.", func(c *Config) *time.Duration { return &c.ShareRetention }),
	durationF("audit_retention", "FERRY_AUDIT_RETENTION", "retention", "Keep audit log and link analytics for", "e.g. 90d, 365d.", func(c *Config) *time.Duration { return &c.AuditRetention }),
}

// FieldByKey finds a UI setting.
func FieldByKey(k string) (Field, bool) {
	for _, f := range Fields {
		if f.Key == k {
			return f, true
		}
	}
	return Field{}, false
}

// ParseScopes splits "openid profile email" (spaces or commas) and makes sure openid comes first.
func ParseScopes(v string) []string {
	out := []string{"openid"}
	for _, sc := range strings.Fields(strings.ReplaceAll(v, ",", " ")) {
		if sc != "openid" {
			out = append(out, sc)
		}
	}
	return out
}

// FormatSize is the inverse of ParseSize for display: 10737418240 → "10GB".
func FormatSize(n int64) string {
	for _, u := range []struct {
		s string
		m int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}} {
		if n >= u.m && n%u.m == 0 {
			return strconv.FormatInt(n/u.m, 10) + u.s
		}
	}
	return strconv.FormatInt(n, 10)
}

// ParseDuration accepts Go durations plus days: "30m", "24h", "7d".
func ParseDuration(v string) (time.Duration, error) {
	if n, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil && strings.HasSuffix(v, "d") && n >= 0 {
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("expected a duration like 30m, 24h or 7d")
	}
	return d, nil
}

// FormatDuration shows whole days as "30d".
func FormatDuration(d time.Duration) string {
	if d > 0 && d%(24*time.Hour) == 0 {
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	return d.String()
}
