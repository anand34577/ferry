import { useState } from "react";
import { uploads, useUploads, type UpItem } from "../lib/uploads";
import { formatBytes, formatEta, formatSpeed } from "../lib/format";
import { Icon } from "./Icon";
import { FileBadge, Progress } from "./ui";

const label: Record<UpItem["status"], string> = {
  queued: "Queued",
  uploading: "Uploading",
  paused: "Paused",
  verifying: "Verifying",
  completed: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
  skipped: "Skipped (already exists)",
};

export function TransferTray() {
  const items = useUploads();
  const [collapsed, setCollapsed] = useState(false);
  if (items.length === 0) return null;
  const active = items.filter((i) => ["queued", "uploading", "verifying", "paused"].includes(i.status));
  const total = active.reduce((a, i) => a + i.size, 0);
  const sent = active.reduce((a, i) => a + i.sent, 0);
  const failed = items.filter((i) => i.status === "failed").length;
  const title = active.length ? `Uploading ${active.length} ${active.length === 1 ? "file" : "files"}` : failed ? `${failed} upload(s) need attention` : "Uploads complete";

  return (
    <section className={"tray" + (collapsed ? " collapsed" : "")} aria-label="Transfers">
      <header className="tray-head">
        <button className="tray-title" onClick={() => setCollapsed(!collapsed)} aria-expanded={!collapsed}>
          <Icon name={collapsed ? "chevronRight" : "chevronDown"} size={18} />
          <span>{title}</span>
          {active.length > 0 && <span className="muted">{Math.floor((sent / Math.max(total, 1)) * 100)}%</span>}
        </button>
        {!active.length && (
          <button className="icon-btn sm" aria-label="Clear finished uploads" onClick={() => uploads.clearFinished()}>
            <Icon name="x" size={16} />
          </button>
        )}
      </header>
      {!collapsed && (
        <ul className="tray-list">
          {items.map((it) => (
            <li key={it.id} className={"tray-item " + it.status}>
              <FileBadge name={it.name} size="sm" />
              <div className="tray-main">
                <div className="tray-row">
                  <span className="tray-name" title={it.name}>
                    {it.name}
                  </span>
                  <span className="chip method" title="Transfer method">
                    {it.transferId ? "Via server → device" : "Via server"}
                  </span>
                </div>
                {(it.status === "uploading" || it.status === "paused" || it.status === "verifying") && <Progress value={it.size ? it.sent / it.size : 1} label={it.name} />}
                <div className="tray-meta">
                  <span className={it.status === "failed" ? "err" : ""}>{it.status === "failed" ? it.error : label[it.status]}</span>
                  {it.status === "uploading" && (
                    <span>
                      {formatBytes(it.sent)} / {formatBytes(it.size)} · {formatSpeed(it.speed)} {it.speed > 0 && "· " + formatEta((it.size - it.sent) / it.speed)}
                    </span>
                  )}
                </div>
              </div>
              <div className="tray-actions">
                {it.status === "uploading" && (
                  <button className="icon-btn sm" aria-label={`Pause ${it.name}`} onClick={() => uploads.pause(it.id)}>
                    <Icon name="pause" size={16} />
                  </button>
                )}
                {it.status === "paused" && (
                  <button className="icon-btn sm" aria-label={`Resume ${it.name}`} onClick={() => uploads.resume(it.id)}>
                    <Icon name="play" size={16} />
                  </button>
                )}
                {it.status === "failed" && (
                  <button className="icon-btn sm" aria-label={`Retry ${it.name}`} onClick={() => uploads.retry(it.id)}>
                    <Icon name="retry" size={16} />
                  </button>
                )}
                {["queued", "uploading", "paused", "failed"].includes(it.status) && (
                  <button className="icon-btn sm" aria-label={`Cancel ${it.name}`} onClick={() => uploads.cancel(it.id)}>
                    <Icon name="x" size={16} />
                  </button>
                )}
                {it.status === "completed" && <Icon name="check" size={18} className="ok-icon" />}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
