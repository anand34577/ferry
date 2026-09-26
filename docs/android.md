# Android app

The app sends files directly to nearby devices — even without internet — and connects to your Ferry server for everything else. It works with other Ferry phones and with [LocalSend](https://localsend.org) on any platform.

Requires Android 10 or newer.

## Install

1. On the phone, open the [latest release](https://github.com/anand34577/ferry/releases/latest) and download `ferry_<version>.apk`.
2. Open it. If Android asks, allow installing apps from your browser or file manager.
3. Open **Ferry**.

## Connect to your server (optional)

Needed for links, your files and sending to your devices through the internet. Nearby transfers work without it.

1. In the web app, open **Devices**. It shows a QR code.
2. In the app, open **Server → Servers → Add server** and scan the code, or type the server address.
3. Sign in with your account (and your two-factor code, if you use one).

The phone now appears under **Devices** in the web app.

## Send files

Choose files in the app with **Send**, or use Android's **Share** menu in any app (Photos, Files, …) and pick **Ferry**. Then choose where to send:

| Option | When to use it |
|---|---|
| **Nearby** | Devices on the same Wi-Fi or hotspot with Ferry or LocalSend open in receive mode. No internet needed. |
| **My devices** | Your other signed-in devices. Ferry sends directly when they are nearby, and through your server otherwise. |
| **Create a link** | For anyone, anywhere. |

## Receive files from nearby devices

Open **Receive** on the home screen and tap **Start receiving**. Ferry asks before saving anything; accepted files are saved to **Downloads/Ferry**.

- **Remember this device as trusted** when accepting marks the sender as known. Under **Settings → Trusted devices** you can let trusted devices send without asking.
- **PIN:** on the **Receive** screen, turn on **Require a PIN** so senders must enter it before you are even asked.
- Receiving runs in the background and shows a notification while it is on. Turn it off when you don't need it to save battery.

Files sent to this phone from the web app or your other devices arrive the same way, with a prompt to accept.

## Device not showing up?

Both devices must be on the same Wi-Fi, or one can turn on its hotspot and let the other join. Some networks (guest Wi-Fi, hotels, offices) block devices from finding each other:

1. On the receiving phone, open **Receive** to show its QR code and pairing code.
2. On the sending phone, open **Send**, tap **Connect**, and scan the QR code or type the pairing code or IP address.
3. If the network blocks device-to-device traffic entirely, send a link instead — Ferry offers this automatically.

## Status and data use

The home screen shows **Wi-Fi**, **internet** and **server** separately: on Wi-Fi without internet, nearby transfers still work.
Ferry asks before sending large files over mobile data (threshold under **Settings → Transfers**) and can wait for Wi-Fi instead.

## Transfers

**Transfers** shows what is running and what finished. Interrupted transfers can be resumed or retried; if Ferry was closed during a transfer, it is listed as interrupted so you can send it again.
