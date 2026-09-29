# Windows app

Ferry for Windows sends and receives files directly with nearby devices — no internet needed — and connects to your Ferry server for your devices anywhere and for share links. It works with the Ferry Android app and with [LocalSend](https://localsend.org) on any platform.

Requires Windows 10 or 11 (64-bit). It uses the Microsoft Edge WebView2 runtime, which Windows 11 includes; the installer adds it on older systems.

> This is the desktop **app**. The Ferry **server** for Windows is a different program (`ferry.exe`) — see [Install the program](install-binary.md). Both can run on the same PC.

## Install

1. Download `ferry-windows_<version>_setup.exe` from the [latest release](https://github.com/anand34577/ferry/releases/latest).
2. Run it and confirm the Windows prompt (administrator rights are needed once, to allow Ferry through Windows Firewall).
3. Ferry starts and appears in the notification area (tray).

The installer:

- allows Ferry through Windows Firewall on **private** and **domain** networks (not public ones);
- adds **Ferry** to Explorer's **Send to** menu;
- creates Start menu and desktop shortcuts.

A portable `…_portable.exe` is also available; it needs no installation, but Windows will ask about the firewall the first time, and **Send to** isn't added.

## Send files

Drop files or folders on the window, use **Choose files / Choose folder**, or right-click files in Explorer → **Send to** → **Ferry**. Then pick where they go:

| Option | When to use it |
|---|---|
| **Nearby** | Phones and computers on the same Wi-Fi or network with Ferry or LocalSend open. Click a device to send. |
| **My devices** | Your own devices signed in to your Ferry server. Ferry sends directly when they are nearby, and through the server otherwise — offline devices get the files when they come online. |
| **Link** | For anyone, anywhere: the files are uploaded to your server and you get a link (with expiry, download limit and password options) plus a QR code. |

Folders keep their structure. Transfers to Ferry devices resume after a dropped connection and are checked with SHA-256; **Transfers** shows progress, speed and time left, and lets you pause, resume, cancel and retry.

**Device not listed?** Some networks (guest Wi-Fi, hotels, offices) block devices from finding each other. Click **Scan network**, or type the other device's IP address or pairing code (shown on its **Receive** screen) under **Connect**.

## Receive files

Ferry receives while it is running — also from the tray when the window is closed. You are always asked first, with the sender and the file list; files are saved in **Downloads\Ferry** (change it under **Settings**, or choose **Save to…** each time).

- **Receive** shows this PC's pairing code, address and a QR code a phone can scan.
- **Require a PIN** makes senders enter it before you are even asked (5 wrong tries lock it for a minute).
- **Remember this device as trusted** when accepting; under **Settings → Trusted devices** you can let trusted devices send without asking. A trusted device's identity is verified cryptographically every time.
- Files sent to this PC from the web app or your other devices (through your server) arrive with the same prompt; **Settings → Accept files from my own devices without asking** skips it.

If nearby devices can't find this PC, make sure Windows treats the network as **Private** (Settings → Network & internet → your Wi-Fi or Ethernet → Private network).

## Connect to your server

**My devices → Connect to your Ferry server**: enter the address you use in the browser (e.g. `files.example.com`, or `192.168.1.10:8080` on your home network), then your email and password (and your two-factor code if you use one). The sign-in is stored encrypted for your Windows account.

- Accounts that sign in with single sign-on only: set a password in the web app (**Settings**) first.
- **Files**, **Links** and **Admin** open the server's web app in your browser.
- You can add several servers and switch between them.

## Settings

Device name, download folder, notifications, **Keep running in the tray when closed**, **Start with Windows** (starts in the tray), light/dark theme, trusted devices and the default link expiry.

## Where things are stored

| | |
|---|---|
| Settings, transfer history, device identity, log (`ferry.log`) | `%APPDATA%\Ferry` |
| Browser cache of the app window | `%LOCALAPPDATA%\Ferry\WebView2` |
| Received files | `Downloads\Ferry` (or the folder you chose) |

Uninstalling removes the program, the firewall rule, the **Send to** entry and the startup entry, and keeps your settings and received files.

## Build from source

Requirements: Go 1.26, Node.js 22, [Wails](https://wails.io) v2.16 (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`) and, for the installer, [Inno Setup 6](https://jrsoftware.org/isinfo.php).

```bash
cd desktop
wails build -clean
"C:/Program Files (x86)/Inno Setup 6/ISCC.exe" /DAppVersion=1.7.3 build/windows/installer.iss
```

This writes `build/bin/FerryDesktop.exe` and the installer `build/bin/Ferry-setup.exe`. For UI work, `npm run dev` in `desktop/frontend` runs the interface in a browser with a stand-in for the Windows engine.
