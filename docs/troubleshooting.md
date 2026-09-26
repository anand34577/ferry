# Troubleshooting

## Installing and starting

| Problem | Solution |
|---|---|
| The page doesn't open | Check that Ferry is running (`ferry service status`, or `docker compose ps`). From another device, use the computer's address instead of `localhost`, and allow Ferry through the firewall. |
| "address already in use" | Another program uses port 8080. Set `FERRY_ADDR=:9000` (or another port) and restart. |
| `service install` says to use sudo / an Administrator terminal | Installing a service needs administrator rights. On Linux and macOS put `sudo` in front; on Windows right-click Terminal → **Run as administrator**. |
| Windows SmartScreen blocks `ferry.exe` | Choose **More info → Run anyway**. |
| macOS says the program can't be opened | Run `xattr -d com.apple.quarantine ferry` in its folder once. |
| The service stops right after starting | Look at the log (see [Logs](install-binary.md#manage-the-service)). The first lines name the setting that is wrong. |
| "invalid configuration" | The message lists each wrong setting and what is expected, e.g. sizes like `10GB`, durations like `7d`. |
| Docker: `env_file` error | Update Docker Compose to 2.24 or newer. |

## Signing in

| Problem | Solution |
|---|---|
| Forgot the administrator password | Reset it on the Ferry machine: `ferry user reset-password -email you@example.com -password 'new-password'` ([how to run commands](administration.md#command-line)). |
| Lost the phone with the authenticator app | Another administrator uses **Admin → Users → Turn off two-factor**, or on the Ferry machine: `ferry user disable-2fa -email you@example.com`. |
| "Too many failed attempts" | Wait 15 minutes, or sign in from another network. |
| "The first administrator account can only be created from the local network" | Open Ferry from a device on the same network as the server (e.g. `http://192.168.1.10:8080`) to create the first account, or set `FERRY_ADMIN_EMAIL` and `FERRY_ADMIN_PASSWORD` and restart. |
| No "Forgot your password?" link | Password reset needs email settings and `FERRY_PUBLIC_URL`. |
| Single sign-on (Keycloak etc.) fails | The sign-in page shows the reason; see [Single sign-on → Troubleshooting](sso.md#troubleshooting). |
| Email fails with "unencrypted connection" or a certificate error | For a local SMTP relay/proxy without TLS set `FERRY_SMTP_SECURITY=none`; for self-signed certificates `FERRY_SMTP_SKIP_VERIFY=true`. Change it in **Admin → Settings → Email** and test with **Send test email**, which shows the mail server's exact error. |

## Links and transfers

| Problem | Solution |
|---|---|
| Links show `localhost` or the wrong address | Set `FERRY_PUBLIC_URL` to the address people use. |
| Large uploads stop at a fixed size | A reverse proxy limits the size. Use the provided [Caddy or nginx examples](https.md), which remove the limit. |
| "Not enough storage space" / "server is out of storage space" | A quota or the disk is full. Raise the user's quota, delete files, or add disk space (`FERRY_MIN_FREE_SPACE` keeps 1 GB free by default). |
| "This file's data is missing" | The file's data was removed from the storage folder. Restore it from a backup. |
| "Storage is temporarily unavailable" | The storage folder is unreachable (e.g. an unmounted disk). Nothing is lost; fix the disk and try again. |
| Emails are not sent | Check the `FERRY_SMTP_*` settings and the log. Many providers need an app password. |
| Everyone shares one rate limit, or the audit log shows the proxy's IP | Set `FERRY_TRUSTED_PROXIES` ([HTTPS guide](https.md)). |

## Android app

| Problem | Solution |
|---|---|
| Nearby devices don't appear | Both must be on the same Wi-Fi or hotspot, with the receiver on **Receive**. On guest or office networks, connect with the QR or pairing code ([details](android.md#device-not-showing-up)). |
| "Identity changed … connection refused" | The other device was reinstalled or is a different device using the same address. If you trust it, remove it under **Settings → Trusted devices** and connect again. |
| The app can't reach the server | Use the same address as in the browser. For `https://` with a self-made certificate, install the certificate on the phone. |
| "needs a newer app" / "older Ferry" | Update the app or the server so both are current. |
| "You've already downloaded … with this link" | One-time and limited links give each file once per recipient. Ask the sender for a new link. |
| My phone shows as offline in the web app | A device is **online** while the Ferry app is open (it checks in every 15 seconds) or while it's receiving in the background, and goes offline about 90 seconds after that stops. Battery savers that pause apps in the background delay this; files sent meanwhile are delivered as soon as the app opens. |
