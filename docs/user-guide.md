# User guide

## What do you want to do?

| I want to… | In the web app | In the Android app |
|---|---|---|
| Send files to anyone | **Send → Create a link** | **Send → Create a link** |
| Get files from someone | **Receive** → share the upload link | **Receive** (nearby) or an upload link |
| Send to someone next to me | — | **Send → Nearby** ([Android app](android.md)) |
| Move files between my devices | **Send →** choose your device | **Send → My devices** |
| Keep files on my server | **Files → Upload** | **Server → Files → Upload** |

People you send to never need an account or the app — a browser is enough.

## Send files with a link

1. Open **Send** and choose files (or drag them onto the page).
2. Choose **Create a link** and click **Upload & create link**.
3. Adjust the link if you like (below), then **Create link**.
4. Copy the link, show its QR code, or email it.

You can also share existing files: in **Files**, select them and choose **Share**.

### Link options

| Option | What it does |
|---|---|
| **Expires** | After this time nobody can start a download. A download already running can finish. |
| **Password protection** | Recipients must enter a password. It is never part of the link — send it separately. |
| **Download limit: One-time / Limited** | The link works for that many recipients. Each recipient can resume an interrupted download in the same browser; others are refused. Preview is turned off for these links. |
| **Allow downloads** | Turn off for preview-only links. |
| **Require sign-in** | Only people with an account on your server can open the link. |
| **Message** | Shown to recipients above the files. |

Manage your links in **Links**: see how often they were used, edit them, disable them, or give them a new address. Disabling takes effect immediately, even for downloads in progress.

## Receive files with an upload link

1. Open **Receive** and choose **Create upload link**.
2. Optionally set a maximum file size, number of files, allowed file types (e.g. `pdf, jpg`), an expiry and a password.
3. Share the link. Anyone with it can upload from their browser.

Received files go to a folder named after the link (you can choose another). Turn on **Email me when files arrive** to be notified (when your server has email set up).

## Send files to your own devices

In **Send**, choose one of your signed-in devices instead of **Create a link**. The files wait on your server until the device picks them up; the device asks before saving them. See [Android app](android.md) to add a phone.

## Your files

**Files** works like a folder on your computer: create folders, upload files or whole folders, rename, move, delete, and download several items as one ZIP.

- **Uploads resume automatically** after a lost connection or even a page reload. The tray at the bottom shows progress; you can pause, resume or cancel.
- **Same name?** When a file already exists you choose **Keep both** (`photo (1).jpg`), **Replace**, **Skip** or **Rename**.
- **Integrity:** every file is checked with a SHA-256 checksum; a damaged upload is rejected, never silently saved.
- **Preview** images, video, audio, PDF and text files in the browser.

## History

**History** lists transfers from all your devices: links, device-to-device transfers and uploads. Filter by sent, received or failed. Clearing history never deletes files.

## Account and security

Everything is under **Settings**.

- **Profile and password.** Changing your password signs you out everywhere else.
- **Two-factor sign-in.** Choose **Set up**, scan the QR code with an authenticator app (Google Authenticator, Microsoft Authenticator, Aegis, 1Password and others), and enter the code it shows. From then on, signing in asks for a code from the app. If you lose your phone, ask your administrator to turn it off.
- **Where you're signed in.** Lists every browser and app on your account. Sign out any one of them, or all others at once.
- **Forgot your password?** Use **Forgot your password?** on the sign-in page. The emailed link works once, for one hour. (Available when your server has email set up; otherwise ask your administrator.)
- **Appearance.** System, light or dark theme.
