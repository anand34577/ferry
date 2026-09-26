import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useEffect, useState } from "react";
import { Icon, type IconName } from "./Icon";
import { useAuth } from "../lib/auth";
import { get, post, type Usage } from "../lib/api";
import { formatBytes } from "../lib/format";
import { TransferTray } from "./TransferTray";
import { useUploads } from "../lib/uploads";

const NAV: [string, string, IconName][] = [
  ["/", "Home", "home"],
  ["/files", "Files", "folder"],
  ["/links", "Links", "link"],
  ["/devices", "Devices", "phone"],
  ["/history", "History", "clock"],
];

export function Layout() {
  const { user, info, logout, me } = useAuth();
  const navigate = useNavigate();
  const [usage, setUsage] = useState<Usage | null>(null);
  const items = useUploads();
  const completed = items.filter((i) => i.status === "completed").length;

  useEffect(() => {
    get<Usage>("/api/v1/me/usage").then(setUsage).catch(() => {});
  }, [completed]);

  const nav = user?.role === "admin" ? [...NAV, ["/admin", "Admin", "shield"] as [string, string, IconName]] : NAV;
  const pct = usage && usage.quotaBytes > 0 ? usage.usedBytes / usage.quotaBytes : 0;

  return (
    <div className="shell">
      <a className="skip" href="#main">Skip to content</a>
      {me?.impersonator && (
        <div className="imp-bar" role="status">
          <Icon name="shield" size={18} />
          <span>
            Signed in as <strong>{user?.email}</strong> by {me.impersonator}
          </span>
          <button className="btn sm" onClick={() => post("/api/v1/auth/return").finally(() => window.location.assign("/admin"))}>
            Return to admin
          </button>
        </div>
      )}
      <aside className="sidebar">
        <div className="brand">
          <span className="logo" aria-hidden="true" />
          <span>{info?.siteName ?? "Ferry"}</span>
        </div>
        <button className="btn primary send-btn" onClick={() => navigate("/send")}>
          <Icon name="send" size={18} /> Send
        </button>
        <nav aria-label="Main">
          {nav.map(([to, label, icon]) => (
            <NavLink key={to} to={to} end={to === "/"} className="nav-item">
              <Icon name={icon} />
              <span>{label}</span>
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-foot">
          {usage && (
            <div className="usage" title={`${usage.fileCount} files`}>
              <div className="usage-row">
                <span>Storage</span>
                <span>
                  {formatBytes(usage.usedBytes)}
                  {usage.quotaBytes > 0 ? ` of ${formatBytes(usage.quotaBytes)}` : ""}
                </span>
              </div>
              {usage.quotaBytes > 0 && (
                <div className={"progress " + (pct > 0.9 ? "danger" : "")}>
                  <i style={{ width: Math.min(100, pct * 100) + "%" }} />
                </div>
              )}
            </div>
          )}
          <NavLink to="/settings" className="nav-item">
            <Icon name="gear" />
            <span className="user-line">
              <span>{user?.name}</span>
              <small>{user?.email}</small>
            </span>
          </NavLink>
          <button className="nav-item as-btn" onClick={() => logout().then(() => navigate("/login"))}>
            <Icon name="logout" />
            <span>Sign out</span>
          </button>
        </div>
      </aside>
      <main id="main" className="main" tabIndex={-1}>
        <Outlet />
      </main>
      <nav className="bottom-nav" aria-label="Main">
        {[...NAV.slice(0, 2), ["/send", "Send", "send"] as [string, string, IconName], NAV[2], ["/settings", "More", "menu"] as [string, string, IconName]].map(([to, label, icon]) => (
          <NavLink key={to} to={to} end={to === "/"} className={"bn-item" + (to === "/send" ? " bn-send" : "")}>
            <Icon name={icon} />
            <span>{label}</span>
          </NavLink>
        ))}
      </nav>
      <TransferTray />
    </div>
  );
}
