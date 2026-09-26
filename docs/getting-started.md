# Getting started

Ferry runs on any computer you control — a server, a NAS, a Raspberry Pi or your own PC. It stores everything locally; nothing is sent to third parties.

## 1. Choose how to run it

| Method | Best for | Guide |
|---|---|---|
| **Docker** | Servers, NAS devices, anyone already using Docker | [Install with Docker](install-docker.md) |
| **Program + system service** | A dedicated machine without Docker; starts automatically with the computer | [Install the program](install-binary.md#run-as-a-system-service) |
| **Program, run by hand** | Trying Ferry out, or occasional use on your own PC | [Install the program](install-binary.md#try-it-out) |

All three run the same Ferry and store data the same way, so you can switch later (see [Backup and restore](administration.md#backup-and-restore)).

**Requirements:** Windows, macOS or Linux on x86-64 or ARM (including Raspberry Pi), and disk space for your files.

## 2. Create the administrator account

Open Ferry in a browser — `http://localhost:8080` on the same computer, or `http://<computer-address>:8080` from another device on your network.

The first visit shows **Welcome to Ferry**. Enter your name, email and a password (at least 8 characters). This account is the administrator.

## 3. Add people (optional)

Go to **Admin → Users → Add user** and give each person their email and a first password. They can change it under **Settings**.

To let people create their own accounts instead, set `FERRY_ALLOW_SIGNUP=true` (see [Configuration](configuration.md)).

## 4. Send your first file

1. Open **Home → Send**, choose one or more files.
2. Pick **Create a link**, then **Upload & create link**.
3. Copy the link or show the QR code. Anyone with the link can download — no account or app needed.

To receive files, open **Home → Receive** and share the upload link.

## 5. Install the Android app (optional)

Download the APK from the [latest release](https://github.com/anand34577/ferry/releases/latest) and follow [Android app](android.md). The app works on its own for nearby transfers and connects to your server for everything else.

## Next steps

- **Reach Ferry from the internet:** [Put Ferry online with HTTPS](https.md). Do this before sharing links with people outside your network.
- **Protect your account:** turn on two-factor sign-in under **Settings**.
- **Set limits:** storage quotas and maximum upload sizes in [Configuration](configuration.md#limits).
- **Plan backups:** [Backup and restore](administration.md#backup-and-restore).
