import { useEffect, useState } from "react";
import { del, get, patch, post, shareLink, type Device, type Share, type User } from "../lib/api";
import { LinkAnalyticsDialog } from "../components/LinkAnalytics";
import { ServerSettings } from "../components/ServerSettings";
import { formatBytes, formatDate, relativeTime } from "../lib/format";
import { useAuth } from "../lib/auth";
import { Icon } from "../components/Icon";
import { EmptyState, ErrorBox, Loading, Menu, Modal, StatusChip, Switch, useAsync, useDialogs, useToast } from "../components/ui";

type Tab = "overview" | "users" | "links" | "devices" | "audit" | "settings" | "system";

export function Admin() {
  const [tab, setTab] = useState<Tab>("overview");
  const [auditUser, setAuditUser] = useState<{ id: string; email: string } | null>(null);
  const tabs: [Tab, string][] = [
    ["overview", "Overview"],
    ["users", "Users"],
    ["links", "Links"],
    ["devices", "Devices"],
    ["audit", "Audit log"],
    ["settings", "Settings"],
    ["system", "System"],
  ];
  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>Administration</h1>
          <p className="muted">Users, storage and server health.</p>
        </div>
      </header>
      <div className="tabs" role="tablist">
        {tabs.map(([k, l]) => (
          <button key={k} role="tab" aria-selected={tab === k} onClick={() => (setTab(k), k !== "audit" && setAuditUser(null))}>
            {l}
          </button>
        ))}
      </div>
      {tab === "overview" && <Overview />}
      {tab === "users" && <Users onActivity={(u) => (setAuditUser(u), setTab("audit"))} />}
      {tab === "links" && <AdminLinks />}
      {tab === "devices" && <AdminDevices />}
      {tab === "audit" && <Audit user={auditUser} clearUser={() => setAuditUser(null)} />}
      {tab === "settings" && <ServerSettings />}
      {tab === "system" && <System />}
    </div>
  );
}

interface Stats {
  users: number;
  files: number;
  usedBytes: number;
  activeShares: number;
  devices: number;
  onlineDevices: number;
  pendingUploads: number;
  activeTransfers: number;
  activeDownloads: number;
  activeUploads: number;
  globalQuotaBytes: number;
  storageHealthy: boolean;
  diskTotalBytes?: number;
  diskFreeBytes?: number;
}

function Overview() {
  const { data, error, reload } = useAsync(() => get<Stats>("/api/v1/admin/stats"), []);
  if (error) return <ErrorBox error={error} onRetry={reload} />;
  if (!data) return <Loading />;
  const cards: [string, string, string?][] = [
    ["Storage used", formatBytes(data.usedBytes), data.globalQuotaBytes ? `of ${formatBytes(data.globalQuotaBytes)} limit` : `${data.files} files`],
    ["Disk free", data.diskFreeBytes !== undefined ? formatBytes(data.diskFreeBytes) : "—", data.diskTotalBytes ? `of ${formatBytes(data.diskTotalBytes)}` : undefined],
    ["Users", String(data.users)],
    ["Active links", String(data.activeShares)],
    ["Devices", String(data.devices), `${data.onlineDevices} active in the last 10 min`],
    ["Transfers now", String(data.activeUploads + data.activeDownloads), `${data.activeUploads} up · ${data.activeDownloads} down · ${data.pendingUploads} resumable`],
  ];
  return (
    <>
      {!data.storageHealthy && (
        <div className="error-box" role="alert">
          <Icon name="alert" />
          <p>Storage is unavailable. Downloads and uploads will fail until the storage path is reachable again. Check the System tab logs.</p>
        </div>
      )}
      <div className="stat-grid">
        {cards.map(([l, v, s]) => (
          <div key={l} className="stat">
            <span className="muted small">{l}</span>
            <strong>{v}</strong>
            {s && <span className="muted small">{s}</span>}
          </div>
        ))}
      </div>
    </>
  );
}

type AdminUser = User & { usedBytes: number; fileCount: number; lastActive: number; effectiveQuotaBytes: number; ssoLinks: number; hasPassword: boolean };
const GB = 1024 ** 3;

