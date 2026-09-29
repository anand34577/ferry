// Typed bridge to the Go app (Wails bindings on window.go, events on window.runtime).

export interface Settings {
  alias: string;
  downloadDir: string;
  receiving: boolean;
  requirePin: boolean;
  pin: string;
  askWhereToSave: boolean;
  openWhenDone: boolean;
  autoAcceptOwn: boolean;
  trusted: { fingerprint: string; alias: string; autoAccept: boolean }[] | null;
  theme: "system" | "light" | "dark";
  notifications: boolean;
  closeToTray: boolean;
  startWithWindows: boolean;
  linkExpiry: number;
  activeAccount: string;
  onboarded: boolean;
}

export interface Peer {
  ip: string;
  port: number;
  https: boolean;
  alias: string;
  model: string;
  type: string;
  fingerprint: string;
  certPin: string;
  source: string;
  ferry: boolean;
  accountDeviceId?: string;
  lastSeen: number;
}

export interface TFile {
  id: string;
  name: string;
  path: string;
  size: number;
  done: number;
  mime: string;
}

export type Status = "created" | "waiting" | "connecting" | "transferring" | "verifying" | "completed" | "failed" | "cancelled" | "rejected" | "interrupted";

export interface Transfer {
  id: string;
  direction: "sent" | "received";
  method: "direct" | "server" | "link";
  peer: string;
  status: Status;
  note: string;
  error: string;
  files: TFile[] | null;
  total: number;
  done: number;
  speed: number;
  createdAt: number;
  updatedAt: number;
  shareUrl?: string;
  saveDir?: string;
  canPause: boolean;
  paused: boolean;
  canRetry: boolean;
}

export interface Incoming {
  request: { id: string; alias: string; model: string; fingerprint: string; trusted: boolean; files: { name: string; size: number }[] | null; totalBytes: number };
  method: "direct" | "server";
}

export interface Account {
  id: string;
  url: string;
  siteName: string;
  email: string;
  userName: string;
  isAdmin: boolean;
  active: boolean;
}

export interface Device {
  id: string;
  name: string;
  platform: string;
  online: boolean;
  lastSeen: number;
  lanAddrs?: string[] | null;
}

export interface Identity {
  fingerprint: string;
  port: number;
  addrs: { ip: string; iface: string; virtual: boolean }[] | null;
  pairCode: string;
  qr: string;
  listening: boolean;
  error?: string;
}

export interface State {
  version: string;
  settings: Settings;
  identity: Identity;
  peers: Peer[] | null;
  scanning: boolean;
  transfers: Transfer[] | null;
  incoming: Incoming[] | null;
  accounts: Account[] | null;
  devices: Device[] | null;
  online: boolean;
  serverError: string;
  pending: string[] | null;
}

export interface Item {
  path: string;
  name: string;
  size: number;
  files: number;
  isDir: boolean;
  error?: string;
}

export interface LinkOpts {
  expiresIn: number;
  maxDownloads: number;
  password: string;
  message: string;
}

type Go = {
  GetState(): Promise<State>;
  SaveSettings(p: Partial<Settings>): Promise<State>;
  SetTrusted(fp: string, alias: string, autoAccept: boolean): Promise<void>;
  Untrust(fp: string): Promise<void>;
  ChooseFiles(): Promise<string[] | null>;
  ChooseFolder(title: string): Promise<string>;
  Describe(paths: string[]): Promise<Item[] | null>;
  ShowInFolder(id: string): Promise<void>;
  OpenFile(path: string): Promise<void>;
  OpenDownloads(): Promise<void>;
  CopyText(s: string): Promise<void>;
  OpenURL(u: string): Promise<void>;
  Quit(): Promise<void>;
  RespondIncoming(id: string, accept: boolean, trust: boolean, dir: string): Promise<void>;
  AnswerPIN(id: string, pin: string): Promise<void>;
  Cancel(id: string): Promise<void>;
  Pause(id: string): Promise<void>;
  Resume(id: string): Promise<void>;
  Retry(id: string): Promise<void>;
  Dismiss(id: string): Promise<void>;
  ClearHistory(): Promise<void>;
  Discover(active: boolean): Promise<void>;
  Scan(): Promise<void>;
  Connect(input: string): Promise<Peer>;
  ForgetPeer(key: string): Promise<void>;
  SendDirect(peerKey: string, paths: string[]): Promise<string>;
  SendToDevice(deviceId: string, paths: string[]): Promise<string>;
  CreateLink(paths: string[], o: LinkOpts): Promise<string>;
  CheckServer(url: string): Promise<{ url: string; siteName: string; version: string; sso: boolean; secure: boolean }>;
  SignIn(url: string, email: string, password: string, code: string): Promise<State>;
  SignOut(id: string): Promise<State>;
  SwitchAccount(id: string): Promise<State>;
  OpenWebApp(page: string): Promise<void>;
  RefreshDevices(): Promise<State>;
};

declare global {
  interface Window {
    go?: { main: { App: Go } };
    runtime?: { EventsOn(name: string, cb: (...data: any[]) => void): () => void };
  }
}

/** The Go app. Outside the desktop shell (e.g. `npm run dev` in a browser) calls reject clearly. */
export const app: Go = new Proxy({} as Go, {
  get: (_, name: string) => (...args: unknown[]) => {
    const fn = (window.go?.main.App as any)?.[name];
    if (!fn) return Promise.reject(new Error("Ferry's engine isn't available (open the Ferry app, not a browser)."));
    return fn(...args);
  },
});

export function on<T = unknown>(event: string, cb: (data: T) => void): () => void {
  return window.runtime?.EventsOn(event, cb) ?? (() => {});
}

/** Go errors arrive as strings; make them readable sentences. */
export function message(e: unknown): string {
  const s = e instanceof Error ? e.message : typeof e === "string" ? e : "Something went wrong.";
  const t = s.trim();
  return t ? t[0].toUpperCase() + t.slice(1) + (/[.!?)]$/.test(t) ? "" : ".") : "Something went wrong.";
}

export const peerKey = (p: Peer) => p.fingerprint || `${p.ip}:${p.port}`;
