# Put Ferry online with HTTPS

On your home or office network, Ferry works as installed. To use it from the internet — for example to send links to people outside — put it behind HTTPS. Passwords, sessions and share links must never travel unencrypted over the internet.

## What you need

- A domain name pointing to your server's public IP address, for example `files.example.com`.
- Ports **80** and **443** reachable from the internet (on a home network: forward them in your router to the Ferry machine).

## Option A: Caddy (recommended)

[Caddy](https://caddyserver.com/docs/install) obtains and renews certificates automatically.

1. Install Caddy on the same machine as Ferry.
2. Replace the contents of Caddy's configuration file (usually `/etc/caddy/Caddyfile`) with [`deployment/Caddyfile`](../deployment/Caddyfile), and change `files.example.com` to your domain.
3. Reload Caddy: `sudo systemctl reload caddy`.
4. Add to Ferry's settings (see [Change settings](configuration.md#where-settings-live)):

   ```ini
   FERRY_PUBLIC_URL=https://files.example.com
   FERRY_TRUSTED_PROXIES=127.0.0.1
   ```

   If Ferry runs in **Docker**, use `FERRY_TRUSTED_PROXIES=172.16.0.0/12` instead, because Docker forwards connections from its own network.
5. Restart Ferry and open `https://files.example.com`.

## Option B: nginx

Use [`deployment/nginx.conf`](../deployment/nginx.conf) as the site configuration, with certificates from [Certbot](https://certbot.eff.org/). The important parts are already set: no upload size limit, no buffering, and long timeouts so large transfers are not cut off. Then set the same two Ferry settings as in Option A.

## Option C: HTTPS directly in Ferry

If you already have certificate files and no reverse proxy:

```ini
FERRY_ADDR=:443
FERRY_TLS_CERT=/path/to/fullchain.pem
FERRY_TLS_KEY=/path/to/privkey.pem
FERRY_PUBLIC_URL=https://files.example.com
```

Ferry does not renew certificates itself; restart it after the files are renewed. On Linux the service can use port 443 without running as root; make sure the `ferry` user can read the certificate files.

## Option D: Cloudflare Tunnel (no open ports)

A [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) publishes Ferry without opening ports on your router; Cloudflare handles HTTPS.

1. In the Cloudflare dashboard: **Zero Trust → Networks → Tunnels → Create a tunnel** (type *Cloudflared*), and copy the tunnel token.
2. Add a **public hostname**, e.g. `files.example.com`, with service `http://ferry:8080` (Docker, see below) or `http://localhost:8080` (cloudflared installed next to Ferry).
3. Run cloudflared next to Ferry. With Docker, add to the Ferry `compose.yaml`:

   ```yaml
     cloudflared:
       image: cloudflare/cloudflared:latest
       command: tunnel --no-autoupdate run
       environment:
         TUNNEL_TOKEN: "<your tunnel token>"
       restart: unless-stopped
   ```
4. Ferry settings:

   ```ini
   FERRY_PUBLIC_URL=https://files.example.com
   FERRY_TRUSTED_PROXIES=172.16.0.0/12   # cloudflared in Docker; use 127.0.0.1 when it runs on the same machine
   FERRY_COOKIE_SECURE=true
   ```
   Without `FERRY_TRUSTED_PROXIES`, every visitor looks like the tunnel's address — rate limits and sign-in lockouts would then apply to everyone at once. Ferry logs a warning when it detects this.
5. Upload size: Cloudflare limits a single request to 100 MB on the Free plan. Ferry uploads in 32 MB pieces, so large files work.

**Only the apps and share links, no web app?** Set `FERRY_WEB_APP=false`: Ferry then serves only the API (for the Android app and future desktop apps), share and upload links, and the password-reset page. See [API-only mode](#api-only-mode).

## API-only mode

With `FERRY_WEB_APP=false` the browser app (sign-in page, Files, Admin, …) isn't served at all — a smaller surface when Ferry is on the internet:

- **Keeps working:** the Ferry apps (everything they do goes through `/api/v1`), share links (`/s/…`), upload links (`/u/…`), password reset from the emailed link, health checks.
- **Not available:** managing files, users and settings in a browser, and sign-in with SSO (which happens in the browser). Administer with the command line (`ferry user …`) or temporarily turn the web app back on.
- Links that require signing in to the server can't be opened from a browser in this mode.

You can also keep the web app but expose only part of Ferry — e.g. publish just `/api/`, `/s/`, `/u/` and `/_ferry/` through the tunnel or proxy and use the web app only on your home network.

## Why these settings matter

| Setting | Effect |
|---|---|
| `FERRY_PUBLIC_URL` | Share links, QR codes and emails use this address. Required for password reset by email. |
| `FERRY_TRUSTED_PROXIES` | Ferry sees visitors' real IP addresses (for rate limits and the audit log) and marks cookies as HTTPS-only. |

## Security checklist

Before putting Ferry on the internet:

- [ ] **Create the administrator first**, on your local network (or with `FERRY_ADMIN_EMAIL` / `FERRY_ADMIN_PASSWORD`). Ferry refuses first-run setup from outside the local network, so a stranger can't claim a fresh server.
- [ ] Ferry is only reachable through HTTPS; port 8080 is not open to the internet.
- [ ] `FERRY_PUBLIC_URL` is set to the `https://` address, and `FERRY_TRUSTED_PROXIES` matches your proxy or tunnel (check the log for the "proxy that isn't trusted" warning).
- [ ] **Sign-up** (Admin → Settings → General) stays off unless you want anyone to create accounts. With sign-up or SSO account creation on, Ferry stops regular users from pointing notifications at your local network.
- [ ] Storage limits are set (Admin → Settings → Limits: storage per user, largest file).
- [ ] Administrators use two-factor sign-in (**Settings → Two-factor sign-in**), or SSO with multi-factor at the provider.
- [ ] Consider `FERRY_WEB_APP=false` if you only need the apps and share links from outside ([API-only mode](#api-only-mode)).
- [ ] `FERRY_METRICS_TOKEN` is either unset or a long random value.
- [ ] Backups are in place ([Backup and restore](administration.md#backup-and-restore)).

Built in and always on: HTTPS-only cookies behind HTTPS, CSRF protection, strict Content-Security-Policy and other security headers, rate limits on sign-in, link passwords, uploads and requests, account lockout after failed sign-ins, hashed passwords (bcrypt) and tokens, share tokens that are never logged, and an audit log of every change.
