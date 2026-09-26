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

## Why these settings matter

| Setting | Effect |
|---|---|
| `FERRY_PUBLIC_URL` | Share links, QR codes and emails use this address. Required for password reset by email. |
| `FERRY_TRUSTED_PROXIES` | Ferry sees visitors' real IP addresses (for rate limits and the audit log) and marks cookies as HTTPS-only. |

## Security checklist

- [ ] Ferry is only reachable through HTTPS; port 8080 is not open to the internet.
- [ ] `FERRY_ALLOW_SIGNUP` stays `false` unless you want anyone to create accounts.
- [ ] Storage limits are set (`FERRY_DEFAULT_USER_QUOTA`, `FERRY_MAX_UPLOAD_SIZE`).
- [ ] Administrators use two-factor sign-in (**Settings → Two-factor sign-in**).
- [ ] Backups are in place ([Backup and restore](administration.md#backup-and-restore)).
