# Developer guide

## Architecture

```
            ┌──────────── Go server (single binary) ────────────┐
Browser ───▶│ /api/v1  JSON API (cookie auth + CSRF header)      │
            │ /s/ /u/  server-rendered share & upload pages      │──▶ SQLite | PostgreSQL (metadata)
Android ───▶│ /api/v1  (Bearer token per device)                 │──▶ storage/blobs (file data)
            │ tus uploads, Range downloads, cleanup, audit       │
            └────────────────────────────────────────────────────┘
Android ◀──────── LocalSend v2 over HTTPS (LAN, no server) ────────▶ Android / LocalSend apps
```


### Backend layout (`backend/`)

| Path | Purpose |
|---|---|
| `cmd/ferry` | entry point and command line: `serve`, `service …`, `config …`, `user …`, `cleanup`, `healthcheck` |
| `internal/config` | settings from `ferry.env` and `FERRY_*` environment variables |
| `internal/service` | system service install/control: systemd, launchd, Windows Service Control Manager |
| `internal/db` | one SQL dialect for SQLite + Postgres, migrations |
| `internal/storage` | `Backend` interface, local filesystem implementation |
| `internal/server` | HTTP handlers: auth, files, tus, shares, public pages, devices, transfers, admin, cleanup |
| `internal/server/templates` | public share/upload pages (HTML, CSS, progressive JS) |
| `internal/server/webui` | built web app (embedded) |

### Android layout (`android/app/src/main/java/dev/ferry/app`)

| Package | Purpose |
|---|---|
| `lan` | LocalSend receiver (NanoHTTPD, HTTPS), discovery (multicast, subnet scan, server-assisted), client with certificate pinning |
| `server` | Ferry API client, tus uploads, resumable downloads, server profiles |
| `transfer` | `TransferManager` state machine, foreground service, notifications, history, MediaStore |
| `ui` | Compose screens |

## Building from source

Requirements: Go (version in `backend/go.mod`), Node.js 22, and for Android JDK 17 with the Android SDK.

```bash
scripts/build.sh          # Linux/macOS → dist/ferry and dist/ferry.env
scripts/build.ps1         # Windows    → dist\ferry.exe and dist\ferry.env
cd android && ./gradlew assembleDebug
```

Cross-compile the server by setting `GOOS`/`GOARCH` before `scripts/build.sh`. The Docker image builds everything itself: `docker build -t ghcr.io/anand34577/ferry:latest .`

## Local development

```bash
# server (http://localhost:8080), data in ./data
cd backend && go run ./cmd/ferry
# web with hot reload (http://localhost:5173, proxies /api, /s, /u to :8080)
cd web && npm install && npm run dev
# android: open android/ in Android Studio, or
cd android && ./gradlew installDebug   # emulator reaches the host at http://10.0.2.2:8080
```

Tests:

```bash
cd backend && go test ./...                                   # SQLite
FERRY_TEST_PG_DSN=postgres://user@localhost:5432/ferrytest?sslmode=disable go test ./...   # Postgres
cd web && npm test && npm run lint
cd android && ./gradlew testDebugUnitTest
```

## API reference (v1)

All endpoints are under `/api/v1`, JSON in/out, times in Unix milliseconds (server clock).
Errors: `{"error":{"code":"quota_exceeded","message":"<human readable>"}}` with a meaningful HTTP status.
Auth: web uses the `ferry_session` cookie and must send `X-Requested-With: ferry` on mutations; apps send `Authorization: Bearer <token>`.
Clients should send `X-Ferry-Client: <platform>/<version> api=1`; an outdated client gets `426`.

