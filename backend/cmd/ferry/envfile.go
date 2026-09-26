package main

import "strings"

// envExample is the annotated configuration file. Every setting is optional; the value shown is the default.
// deployment/.env.example is a copy of it (kept identical by a test).
const envExample = `# Ferry configuration. Every setting is optional; commented values are the defaults.
# Full reference: docs/configuration.md

# ---- Server ----
# Address to listen on.
# FERRY_ADDR=:8080
# FERRY_WEB_APP=true            # false: API-only (apps, share and upload links keep working)
# Public address, used in share links and emails. Set this when Ferry is reachable from the internet.
# FERRY_PUBLIC_URL=https://files.example.com
# Name shown in the web app and on share pages.
# FERRY_SITE_NAME=Ferry
# Where the database and files are stored.
# FERRY_DATA_DIR=./data

# ---- HTTPS ----
# Serve HTTPS directly (otherwise put Ferry behind a reverse proxy).
# FERRY_TLS_CERT=/path/to/fullchain.pem
# FERRY_TLS_KEY=/path/to/privkey.pem
# Reverse proxy addresses (IPs or CIDRs) whose X-Forwarded-For/-Proto headers are trusted.
# FERRY_TRUSTED_PROXIES=127.0.0.1

# ---- First administrator (alternative to the setup page) ----
# FERRY_ADMIN_EMAIL=admin@example.com
# FERRY_ADMIN_PASSWORD=

# ---- Accounts and sharing ----
# Let anyone create an account.
# FERRY_ALLOW_SIGNUP=false
# false = every link requires signing in.
# FERRY_PUBLIC_SHARING=true

# ---- Limits (sizes like 500MB or 10GB; 0 = unlimited) ----
# FERRY_MAX_UPLOAD_SIZE=0
# FERRY_DEFAULT_USER_QUOTA=0
# FERRY_GLOBAL_QUOTA=0
# FERRY_MAX_SHARE_SIZE=0
# FERRY_MAX_SHARE_FILES=0
# Uploads are refused when free disk space would fall below this.
# FERRY_MIN_FREE_SPACE=1GB
# Requests per minute per IP address (0 = off).
# FERRY_RATE_LIMIT=600

# ---- Email (upload notifications, emailing links, password reset) ----
# Email, single sign-on, short links, limits and the site name can also be set in the web app
# (Admin → Settings) without a restart. Variables set here win and show as locked there.
# FERRY_SMTP_HOST=smtp.example.com
# FERRY_SMTP_PORT=587
# FERRY_SMTP_USER=
# FERRY_SMTP_PASSWORD=
# FERRY_SMTP_FROM=ferry@example.com
# FERRY_SMTP_SECURITY=auto        # auto | starttls | tls | none (plain, e.g. a local SMTP relay/proxy)
# FERRY_SMTP_SKIP_VERIFY=false    # accept self-signed certificates
#
# Optional URL shortener (Shortr): share links also get a short URL.
# FERRY_SHORTENER_URL=https://s.example.com
# FERRY_SHORTENER_TOKEN=sk_...    # API key with the links:write scope
#
# Single sign-on with OpenID Connect (Keycloak, Authentik, Google, …). See docs/sso.md.
# FERRY_OIDC_ISSUER=https://keycloak.example.com/realms/myrealm
# FERRY_OIDC_CLIENT_ID=ferry
# FERRY_OIDC_CLIENT_SECRET=
# FERRY_OIDC_NAME=Keycloak
# FERRY_OIDC_AUTO_CREATE=false         # create accounts for people without one
# FERRY_OIDC_LINK_BY_EMAIL=true        # connect to existing accounts with the same verified email
# FERRY_OIDC_ALLOWED_DOMAINS=          # e.g. example.com
# FERRY_OIDC_ADMIN_GROUP=              # e.g. ferry-admins
# FERRY_OIDC_GROUPS_CLAIM=groups       # Keycloak realm roles: realm_access.roles

# ---- Database (SQLite by default) ----
# FERRY_DB_DRIVER=postgres
# FERRY_DB_DSN=postgres://ferry:password@localhost:5432/ferry?sslmode=disable

# ---- Logging and monitoring ----
# FERRY_LOG_LEVEL=info
# FERRY_LOG_FORMAT=text
# FERRY_LOG_FILE=
# Enables /metrics for Prometheus; send it as a Bearer token.
# FERRY_METRICS_TOKEN=
`

// envForService fills in the paths a system service needs, starting from base (the user's existing
// config file, or the example). Settings the user already set are kept.
func envForService(base, dataDir, logFile, addr string) string {
	const nl = "\n"
	set := func(s, key, val string) string {
		if val == "" {
			return s
		}
		for _, line := range strings.Split(s, nl) {
			if strings.HasPrefix(strings.TrimSpace(line), key+"=") {
				return s // already configured by the user
			}
		}
		if i := strings.Index(s, "# "+key+"="); i >= 0 {
			end := strings.Index(s[i:], nl)
			if end < 0 {
				end = len(s) - i
			}
			return s[:i] + key + "=" + val + s[i+end:]
		}
		return strings.TrimRight(s, nl) + nl + key + "=" + val + nl
	}
	if addr == ":8080" {
		addr = ""
	}
	s := set(base, "FERRY_DATA_DIR", dataDir)
	s = set(s, "FERRY_LOG_FILE", logFile)
	return set(s, "FERRY_ADDR", addr)
}
