# Ferry

**Send files to anyone, anywhere — from your own server, or directly between devices without internet.**

Ferry is self-hosted file sharing and transfer. Your files stay on hardware you control; no accounts with third parties, no tracking.

- **Share links** with expiry, passwords, one-time and limited downloads
- **Upload links** so anyone can send you files from a browser
- **Nearby transfers** between phones on the same Wi-Fi or hotspot — no internet needed, compatible with [LocalSend](https://localsend.org)
- **Your devices, anywhere:** send from the web or your phone to your other devices through your server
- **Resumable and verified:** transfers continue after a dropped connection, and every file is checked with SHA-256
- **Secure by default:** two-factor sign-in, single sign-on (OIDC: Keycloak, Authentik, Google, …), session management, rate limits, full audit log
- **Link analytics, notifications and themes:** see who opened and downloaded your links, get Gotify or email alerts, optional short links via Shortr, 12 colour themes

Ferry runs as a single program with the web app built in, on Windows, macOS and Linux (including Raspberry Pi), or in Docker. An Android app is included.

## Quick start

### Docker

```bash
docker run -d --name ferry --restart unless-stopped -p 8080:8080 -v ferry-data:/data ghcr.io/anand34577/ferry:latest
```

Or with Docker Compose: download [`compose.yaml`](compose.yaml) and run `docker compose up -d`.

### Program

Download the file for your system from the [latest release](https://github.com/anand34577/ferry/releases/latest), unpack it, and run:

| System | Try it | Install as a service (starts automatically) |
|---|---|---|
| Windows | double-click `ferry.exe` | `.\ferry.exe service install` in an Administrator terminal |
| macOS | `./ferry` | `sudo ./ferry service install` |
| Linux | `./ferry` | `sudo ./ferry service install` |

Then open **http://localhost:8080** and create the administrator account.

**Next:** [put Ferry online with HTTPS](docs/https.md) before sharing links outside your network.

## Documentation

| Get started | Use Ferry | Run a server |
|---|---|---|
| [Getting started](docs/getting-started.md) | [User guide](docs/user-guide.md) | [Configuration](docs/configuration.md) |
| [Install with Docker](docs/install-docker.md) | [Android app](docs/android.md) | [Administration](docs/administration.md) |
| [Install the program](docs/install-binary.md) | | [Single sign-on](docs/sso.md) |
| | | [Troubleshooting](docs/troubleshooting.md) |
| [HTTPS](docs/https.md) | | |

Developers: [Development guide](docs/development.md) · [Contributing](CONTRIBUTING.md)

## Project layout

| Folder | Contents |
|---|---|
| [`backend/`](backend) | Server (Go): API, share pages, storage, service management |
| [`web/`](web) | Web app (React, TypeScript), built into the server |
| [`android/`](android) | Android app (Kotlin, Jetpack Compose) |
| [`deployment/`](deployment) | Example settings and reverse proxy configurations |

## Security

Please report vulnerabilities privately as described in [SECURITY.md](SECURITY.md), not in public issues.

## License

[MIT](LICENSE)
