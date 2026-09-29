// Development only (`npm run dev` in a browser): a stand-in for the Go engine so the UI can be designed
// and reviewed without Windows networking. Never included in the app build.
import type { State, Transfer } from "./api";

const listeners: Record<string, ((d: unknown) => void)[]> = {};
const emit = (n: string, d: unknown) => (listeners[n] ?? []).forEach((f) => f(d));
const now = Date.now();

const t = (p: Partial<Transfer>): Transfer => ({
  id: Math.random().toString(36).slice(2), direction: "sent", method: "direct", peer: "Pixel 8", status: "transferring", note: "", error: "",
  files: [{ id: "1", name: "Holiday video.mp4", path: "C:\\Users\\me\\Videos\\Holiday video.mp4", size: 734003200, done: 412000000, mime: "video/mp4" }],
  total: 734003200, done: 412000000, speed: 28_400_000, createdAt: now, updatedAt: now, canPause: true, paused: false, canRetry: false, ...p,
});

const state: State = {
  version: "v1.4.0-dev",
  settings: { alias: "Anand's PC", downloadDir: "C:\\Users\\me\\Downloads\\Ferry", receiving: true, requirePin: false, pin: "", askWhereToSave: false, openWhenDone: false,
    autoAcceptOwn: false, trusted: [{ fingerprint: "AB12CD34EF56AB12CD34EF56", alias: "Pixel 8", autoAccept: true }], theme: "system", notifications: true, closeToTray: true,
    startWithWindows: false, linkExpiry: 604800, activeAccount: "a1", onboarded: true },
  identity: { fingerprint: "9F2C4E1A7B3D5F6E8A0B1C2D3E4F5A6B7C8D9E0F1A2B3C4D5E6F7A8B9C0D1E2F", port: 53317,
    addrs: [{ ip: "192.168.1.23", iface: "Wi-Fi", virtual: false }, { ip: "172.24.0.1", iface: "vEthernet (WSL)", virtual: true }], pairCode: "60A-R0BQ",
    qr: "ferry://peer?h=192.168.1.23&p=53317&f=9F2C&n=Anand%27s+PC&s=https", listening: true },
  peers: [
    { ip: "192.168.1.40", port: 53317, https: true, alias: "Pixel 8", model: "Google Pixel 8", type: "mobile", fingerprint: "AB12CD34EF56AB12CD34EF56", certPin: "x", source: "multicast", ferry: true, lastSeen: now },
    { ip: "192.168.1.52", port: 53317, https: true, alias: "MacBook Air", model: "macOS", type: "desktop", fingerprint: "CC", certPin: "x", source: "multicast", ferry: false, lastSeen: now },
    { ip: "192.168.1.61", port: 53317, https: false, alias: "Living room TV", model: "Android TV", type: "headless", fingerprint: "DD", certPin: "", source: "scan", ferry: false, lastSeen: now },
  ],
  scanning: false,
  transfers: [
    t({}),
    t({ direction: "received", peer: "MacBook Air", status: "waiting", done: 0, speed: 0, note: "Waiting for MacBook Air to start…" }),
    t({ method: "link", peer: "Link · Report.pdf", status: "completed", done: 734003200, speed: 0, shareUrl: "https://files.example.com/s/k3j2h4g5f6d7", updatedAt: now - 3600e3 }),
    t({ status: "failed", peer: "Living room TV", error: "Living room TV declined the files.", speed: 0, updatedAt: now - 7200e3 }),
  ],
  incoming: [],
  accounts: [{ id: "a1", url: "https://files.example.com", siteName: "Ferry", email: "me@example.com", userName: "Me", isAdmin: true, active: true }],
  devices: [
    { id: "d1", name: "Pixel 8", platform: "android", online: true, lastSeen: now, lanAddrs: ["192.168.1.40"] },
    { id: "d2", name: "Work laptop", platform: "windows", online: false, lastSeen: now - 86400e3, lanAddrs: [] },
  ],
  online: true,
  serverError: "",
  pending: [],
};

const ok = <T>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 150));
const App = new Proxy({} as Record<string, unknown>, {
  get: (_, name: string) => (...args: unknown[]) => {
    switch (name) {
      case "GetState":
        return ok(structuredClone(state));
      case "SaveSettings":
        Object.assign(state.settings, args[0]);
        return ok(structuredClone(state));
      case "Describe":
        return ok((args[0] as string[]).map((p) => ({ path: p, name: p.split("\\").pop(), size: 52_428_800, files: p.includes(".") ? 1 : 24, isDir: !p.includes(".") })));
      case "ChooseFiles":
        return ok(["C:\\Users\\me\\Documents\\Report.pdf", "C:\\Users\\me\\Pictures\\Trip"]);
      case "ChooseFolder":
        return ok("C:\\Users\\me\\Pictures\\Trip");
      case "Connect":
        return Promise.reject("couldn't connect to that device. Make sure it's on the same network");
      case "SendDirect":
      case "SendToDevice":
      case "CreateLink":
        return ok("t1");
      default:
        return ok(undefined);
    }
  },
});

window.go = { main: { App: App as never } };
window.runtime = {
  EventsOn(n, cb) {
    (listeners[n] ??= []).push(cb);
    return () => (listeners[n] = listeners[n].filter((f) => f !== cb));
  },
};
// Preview the incoming dialog: open the page with ?incoming
if (location.search.includes("incoming"))
  setTimeout(() => emit("incoming", [{ method: "direct", request: { id: "r1", alias: "Pixel 8", model: "Google Pixel 8", fingerprint: "AB", trusted: false,
    files: [{ name: "IMG_2041.jpg", size: 3_400_000 }, { name: "IMG_2042.jpg", size: 3_100_000 }, { name: "Trip/notes.txt", size: 2_000 }], totalBytes: 6_502_000 } }]), 600);
