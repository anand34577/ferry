# Configuration

Ferry works without any configuration. Change settings only when you need to.

## Where settings live

| How Ferry runs | Where to put settings |
|---|---|
| Docker | `.env` in the Ferry folder ([how](install-docker.md#change-settings)) |
| Program run by hand | `ferry.env` next to the program |
| Service on Linux | `/etc/ferry/ferry.env` |
| Service on macOS | `/usr/local/etc/ferry/ferry.env` |
| Service on Windows | `C:\ProgramData\Ferry\ferry.env` |

The file has one `NAME=value` per line; lines starting with `#` are ignored. After a change, restart Ferry (`docker compose up -d`, or `ferry service restart`).

Settings can also be given as environment variables, which take priority over the file. `ferry config example` prints a complete example file; `ferry config path` shows which file is in use.

**Sizes** are written like `500MB`, `10GB` or `1TB`. **Durations** like `30m`, `24h` or `7d`.

## Server

| Setting | Default | Description |
|---|---|---|
| `FERRY_ADDR` | `:8080` | Address and port to listen on. `:8080` means all network interfaces; `127.0.0.1:8080` only this computer. |
| `FERRY_PUBLIC_URL` | — | The address people use to reach Ferry, e.g. `https://files.example.com`. Used in links and emails. |
| `FERRY_SITE_NAME` | `Ferry` | Name shown in the app, on share pages and in emails. |
| `FERRY_DATA_DIR` | `./data` | Folder for the database and files. |
| `FERRY_STORAGE_PATH` | `<data dir>/storage` | Folder for file contents, if it should be elsewhere (e.g. a larger disk). |

## HTTPS and proxies

See [Put Ferry online with HTTPS](https.md).

| Setting | Default | Description |
|---|---|---|
| `FERRY_TLS_CERT`, `FERRY_TLS_KEY` | — | Certificate and key files to serve HTTPS directly. |
| `FERRY_TRUSTED_PROXIES` | — | Reverse proxy addresses, comma separated (IPs or ranges like `172.16.0.0/12`). |
| `FERRY_COOKIE_SECURE` | `auto` | Mark sign-in cookies HTTPS-only: `auto`, `true` or `false`. |

## Accounts and sharing

| Setting | Default | Description |
|---|---|---|
| `FERRY_ADMIN_EMAIL`, `FERRY_ADMIN_PASSWORD` | — | Create the first administrator automatically instead of using the setup page. |
| `FERRY_ALLOW_SIGNUP` | `false` | Let anyone create an account. |
| `FERRY_PUBLIC_SHARING` | `true` | `false` makes every link require signing in. |
| `FERRY_SESSION_TTL` | `30d` | How long people stay signed in without using Ferry. |

## Limits

`0` means no limit.

| Setting | Default | Description |
|---|---|---|
| `FERRY_MAX_UPLOAD_SIZE` | `0` | Largest single file. |
| `FERRY_DEFAULT_USER_QUOTA` | `0` | Storage per user. Can be changed per user in **Admin → Users**. |
| `FERRY_GLOBAL_QUOTA` | `0` | Total storage for everyone. |
| `FERRY_MIN_FREE_SPACE` | `1GB` | Uploads are refused when free disk space would fall below this. |
| `FERRY_MAX_SHARE_SIZE` | `0` | Largest total size of one share link. |
| `FERRY_MAX_SHARE_FILES` | `0` | Most files in one share link or upload link. |
| `FERRY_RATE_LIMIT` | `600` | Requests per minute per IP address. Sign-in and link passwords have stricter built-in limits. |

## Email

Enables upload notifications, emailing links, and password reset (which also needs `FERRY_PUBLIC_URL`).

| Setting | Default | Description |
|---|---|---|
| `FERRY_SMTP_HOST` | — | Mail server, e.g. `smtp.gmail.com`. |
| `FERRY_SMTP_PORT` | `587` | `587` (STARTTLS) or `465` (TLS). |
| `FERRY_SMTP_USER`, `FERRY_SMTP_PASSWORD` | — | Mail account. For Gmail and similar, use an app password. |
| `FERRY_SMTP_FROM` | `FERRY_SMTP_USER` | Sender address. |

## Database

SQLite is built in and needs no setup. For PostgreSQL:

| Setting | Default | Description |
|---|---|---|
| `FERRY_DB_DRIVER` | `sqlite` | `sqlite` or `postgres`. |
| `FERRY_DB_DSN` | `<data dir>/ferry.db` | For PostgreSQL: `postgres://user:password@host:5432/ferry?sslmode=disable`. |

## Retention and cleanup

| Setting | Default | Description |
|---|---|---|
| `FERRY_UPLOAD_EXPIRY` | `24h` | How long an interrupted upload can be resumed. |
| `FERRY_DOWNLOAD_WINDOW` | `6h` | How long a recipient can resume a one-time or limited download. |
| `FERRY_EXPIRED_SHARE_RETENTION` | `30d` | How long expired or disabled links stay visible before removal. |
| `FERRY_AUDIT_RETENTION` | `90d` | How long audit log entries are kept. |
| `FERRY_CLEANUP_INTERVAL` | `1h` | How often cleanup runs. |

## Logging and monitoring

| Setting | Default | Description |
|---|---|---|
| `FERRY_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `FERRY_LOG_FORMAT` | `text` | `json` for log collectors. |
| `FERRY_LOG_FILE` | — | Also write logs to this file (rotated at 10 MB). Set automatically for the Windows service. |
| `FERRY_METRICS_TOKEN` | — | Enables `/metrics` (Prometheus) for requests with `Authorization: Bearer <token>`. |

## Advanced

| Setting | Default | Description |
|---|---|---|
| `FERRY_SCAN_COMMAND` | — | Virus scan for every upload; `{path}` is replaced by the file. A non-zero exit rejects the file. Example: `clamdscan --no-summary {path}`. |
| `FERRY_CORS_ORIGINS` | — | Other websites allowed to call the API, comma separated. |
| `FERRY_CONFIG` | — | Path of the settings file, instead of the default locations. Same as `--config`. |
