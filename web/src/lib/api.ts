// Typed client for Ferry's /api/v1. Cookie auth + the X-Requested-With CSRF header.

export const CLIENT_API_VERSION = 1;
export const CLIENT_HEADER = `web/1.0.0 api=${CLIENT_API_VERSION}`;

export interface User {
  id: string;
  email: string;
  name: string;
  role: "admin" | "user";
  disabled: boolean;
  quotaBytes: number;
  createdAt: number;
  totpEnabled: boolean;
}

export interface ServerInfo {
  name: string;
  siteName: string;
  version: string;
  apiVersion: number;
  minClientApiVersion: number;
  capabilities: string[];
  serverTime: number;
  allowSignup: boolean;
  publicSharing: boolean;
  maxUploadBytes: number;
  oidc: { name: string; autoCreate: boolean } | null;
}

export interface FileItem {
  id: string;
  name: string;
  folderId: string;
  size: number;
  mime: string;
  sha256: string;
  createdAt: number;
  updatedAt: number;
}

export interface Folder {
  id: string;
  name: string;
  parentId: string;
  createdAt: number;
  updatedAt: number;
}

export interface Listing {
  folderId: string;
  breadcrumbs: Folder[];
  folders: Folder[];
  files: FileItem[];
  shared: string[];
  search?: string;
}

export interface Share {
  id: string;
  token: string;
  url: string;
  kind: "download" | "upload";
  name: string;
  message: string;
  hasPassword: boolean;
  expiresAt: number;
  maxDownloads: number;
  downloadCount: number;
  allowDownload: boolean;
  allowPreview: boolean;
  requireAuth: boolean;
  allowList: boolean;
  allowDelete: boolean;
  maxFileBytes: number;
  maxFiles: number;
  allowedTypes: string;
  folderId: string;
  uploadCount: number;
  uploadedBytes: number;
  notify: boolean;
  revoked: boolean;
  lastAccess: number;
  createdAt: number;
  updatedAt: number;
  status: "active" | "expired" | "revoked" | "exhausted";
  items?: { type: "file" | "folder"; id: string; name: string; size?: number; folderId: string }[];
  itemCount: number;
  ownerEmail?: string;
  shortUrl: string;
}

/** The link to hand out: the short URL when the server's URL shortener made one. */
export const shareLink = (s: Share) => s.shortUrl || s.url;

export interface LinkEvent {
  at: number;
  kind: "view" | "download" | "preview" | "upload" | "password_failed";
  ip: string;
  userAgent: string;
  referrer: string;
  detail: string;
}

export interface LinkAnalytics {
  share: { id: string; name: string; kind: "download" | "upload"; createdAt: number };
  totals: Record<LinkEvent["kind"], number>;
  visitors: number;
  days: number;
  events: LinkEvent[];
}

export interface Me {
  user: User;
  deviceId: string;
  gotifyUrl: string;
  gotifyConfigured: boolean;
  gotifySkipVerify?: boolean;
  hasPassword: boolean;
  impersonator?: string;
  prefs?: Record<string, unknown>;
}

export interface Device {
  id: string;
  name: string;
  platform: string;
  appVersion: string;
  lastSeen: number;
  createdAt: number;
  current: boolean;
  online: boolean;
  lanAddrs?: string[];
  userEmail?: string;
}

export type TransferStatus =
  | "created" | "waiting" | "negotiating" | "connecting" | "transferring" | "verifying" | "completed"
  | "failed" | "cancelled" | "expired" | "rejected" | "interrupted";

export interface Transfer {
  id: string;
  direction: "sent" | "received";
  method: "direct" | "server" | "link";
  status: TransferStatus;
  peer: string;
  sourceDeviceId: string;
  targetDeviceId: string;
  sourceDeviceName: string;
  targetDeviceName: string;
  shareId?: string;
  fileCount: number;
  totalBytes: number;
  bytesDone: number;
  error?: string;
  createdAt: number;
  updatedAt: number;
  files?: FileItem[];
}

export interface Usage {
  usedBytes: number;
  pendingBytes: number;
  quotaBytes: number;
  fileCount: number;
  maxUploadBytes: number;
}

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

type Listener = () => void;
const unauthorizedListeners = new Set<Listener>();
export function onUnauthorized(fn: Listener) {
  unauthorizedListeners.add(fn);
  return () => void unauthorizedListeners.delete(fn);
}

export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: {
        "X-Requested-With": "ferry",
        "X-Ferry-Client": CLIENT_HEADER,
        ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, "network", "Can't reach the server. Check your connection — nothing was lost.");
  }
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    /* non-JSON body (e.g. proxy error page) */
  }
  if (!res.ok) {
    const e = data?.error;
    if (res.status === 401 && path !== "/api/v1/auth/login") unauthorizedListeners.forEach((f) => f());
    if (e) throw new ApiError(res.status, e.code, e.message);
    if (res.status === 502 || res.status === 503 || res.status === 504)
      throw new ApiError(res.status, "unavailable", "The server is temporarily unavailable. Please try again in a moment.");
    throw new ApiError(res.status, "http_" + res.status, `The server returned an unexpected error (${res.status}).`);
  }
  return data as T;
}

export const get = <T>(p: string) => api<T>("GET", p);
export const post = <T>(p: string, b?: unknown) => api<T>("POST", p, b ?? {});
export const patch = <T>(p: string, b: unknown) => api<T>("PATCH", p, b);
export const put = <T>(p: string, b: unknown) => api<T>("PUT", p, b);
export const del = <T>(p: string) => api<T>("DELETE", p);

export const fileUrl = (id: string, inline = false) => `/api/v1/files/${id}/content${inline ? "?inline=1" : ""}`;
export interface SessionInfo {
  id: string;
  deviceId?: string;
  deviceName?: string;
  ip: string;
  userAgent: string;
  createdAt: number;
  lastSeen: number;
  expiresAt: number;
  current: boolean;
}

/** Downloads files/folders as one ZIP. The selection is posted first (it can be too long for a URL). */
export async function downloadZip(fileIds: string[], folderIds: string[], name?: string) {
  const { url } = await post<{ url: string }>("/api/v1/files/zip", { fileIds, folderIds, name });
  const a = document.createElement("a");
  a.href = url;
  a.download = "";
  a.click();
}
