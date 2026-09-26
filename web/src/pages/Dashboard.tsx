import { Link, useNavigate } from "react-router-dom";
import { get, type Device, type Share, type Transfer, type Usage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { formatBytes, relativeTime } from "../lib/format";
import { Icon, type IconName } from "../components/Icon";
import { EmptyState, useAsync } from "../components/ui";
import { TransferRow } from "./History";

export function Dashboard() {
  const { user } = useAuth();
  const navigate = useNavigate();
  const data = useAsync(
    () =>
      Promise.all([
        get<{ transfers: Transfer[] }>("/api/v1/transfers?limit=5"),
        get<{ shares: Share[] }>("/api/v1/shares"),
        get<{ devices: Device[] }>("/api/v1/devices"),
        get<Usage>("/api/v1/me/usage"),
      ]),
    [],
  );
  const [transfers, shares, devices, usage] = data.data ?? [];
  const activeShares = shares?.shares.filter((s) => s.status === "active") ?? [];
  const hour = new Date().getHours();
  const greet = hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";

  const actions: [string, string, IconName, string][] = [
    ["Send", "Share files with a link or push them to your device", "send", "/send"],
    ["Receive", "Get a link anyone can upload files to", "inbox", "/receive"],
    ["Upload", "Store files on your server", "upload", "/files?upload=1"],
    ["My links", "Manage expiry, passwords and downloads", "link", "/links"],
  ];

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>
            {greet}, {user?.name.split(" ")[0]}
          </h1>
          <p className="muted">What would you like to do?</p>
        </div>
      </header>

      <div className="quick">
        {actions.map(([title, text, icon, to], i) => (
          <button key={title} className={"quick-card" + (i === 0 ? " accent" : "")} onClick={() => navigate(to)}>
            <span className="quick-icon">
              <Icon name={icon} size={22} />
            </span>
            <span className="quick-title">{title}</span>
            <span className="quick-text">{text}</span>
          </button>
        ))}
      </div>

      <div className="dash-grid">
        <section className="panel span2">
          <header className="panel-head">
            <h2>Recent transfers</h2>
            <Link to="/history">View all</Link>
          </header>
          {transfers && transfers.transfers.length === 0 && (
            <EmptyState icon="clock" title="No transfers yet" text="Files you send to devices or receive show up here.">
              <button className="btn primary" onClick={() => navigate("/send")}>
                <Icon name="send" size={18} /> Send something
              </button>
            </EmptyState>
          )}
          <ul className="rows">{transfers?.transfers.map((t) => <TransferRow key={t.id} t={t} compact />)}</ul>
        </section>

        <section className="panel">
          <header className="panel-head">
            <h2>Storage</h2>
          </header>
          {usage && (
            <div className="stat-big">
              <strong>{formatBytes(usage.usedBytes)}</strong>
              <span className="muted">{usage.quotaBytes > 0 ? `of ${formatBytes(usage.quotaBytes)} used` : "used · no limit"}</span>
              {usage.quotaBytes > 0 && (
                <div className={"progress " + (usage.usedBytes / usage.quotaBytes > 0.9 ? "danger" : "")}>
                  <i style={{ width: Math.min(100, (usage.usedBytes / usage.quotaBytes) * 100) + "%" }} />
                </div>
              )}
              <span className="muted small">{usage.fileCount} files</span>
            </div>
          )}
        </section>

        <section className="panel">
          <header className="panel-head">
            <h2>Active links</h2>
            <Link to="/links">Manage</Link>
          </header>
          {shares && activeShares.length === 0 && <p className="muted">No active links. Share a file to create one.</p>}
          <ul className="mini-list">
            {activeShares.slice(0, 5).map((s) => (
              <li key={s.id}>
                <Icon name={s.kind === "upload" ? "inbox" : "link"} size={18} />
                <span className="ellipsis">{s.name}</span>
                <span className="muted small">{s.expiresAt ? relativeTime(s.expiresAt) : "no expiry"}</span>
              </li>
            ))}
          </ul>
        </section>

        <section className="panel span2">
          <header className="panel-head">
            <h2>My devices</h2>
            <Link to="/devices">Manage</Link>
          </header>
          {devices && devices.devices.length === 0 && (
            <p className="muted">
              No devices yet. Install the Ferry Android app and sign in to send files between your phone and this server — or directly between phones on the same Wi-Fi.
            </p>
          )}
          <ul className="mini-list">
            {devices?.devices.slice(0, 5).map((d) => (
              <li key={d.id}>
                <Icon name={d.platform === "android" ? "phone" : "laptop"} size={18} />
                <span className="ellipsis">{d.name}</span>
                <span className={"dot " + (d.online ? "on" : "")} aria-label={d.online ? "online" : "offline"} />
                <span className="muted small">{d.online ? "Online" : "seen " + relativeTime(d.lastSeen)}</span>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </div>
  );
}
