export function bytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return `${n} B`;
  const u = ["KB", "MB", "GB", "TB", "PB"];
  let v = n;
  let i = -1;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1).replace(/\.0$/, "")} ${u[i]}`;
}

export const speed = (bps: number) => (bps > 0 ? `${bytes(bps)}/s` : "");

export function eta(left: number, bps: number): string {
  if (bps <= 0 || left <= 0) return "";
  const s = left / bps;
  if (s < 60) return `${Math.ceil(s)} s left`;
  if (s < 3600) return `${Math.ceil(s / 60)} min left`;
  return `${(s / 3600).toFixed(1)} h left`;
}

export function ago(ms: number, now = Date.now()): string {
  const d = Math.round((now - ms) / 1000);
  if (d < 45) return "just now";
  if (d < 3600) return `${Math.round(d / 60)} min ago`;
  if (d < 86400) return `${Math.round(d / 3600)} h ago`;
  if (d < 7 * 86400) return `${Math.round(d / 86400)} d ago`;
  return new Date(ms).toLocaleDateString();
}

export const plural = (n: number, one: string, many = one + "s") => `${n.toLocaleString()} ${n === 1 ? one : many}`;

export const EXPIRY: [string, number][] = [
  ["1 hour", 3600],
  ["1 day", 86400],
  ["7 days", 7 * 86400],
  ["30 days", 30 * 86400],
  ["Never", 0],
];
