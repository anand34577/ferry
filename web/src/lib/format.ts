import type { TransferStatus } from "./api";

// Difference between the server's clock and ours; expiry is enforced by the server, we only display it.
let skewMs = 0;
export function setServerTime(serverTime: number) {
  skewMs = serverTime - Date.now();
}
export const serverNow = () => Date.now() + skewMs;

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB", "PB"];
  let v = n;
  let i = -1;
  do {
    v /= 1024;
    i++;
  } while (v >= 1024 && i < units.length - 1);
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1).replace(/\.0$/, "")} ${units[i]}`;
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 31536000],
  ["month", 2592000],
  ["week", 604800],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
];

export function relativeTime(ms: number, now = serverNow()): string {
  const diff = (ms - now) / 1000;
  for (const [unit, secs] of UNITS) {
    if (Math.abs(diff) >= secs) return rtf.format(Math.round(diff / secs), unit);
  }
  return Math.abs(diff) < 30 ? "just now" : rtf.format(Math.round(diff), "second");
}

export function formatDate(ms: number): string {
  return new Date(ms).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function formatSpeed(bytesPerSec: number): string {
  return bytesPerSec > 0 ? `${formatBytes(bytesPerSec)}/s` : "";
}

export function formatEta(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "";
  if (seconds < 60) return `${Math.ceil(seconds)}s left`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} min left`;
  return `${(seconds / 3600).toFixed(1)} h left`;
}

export const statusLabel: Record<TransferStatus, string> = {
  created: "Preparing",
  waiting: "Waiting for device",
  negotiating: "Negotiating",
  connecting: "Connecting",
  transferring: "Transferring",
  verifying: "Verifying",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
  expired: "Expired",
  rejected: "Declined",
  interrupted: "Interrupted",
};

export const isFinalStatus = (s: TransferStatus) => ["completed", "failed", "cancelled", "expired", "rejected"].includes(s);

export function fileKind(mime: string, name = ""): "image" | "video" | "audio" | "pdf" | "archive" | "text" | "doc" | "other" {
  const m = mime.split(";")[0];
  const ext = name.split(".").pop()?.toLowerCase() ?? "";
  if (m.startsWith("image/")) return "image";
  if (m.startsWith("video/")) return "video";
  if (m.startsWith("audio/")) return "audio";
  if (m === "application/pdf") return "pdf";
  if (/zip|compressed|tar|rar|7z|gzip/.test(m) || ["zip", "rar", "7z", "gz", "tar"].includes(ext)) return "archive";
  if (["doc", "docx", "odt", "xls", "xlsx", "ods", "ppt", "pptx", "odp"].includes(ext)) return "doc";
  if (m.startsWith("text/")) return "text";
  return "other";
}

export function fileExt(name: string): string {
  const i = name.lastIndexOf(".");
  const e = i > 0 ? name.slice(i + 1).toUpperCase() : "";
  return e && e.length <= 4 ? e : "FILE";
}

export const previewable = (mime: string) =>
  /^(image\/(png|jpeg|gif|webp|bmp|avif)|video\/(mp4|webm|ogg)|audio\/|application\/pdf|text\/(plain|csv|markdown))/.test(mime);

// Durations offered for link expiry, in seconds (0 = never).
export const EXPIRY_OPTIONS: [string, number][] = [
  ["1 hour", 3600],
  ["1 day", 86400],
  ["7 days", 7 * 86400],
  ["30 days", 30 * 86400],
  ["Never", 0],
];

/** A short "Firefox on Windows"-style label from a User-Agent string. */
export function describeAgent(ua: string): string {
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : /okhttp/i.test(ua) ? "Ferry app" : "Browser";
  const os = /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Windows/.test(ua) ? "Windows" : /Mac OS X/.test(ua) ? "macOS" : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${browser} on ${os}` : browser;
}