| Method & path | Description |
|---|---|
| `GET /info` | server name, version, `apiVersion`, `minClientApiVersion`, capabilities, `serverTime` |
| `GET/POST /setup` | first-run status / create first admin |
| `POST /auth/login` | `{email,password,code?,device?}` → cookie (web) or `{token, device}` (apps). With 2FA on and no `code`: `401 totp_required`; wrong/reused code: `401 invalid_code` |
| `POST /auth/signup`, `POST /auth/logout` | |
| `GET /auth/oidc/start?next=&mode=login\|link`, `GET /auth/oidc/callback` | single sign-on redirect flow (browser navigation, not XHR). Errors come back as `/login?sso_error=…`; an unmatched identity as `/login?sso=connect` |
| `GET/DELETE /auth/oidc/pending`, `POST /auth/oidc/link` | identity waiting to be connected (signed cookie); connect it to the signed-in account |
| `GET /me/identities`, `DELETE /me/identities/{id}` | connected SSO accounts (can't remove the last sign-in method) |
| `POST /auth/return` | end an admin's "sign in as" session and restore the admin session |
| `POST /auth/forgot`, `POST /auth/reset` | `{email}` → emails a one-hour, single-use link (same answer whether or not the account exists); `{token,password}` → new password, all sessions signed out. Needs SMTP **and** `FERRY_PUBLIC_URL` (capability `password-reset`) |
| `GET/PATCH /me`, `POST /me/password`, `GET /me/usage` | profile (`name`, `gotifyUrl`, `gotifyToken`, `prefs` — merged, `null` removes a key; `GET` also returns `hasPassword`, `gotifyConfigured`, `impersonator`, `prefs`), password (signs out other sessions; `current` not needed when the account has none), quota usage |
| `POST /me/gotify/test` | send a test push to the user's Gotify |
| `POST /me/totp/setup\|enable\|disable` | two-factor sign-in: setup → `{secret, uri}` (otpauth QR); enable `{code}`; disable `{password}` |
| `GET /me/sessions`, `DELETE /me/sessions/{id}`, `POST /me/sessions/revoke-others` | where the account is signed in; sign out one or all others |
| `GET /files?folder=&q=&sort=name\|size\|date&order=` | folder listing or search |
| `POST /files/check` | `{folderId,names}` → name conflicts |
| `GET/PATCH/DELETE /files/{id}`, `GET /files/{id}/content[?inline=1]` | metadata, rename/move, delete, download (Range, `X-Content-SHA256`) |
| `GET /files/zip?files=&folders=`, `POST /files/zip` | uncompressed ZIP with a precomputed layout: `Content-Length`, `ETag` and `Range` requests (resumable); for large selections POST `{fileIds,folderIds,name}` → `{url}` (a 5-minute ticket URL). Fails up front with `file_missing` rather than sending an incomplete archive |
| `POST /files/batch` | `{action: delete\|move, fileIds, folderIds, targetFolderId}` |
| `POST /folders`, `PATCH/DELETE /folders/{id}` | |
| `POST /uploads` … | tus 1.0 (creation, termination, concatenation — `Upload-Concat: partial` parts, then `final;<urls>` with the metadata joins them into one file; not on upload links). Metadata: `filename`, `folderId`, `transferId`, `conflict` (`keep_both\|replace\|skip`), `sha256`. Final response headers: `Ferry-File-Id`, `Ferry-Sha256`, `Ferry-Skipped` |
| `GET /uploads` | my incomplete uploads |
| `GET/POST /shares`, `GET/PATCH/DELETE /shares/{id}` | links (`kind: download\|upload`, `expiresIn`, `password`, `maxDownloads`, flags…) |
| `POST /shares/{id}/regenerate`, `POST /shares/{id}/email` | new token (old URL and short URL die), email the link |
| `GET /shares/{id}/analytics?days=30` | `{totals, visitors, events[]}` — owner or admin |
| `POST /shares/{id}/shorten` | (re)create the short URL (capability `shortener`); shares carry `shortUrl` |
| `GET /devices`, `PATCH/DELETE /devices/{id}` | my devices (with LAN presence when fresh) |
| `PUT/DELETE /devices/current/presence` | publish/clear LAN addresses for server-assisted discovery |
| `GET/POST /transfers`, `GET/PATCH/DELETE /transfers/{id}`, `POST /transfers/clear` | unified history (`?limit=&before=<createdAt>&direction=sent\|received&status=a,b` — filters are exact, page with `before`); `targetDeviceId` creates a device-inbox transfer |
| `GET /transfers/{id}/files`, `GET /inbox` | files of a relayed transfer; transfers waiting for this device |
| `GET /admin/stats\|users\|shares\|devices\|audit\|system`, `POST /admin/users`, `PATCH/DELETE /admin/users/{id}`, `POST /admin/shares/{id}/revoke`, `DELETE /admin/shares/{id}`, `DELETE /admin/devices/{id}`, `POST /admin/cleanup` | administration |
| `GET /admin/audit?q=&user=&action=&before=&since=&limit=&format=csv` | searchable audit log, paged with `before`; `more: true` when older events exist |
| `GET/PATCH /admin/settings` | settings editable in the web UI (`{key: value}`, `null` resets to default); environment-set keys are `locked`, secrets are never returned |
| `POST /admin/users/{id}/impersonate\|signout`, `DELETE /admin/users/{id}/identities`, `POST /admin/test-email` | sign in as a user (web, 1 h), sign a user out everywhere, disconnect SSO, SMTP test |

Public routes: `GET /s/{token}`, `POST /s/{token}` (password), `GET /s/{token}/f/{fileId}[?inline=1]`, `GET /s/{token}/zip`,
`GET/POST /u/{token}` (page / password or multipart upload), `/u/{token}/tus[/{id}]` (tus), `POST /u/{token}/delete/{fileId}`.
Apps may reuse a download session with the `X-Download-Session` header (returned as `Ferry-Download-Session`).

Compatibility: fields are only added within v1. Breaking changes bump `apiVersion` and raise `minClientApiVersion` only when unavoidable.

### LAN protocol

LocalSend v2 (`/api/localsend/v2/info|register|prepare-upload|upload|cancel`, multicast `224.0.0.167:53317`) plus two Ferry extensions advertised by `"ferry":1`:
`GET /api/localsend/v2/ferry/offset?sessionId&fileId&token` → `{"offset":n}` and `POST …/ferry/verify?…&sha256=` (409 on mismatch). Uploads accept `&offset=n` to resume.
Peers are pinned to the TLS certificate seen on first contact, or to the fingerprint in a scanned QR code.

## Building an app (desktop, mobile, scripts)

Ferry's apps use only the API, so they keep working when the web app is turned off (`FERRY_WEB_APP=false`) or when only `/api/` is exposed through a tunnel. A new client (e.g. a desktop app for Windows, macOS or Linux) needs:

1. **Sign in** with `POST /api/v1/auth/login` and a `device` object (`name`, `platform`, `appVersion`, and `deviceId` on later sign-ins to keep the same device). The response has a `token`: send it as `Authorization: Bearer <token>` on every request. Two-factor accounts answer `401 totp_required`; send `code` and try again.
2. **Identify the client** with `X-Ferry-Client: <platform>/<version> api=1`, and check `GET /api/v1/info` (`apiVersion`, `minClientApiVersion`, `capabilities`) at start-up.
3. **Upload** with tus to `/api/v1/uploads` (any tus client library works; parallel parts are optional), and **download** with `GET /api/v1/files/{id}/content` using `Range` to resume; verify with `X-Content-SHA256`.
4. **Receive** files sent to the device by polling `GET /api/v1/inbox` every 15 seconds while running — this also shows the device as online.

Security for clients:
- Store the token in the operating system's credential store (Windows Credential Manager, macOS Keychain, Secret Service on Linux, Android Keystore), never in plain files.
- Refuse `http://` servers outside the local network, or warn clearly as the Android app does.
- Tokens are revocable per device (**Devices → Remove**, or `DELETE /api/v1/devices/{id}`); sign out with `POST /api/v1/auth/logout`.
- Browser-based clients (Electron or Tauri web views with their own origin) need that origin in `FERRY_CORS_ORIGINS`; native HTTP clients don't.
- Cookie authentication (the web app) requires the `X-Requested-With: ferry` header on changes; bearer-token clients don't need it.

## Transfer states

`created → waiting → negotiating → connecting → transferring → verifying → completed`, failures `failed | cancelled | expired | rejected | interrupted`.
Defined identically in `backend/internal/server/transfers.go`, `web/src/lib/api.ts` and `android/.../data/Models.kt`.

## Releasing

1. Update versions (`android/app/build.gradle.kts` `versionName`/`versionCode`, `web/package.json`).
2. Tag: `git tag v1.1.0 && git push --tags`.
3. The *Release* workflow builds archives for Linux (amd64, arm64, armv7), macOS (amd64, arm64) and Windows (amd64, arm64), each with the program and `ferry.env`; the signed APK/AAB (set `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD` secrets), the multi-arch container image on GHCR, checksums, and a GitHub release with notes.
