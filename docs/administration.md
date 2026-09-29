# Administration

Administrators see **Admin** in the web app.

| Tab | Use it to |
|---|---|
| **Overview** | See storage and disk space, users, active links, devices and transfers at a glance |
| **Users** | Add, edit, disable or delete users; set storage quotas; reset passwords; turn off two-factor sign-in |
| **Links** | See every link on the server and disable any of them |
| **Devices** | See every signed-in phone and sign it out |
| **Audit log** | Review sign-ins, failed passwords, link activity and admin actions |
| **System** | Check version and effective settings, view recent log lines, run cleanup |

## Users

- **Add a user:** **Users → Add user**, with an email and a first password. Turn on **Administrator** for full access.
- **Storage quota:** set per user under **Edit → Storage quota**; otherwise `FERRY_DEFAULT_USER_QUOTA` applies.
- **Disable** signs the person out everywhere and stops their links immediately, without deleting anything. **Delete** removes the user with all their files, links and devices.
- **Lost authenticator:** **Turn off two-factor** in the user's menu. They can set it up again under **Settings**.
- **Forgotten password:** edit the user and enter a new password, or let them use **Forgot your password?** when email is configured.
- **Too many failed sign-ins** lock an account for 15 minutes for that network only, so nobody can lock other people out.

## Backup and restore

Ferry keeps two things, and both must be backed up together:

| What | Where |
|---|---|
| **Database** (users, folders, file names, links, history) | `ferry.db` in the data folder, or your PostgreSQL database |
| **File contents** | `storage` in the data folder |

The data folder is:

| How Ferry runs | Data folder |
|---|---|
| Docker | the `ferry-data` volume |
| Program run by hand | `data` next to the program |
| Service on Linux | `/var/lib/ferry` |
| Service on macOS | `/usr/local/var/ferry` |
| Service on Windows | `C:\ProgramData\Ferry\data` |

**Simplest backup:** stop Ferry, copy the whole data folder, start Ferry.

- Service: `ferry service stop`, copy, `ferry service start`.
- Docker: `docker compose stop`, then
  `docker run --rm -v ferry-data:/data -v "$PWD":/backup alpine tar czf /backup/ferry-backup.tar.gz -C /data .`,
  then `docker compose start`.

**Without stopping** (SQLite): `sqlite3 ferry.db ".backup ferry-backup.db"`, then copy `storage`. For PostgreSQL use `pg_dump`.
`storage/uploads` only holds unfinished uploads and can be skipped.

**Restore:** stop Ferry, put the data folder back, start Ferry. Files whose data is missing show as "missing" to users; stray data without a database entry is cleaned up automatically after an hour.

## Upgrades

Back up first. Then:

- **Docker:** `docker compose pull && docker compose up -d`.
- **Service:** run `service install` from the new version's folder ([details](install-binary.md#update)).

Database updates run automatically on start and cannot be undone, which is why the backup matters. The web app and Android app show a clear message if their version doesn't match the server.

## Monitoring

| Endpoint | Purpose |
|---|---|
| `/healthz` | Ferry is running |
| `/readyz` | Database and storage are available (used by the Docker health check) |
| `/metrics` | Prometheus metrics; set `FERRY_METRICS_TOKEN` and send it as `Authorization: Bearer <token>` |

Logs never contain passwords or share links. **Admin → System** shows the most recent lines.

## Cleanup

Runs every hour (`FERRY_CLEANUP_INTERVAL`) and on demand from **Admin → System** or with `ferry cleanup`. It removes expired sessions, unfinished uploads older than `FERRY_UPLOAD_EXPIRY`, transfers to devices that were never picked up (after 7 days) or already delivered (after 24 hours), expired links after `FERRY_EXPIRED_SHARE_RETENTION`, old audit entries, and leftover data.

## Command line

Run these on the Ferry machine. With a service install, see [Admin commands](install-binary.md#admin-commands) for how to call `ferry`; with Docker, prefix `docker compose exec ferry`.

| Command | Purpose |
|---|---|
| `ferry user list` | List users |
| `ferry user create -email E -password P [-name N] [-admin]` | Create a user |
| `ferry user reset-password -email E -password P` | Set a new password (signs the user out everywhere) |
| `ferry user set-role -email E -role admin` | Make a user an administrator (`-role user` to undo) |
| `ferry user disable-2fa -email E` | Turn off two-factor sign-in |
| `ferry cleanup` | Run cleanup now |
| `ferry config example` / `ferry config path` | Show an example settings file / the file in use |
| `ferry service install\|uninstall\|start\|stop\|restart\|status` | Manage the system service |
| `ferry version` | Show the version |

## Scaling

Run one Ferry per set of data. A single instance comfortably serves a team; for more users, use PostgreSQL and a larger machine. Running several copies behind a load balancer is not supported.

## Server settings

**Admin → Settings** changes the site name, public address, sign-up, limits, email, single sign-on, short links and retention while Ferry runs — no restart. Environment variables still win and show as locked. See [Configuration](configuration.md#two-places-for-settings).

## Monitoring and admin powers

- **Audit log** (Admin → Audit log): every change made through the API is recorded — sign-ins, file and folder changes, links, transfers, settings and all admin actions — with user, IP and time. Search it, filter by user (Users → ⋯ → Activity) and export it as CSV. Entries are kept for `FERRY_AUDIT_RETENTION`.
- **Sign in as a user** (Users → ⋯): see and manage a user's files, links and settings as they would, for up to one hour. A banner offers “Return to admin”; everything done meanwhile is tagged in the audit log.
- **Sign out everywhere**, disable, reset password, turn off two-factor, **disconnect SSO**, change quota or role, delete.
- **Single sign-on:** users with a connected provider account show an **SSO** chip. Set-up and user matching rules: [Single sign-on](sso.md).
- **Links** (Admin → Links): search every link on the server, open its analytics, revoke or delete it.
- **Settings → Email → Send test email** checks your mail settings and shows the mail server's exact error, with the likely fix (untrusted certificate, wrong security mode for the port, rejected sign-in, …).
- **Settings → Single sign-on → Test connection** loads the provider's configuration with the saved settings and explains what's wrong (wrong issuer URL, untrusted certificate, public client without secret).

## Link analytics

Each link records when it is opened, downloaded, previewed or uploaded to, and wrong password attempts, with visitor IP and browser. Owners see it under Links → chart icon; admins can open it for any link. Events follow `FERRY_AUDIT_RETENTION`.

## Notifications (Gotify)

Users can add their own [Gotify](https://gotify.net) server and application token in Settings → Notifications. Links with “Notify me” turned on then push a message when files arrive or are downloaded (and send an email when SMTP is configured), at most once every 10 minutes per link. On servers where anyone can get an account (sign-up or SSO auto-create), only administrators may use a Gotify server on the local network.

## URL shortener

In **Admin → Settings → Short links** (or with `FERRY_SHORTENER_URL` and `FERRY_SHORTENER_TOKEN`), enter your Shortr address and an API key (a [Shortr](https://github.com/anand34577/shortr) API key with `links:write`) and every new link also gets a short URL, shown and copied by default. Regenerating or deleting a link removes its short URL too; links made earlier get one from Links → ⋯ → Create short link. If the shortener is unreachable, links still work with their full address.

Shortr must be able to accept your Ferry address as a link target. By default it rejects `localhost` and private-network addresses (`SHORTR_ALLOW_PRIVATE_TARGETS`), so short links for a LAN-only Ferry fail unless that is enabled on the Shortr side. Use the Shortr address that serves the API (its public port, if you run separate ports). Names longer than 200 characters are shortened to fit Shortr's title limit.