function Users({ onActivity }: { onActivity: (u: { id: string; email: string }) => void }) {
  const { user: me } = useAuth();
  const { data, error, reload } = useAsync(() => get<{ users: AdminUser[]; defaultQuotaBytes: number }>("/api/v1/admin/users"), []);
  const [editing, setEditing] = useState<AdminUser | "new" | null>(null);
  const toast = useToast();
  const dialogs = useDialogs();
  const act = async (fn: () => Promise<unknown>, msg: string) => {
    try {
      await fn();
      toast.ok(msg);
      reload();
    } catch (e) {
      toast.error(e);
    }
  };
  if (error) return <ErrorBox error={error} onRetry={reload} />;
  if (!data) return <Loading />;
  return (
    <>
      <div className="toolbar end">
        <button className="btn primary" onClick={() => setEditing("new")}>
          <Icon name="plus" size={18} /> Add user
        </button>
      </div>
      <div className="table-scroll">
        <table className="dtable">
          <thead>
            <tr>
              <th>User</th>
              <th>Role</th>
              <th>Storage</th>
              <th>Last active</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {data.users.map((u) => (
              <tr key={u.id} className={u.disabled ? "dim" : ""}>
                <td>
                  <strong>{u.name}</strong>
                  <div className="muted small">{u.email}</div>
                </td>
                <td>
                  <span className={"chip " + (u.role === "admin" ? "info" : "muted")}>{u.role}</span> {u.disabled && <span className="chip warn">disabled</span>}{" "}
                  {u.ssoLinks > 0 && <span className="chip ok" title={u.hasPassword ? "Signs in with SSO or password" : "Signs in with SSO only"}>SSO</span>}
                </td>
                <td>
                  {formatBytes(u.usedBytes)}
                  <div className="muted small">
                    {u.effectiveQuotaBytes > 0 ? `of ${formatBytes(u.effectiveQuotaBytes)}` : "no limit"}
                    {u.quotaBytes === -1 && u.effectiveQuotaBytes > 0 ? " (default)" : ""}
                  </div>
                </td>
                <td className="muted small">{u.lastActive ? relativeTime(u.lastActive) : "never"}</td>
                <td className="right">
                  <Menu
                    items={[
                      { label: "Edit", icon: "edit", onClick: () => setEditing(u) },
                      { label: "Activity", icon: "clock", onClick: () => onActivity({ id: u.id, email: u.email }) },
                      u.id !== me?.id &&
                        !u.disabled && {
                          label: "Sign in as this user",
                          icon: "users",
                          onClick: async () => {
                            const ok = await dialogs.confirm(
                              `Sign in as ${u.email}?`,
                              "You'll see and can change everything they can, for up to one hour. Everything you do is recorded in the audit log. Use “Return to admin” in the banner to come back.",
                              "Sign in as user",
                            );
                            if (!ok) return;
                            try {
                              await post(`/api/v1/admin/users/${u.id}/impersonate`);
                              window.location.assign("/");
                            } catch (e) {
                              toast.error(e);
                            }
                          },
                        },
                      u.ssoLinks > 0 && {
                        label: "Disconnect SSO",
                        icon: "link",
                        onClick: async () => {
                          const warn = u.hasPassword ? "They can connect again from Settings or by signing in with SSO." : "They have no password: set one with Edit first, or they won't be able to sign in.";
                          if (await dialogs.confirm(`Disconnect SSO for ${u.email}?`, warn, "Disconnect", true)) act(() => del(`/api/v1/admin/users/${u.id}/identities`), "SSO disconnected");
                        },
                      },
                      u.id !== me?.id && {
                        label: "Sign out everywhere",
                        icon: "logout",
                        onClick: () => act(() => post(`/api/v1/admin/users/${u.id}/signout`), "User signed out of all devices"),
                      },
                      u.id !== me?.id &&
                        (u.disabled
                          ? { label: "Enable", icon: "check", onClick: () => act(() => patch(`/api/v1/admin/users/${u.id}`, { disabled: false }), "User enabled") }
                          : { label: "Disable", icon: "x", onClick: () => act(() => patch(`/api/v1/admin/users/${u.id}`, { disabled: true }), "User disabled and signed out") }),
                      u.totpEnabled && {
                        label: "Turn off two-factor",
                        icon: "shield",
                        onClick: async () => {
                          if (await dialogs.confirm(`Turn off two-factor sign-in for ${u.email}?`, "Use this when they lost their authenticator app. They can set it up again in Settings.", "Turn off"))
                            act(() => patch(`/api/v1/admin/users/${u.id}`, { disableTotp: true }), "Two-factor sign-in turned off");
                        },
                      },
                      u.id !== me?.id && {
                        label: "Delete",
                        icon: "trash",
                        danger: true,
                        onClick: async () => {
                          if (await dialogs.confirm(`Delete ${u.email}?`, "All of their files, links and devices are permanently deleted.", "Delete user", true))
                            act(() => del(`/api/v1/admin/users/${u.id}`), "User deleted");
                        },
                      },
                    ]}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <UserDialog user={editing} self={me?.id} defaultQuota={data.defaultQuotaBytes} onClose={() => setEditing(null)} onSaved={() => (setEditing(null), reload())} />
    </>
  );
}

function UserDialog({ user, self, defaultQuota, onClose, onSaved }: { user: AdminUser | "new" | null; self?: string; defaultQuota: number; onClose: () => void; onSaved: () => void }) {
  const isNew = user === "new";
  const u = user && user !== "new" ? user : null;
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [admin, setAdmin] = useState(false);
  const [quotaMode, setQuotaMode] = useState<"default" | "unlimited" | "custom">("default");
  const [quotaGB, setQuotaGB] = useState(10);
  const [key, setKey] = useState<unknown>(null);
  const toast = useToast();
  if (user !== key) {
    setKey(user);
    setEmail(u?.email ?? "");
    setName(u?.name ?? "");
    setPassword("");
    setAdmin(u?.role === "admin");
    setQuotaMode(!u || u.quotaBytes === -1 ? "default" : u.quotaBytes === 0 ? "unlimited" : "custom");
    setQuotaGB(u && u.quotaBytes > 0 ? Math.round((u.quotaBytes / GB) * 10) / 10 : 10);
  }
  const quotaBytes = quotaMode === "default" ? -1 : quotaMode === "unlimited" ? 0 : Math.round(quotaGB * GB);
  const save = async () => {
    try {
      if (isNew) await post("/api/v1/admin/users", { email, name, password, role: admin ? "admin" : "user", quotaBytes });
      else if (u) await patch(`/api/v1/admin/users/${u.id}`, { name, role: admin ? "admin" : "user", quotaBytes, ...(password ? { password } : {}) });
      toast.ok(isNew ? "User created" : "User updated");
      onSaved();
    } catch (e) {
      toast.error(e);
    }
  };
  return (
    <Modal
      open={!!user}
      onClose={onClose}
      title={isNew ? "Add user" : `Edit ${u?.email}`}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            {isNew ? "Create user" : "Save"}
          </button>
        </>
      }
    >
      <form className="form" onSubmit={(e) => (e.preventDefault(), save())}>
        {isNew && (
          <label className="field">
            <span>Email</span>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
          </label>
        )}
        <label className="field">
          <span>Name</span>
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="field">
          <span>{isNew ? "Initial password" : "Reset password (leave empty to keep)"}</span>
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" minLength={8} maxLength={72} required={isNew} />
          {!isNew && <small className="hint">Resetting signs the user out of all devices.</small>}
        </label>
        <Switch checked={admin} onChange={setAdmin} disabled={u?.id === self} label="Administrator" hint="Admins manage users, storage and all links." />
        <fieldset className="field">
          <legend>Storage quota</legend>
          <div className="seg">
            {(["default", "unlimited", "custom"] as const).map((m) => (
              <label key={m} className={quotaMode === m ? "on" : ""}>
                <input type="radio" checked={quotaMode === m} onChange={() => setQuotaMode(m)} />
                {m === "default" ? `Server default (${defaultQuota ? formatBytes(defaultQuota) : "none"})` : m === "unlimited" ? "Unlimited" : "Custom"}
              </label>
            ))}
          </div>
          {quotaMode === "custom" && (
            <label className="inline">
              <input type="number" min={0.1} step={0.1} value={quotaGB} onChange={(e) => setQuotaGB(Number(e.target.value))} aria-label="Quota in GB" /> GB
            </label>
          )}
        </fieldset>
      </form>
    </Modal>
  );
}

function AdminLinks() {
  const { data, error, reload } = useAsync(() => get<{ shares: (Share & { ownerEmail: string })[] }>("/api/v1/admin/shares"), []);
  const toast = useToast();
  const dialogs = useDialogs();
  const [filter, setFilter] = useState<"active" | "all">("active");
  const [q, setQ] = useState("");
  const [stats, setStats] = useState<string | null>(null);
  if (error) return <ErrorBox error={error} onRetry={reload} />;
  if (!data) return <Loading />;
  const needle = q.trim().toLowerCase();
  const list = data.shares.filter((s) => (filter === "all" || s.status === "active") && (!needle || (s.name + " " + s.ownerEmail).toLowerCase().includes(needle)));
  const act = (p: Promise<unknown>, msg: string) => p.then(() => (toast.ok(msg), reload())).catch(toast.error);
  return (
    <>
      <div className="toolbar">
        <div className="chips-row">
          <button className={"chip-btn" + (filter === "active" ? " on" : "")} onClick={() => setFilter("active")}>
            Active
          </button>
          <button className={"chip-btn" + (filter === "all" ? " on" : "")} onClick={() => setFilter("all")}>
            All
          </button>
        </div>
        <label className="search grow-sm">
          <Icon name="search" size={18} />
          <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search links or owners" aria-label="Search links" />
        </label>
      </div>
      <div className="table-scroll">
        <table className="dtable">
          <thead>
            <tr>
              <th>Link</th>
              <th>Owner</th>
              <th>Status</th>
              <th>Usage</th>
              <th>Expires</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {list.length === 0 && (
              <tr>
                <td colSpan={6} className="muted center">
                  No links.
                </td>
              </tr>
            )}
            {list.map((s) => (
              <tr key={s.id}>
                <td>
                  <Icon name={s.kind === "upload" ? "inbox" : "link"} size={15} /> {s.name} {s.hasPassword && <Icon name="lock" size={13} label="Password protected" />}
                </td>
                <td className="small">{s.ownerEmail}</td>
                <td>
                  <StatusChip status={s.status} />
                </td>
                <td className="small">{s.kind === "upload" ? `${s.uploadCount} uploads` : `${s.downloadCount}${s.maxDownloads ? "/" + s.maxDownloads : ""} downloads`}</td>
                <td className="small muted">{s.expiresAt ? relativeTime(s.expiresAt) : "never"}</td>
                <td className="right">
                  <Menu
                    items={[
                      { label: "Analytics", icon: "chart", onClick: () => setStats(s.id) },
                      { label: "Open link", icon: "globe", onClick: () => window.open(shareLink(s), "_blank", "noopener") },
                      !s.revoked && { label: "Revoke", icon: "x", onClick: () => act(post(`/api/v1/admin/shares/${s.id}/revoke`), "Link revoked") },
                      {
                        label: "Delete",
                        icon: "trash",
                        danger: true,
                        onClick: async () => {
                          if (await dialogs.confirm(`Delete “${s.name}”?`, "The link stops working immediately and its analytics are removed. The owner's files are not deleted.", "Delete", true))
                            act(del(`/api/v1/admin/shares/${s.id}`), "Link deleted");
                        },
                      },
                    ]}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <LinkAnalyticsDialog shareId={stats} onClose={() => setStats(null)} />
    </>
  );
}

function AdminDevices() {
  const { data, error, reload } = useAsync(() => get<{ devices: Device[] }>("/api/v1/admin/devices"), []);
  const toast = useToast();
  if (error) return <ErrorBox error={error} onRetry={reload} />;
  if (!data) return <Loading />;
  return (
    <div className="table-scroll">
      <table className="dtable">
        <thead>
          <tr>
            <th>Device</th>
            <th>Owner</th>
            <th>Last active</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {data.devices.length === 0 && (
            <tr>
              <td colSpan={4} className="muted center">
                No devices registered.
              </td>
            </tr>
          )}
          {data.devices.map((d) => (
            <tr key={d.id}>
              <td>
                <strong>{d.name}</strong>
                <div className="muted small">
                  {d.platform} {d.appVersion}
                </div>
              </td>
              <td className="small">{d.userEmail}</td>
              <td className="small muted">{relativeTime(d.lastSeen)}</td>
              <td className="right">
                <button className="btn sm danger" onClick={() => del(`/api/v1/admin/devices/${d.id}`).then(() => (toast.ok("Device revoked"), reload())).catch(toast.error)}>
                  Revoke
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

interface AuditEvent {
  id: string;
  at: number;
  userId: string;
  email: string;
  action: string;
  target: string;
  ip: string;
  detail: string;
}

function Audit({ user, clearUser }: { user: { id: string; email: string } | null; clearUser: () => void }) {
  const [q, setQ] = useState("");
  const [applied, setApplied] = useState("");
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [more, setMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const filters = (): Record<string, string> => ({ ...(applied ? { q: applied } : {}), ...(user ? { user: user.id } : {}) });
  const load = async (before?: number) => {
    setLoading(true);
    try {
      const qs = new URLSearchParams({ limit: "200", ...filters(), ...(before ? { before: String(before) } : {}) });
      const r = await get<{ events: AuditEvent[]; more: boolean }>(`/api/v1/admin/audit?${qs}`);
      setEvents((prev) => (before ? [...prev, ...r.events] : r.events));
      setMore(r.more);
      setError(null);
    } catch (e) {
      setError(e);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    load();
  }, [applied, user]); // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <>
      <div className="toolbar">
        <form className="search grow-sm" role="search" onSubmit={(e) => (e.preventDefault(), setApplied(q.trim()))}>
          <Icon name="search" size={18} />
          <input
            type="search"
            value={q}
            onChange={(e) => (setQ(e.target.value), e.target.value === "" && setApplied(""))}
            placeholder="Search action, user, detail or IP"
            aria-label="Search audit log"
          />
        </form>
        {user && (
          <span className="chip info">
            {user.email}
            <button className="icon-btn sm" aria-label="Show all users" onClick={clearUser}>
              <Icon name="x" size={14} />
            </button>
          </span>
        )}
        <a className="btn sm" href={`/api/v1/admin/audit?format=csv&${new URLSearchParams(filters())}`} download>
          <Icon name="download" size={16} /> Export CSV
        </a>
        <button className="btn sm" onClick={() => load()}>
          <Icon name="retry" size={16} /> Refresh
        </button>
      </div>
      {error != null && <ErrorBox error={error} onRetry={() => load()} />}
      {loading && events.length === 0 && <Loading />}
      {!loading && events.length === 0 && error == null && <EmptyState icon="search" title="No matching events" />}
      {events.length > 0 && (
        <div className="table-scroll">
          <table className="dtable">
            <thead>
              <tr>
                <th>When</th>
                <th>Who</th>
                <th>Action</th>
                <th>Detail</th>
                <th>IP</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.id}>
                  <td className="small muted nowrap" title={formatDate(e.at)}>
                    {relativeTime(e.at)}
                  </td>
                  <td className="small">{e.email || "—"}</td>
                  <td>
                    <code>{e.action}</code>
                  </td>
                  <td className="small wrap">{[e.detail, e.target && e.target !== e.detail ? e.target : ""].filter(Boolean).join(" · ")}</td>
                  <td className="small muted">{e.ip}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {more && (
        <button className="btn self-start" disabled={loading} onClick={() => load(events[events.length - 1]?.at)}>
          {loading ? "Loading…" : "Load older events"}
        </button>
      )}
    </>
  );
}

function System() {
  const { data, error, reload } = useAsync(
    () => get<{ version: string; goVersion: string; os: string; arch: string; uptimeSeconds: number; memoryBytes: number; goroutines: number; config: Record<string, unknown>; logs: string[] }>("/api/v1/admin/system"),
    [],
  );
  const toast = useToast();
  if (error) return <ErrorBox error={error} onRetry={reload} />;
  if (!data) return <Loading />;
  const up = data.uptimeSeconds;
  return (
    <>
      <div className="stat-grid">
        <div className="stat">
          <span className="muted small">Version</span>
          <strong>{data.version}</strong>
          <span className="muted small">
            {data.goVersion} · {data.os}/{data.arch}
          </span>
        </div>
        <div className="stat">
          <span className="muted small">Uptime</span>
          <strong>{up > 86400 ? `${Math.floor(up / 86400)}d ${Math.floor((up % 86400) / 3600)}h` : `${Math.floor(up / 3600)}h ${Math.floor((up % 3600) / 60)}m`}</strong>
        </div>
        <div className="stat">
          <span className="muted small">Memory</span>
          <strong>{formatBytes(data.memoryBytes)}</strong>
          <span className="muted small">{data.goroutines} goroutines</span>
        </div>
      </div>
      <section className="panel">
        <header className="panel-head">
          <h2>Configuration</h2>
          <button
            className="btn sm"
            onClick={() =>
              post<Record<string, number>>("/api/v1/admin/cleanup")
                .then((r) => toast.ok(`Cleanup done: ${Object.entries(r).filter(([, v]) => v).map(([k, v]) => `${k} ${v}`).join(", ") || "nothing to clean"}`))
                .catch(toast.error)
            }
          >
            <Icon name="retry" size={16} /> Run cleanup now
          </button>

        </header>
        <dl className="kv">
          {Object.entries(data.config).map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd>{typeof v === "number" && /Bytes$/.test(k) ? (v ? formatBytes(v) : "unlimited") : Array.isArray(v) ? v.join(", ") || "—" : String(v || "—")}</dd>
            </div>
          ))}
        </dl>
        <p className="muted small">Current effective configuration (secrets are never shown). Change it under the Settings tab; server-level options like the port, database and TLS are set with FERRY_* environment variables.</p>
      </section>
      <section className="panel">
        <header className="panel-head">
          <h2>Recent logs</h2>
          <button className="btn sm" onClick={reload}>
            <Icon name="retry" size={16} /> Refresh
          </button>
        </header>
        <pre className="logs">{data.logs.slice().reverse().join("\n") || "No log lines yet."}</pre>
      </section>
    </>
  );
}
