import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { get, type Share } from "../lib/api";
import { relativeTime } from "../lib/format";
import { Icon } from "../components/Icon";
import { CopyField, QR, StatusChip, useAsync } from "../components/ui";
import { ShareDialog, type ShareTarget } from "../components/ShareDialog";

export function Receive() {
  const { data, reload } = useAsync(() => get<{ shares: Share[] }>("/api/v1/shares?kind=upload"), []);
  const [target, setTarget] = useState<ShareTarget | null>(null);
  const navigate = useNavigate();
  const active = data?.shares.filter((s) => s.status === "active") ?? [];
  const latest = active[0];

  return (
    <div className="page narrow-page">
      <header className="page-head">
        <div>
          <h1>Receive files</h1>
          <p className="muted">Anyone can send you files through an upload link — they only need a browser.</p>
        </div>
      </header>
      <section className="panel receive-hero">
        {latest ? (
          <>
            <QR value={latest.url} size={180} />
            <div className="grow">
              <h2>{latest.name}</h2>
              <p className="muted small">
                {latest.uploadCount} file{latest.uploadCount === 1 ? "" : "s"} received · {latest.expiresAt ? "expires " + relativeTime(latest.expiresAt) : "no expiry"}
              </p>
              <CopyField value={latest.url} />
              <div className="row-actions">
                <button className="btn" onClick={() => navigate(`/files?folder=${latest.folderId}`)}>
                  <Icon name="folder" size={18} /> Open received files
                </button>
                <button className="btn" onClick={() => setTarget({ kind: "upload" })}>
                  <Icon name="plus" size={18} /> New upload link
                </button>
              </div>
            </div>
          </>
        ) : (
          <div className="center grow">
            <div className="empty-icon big">
              <Icon name="inbox" size={32} />
            </div>
            <h2>Create your upload link</h2>
            <p className="muted">Set limits like file size, types and expiry. Files land in a folder of your choice.</p>
            <button className="btn primary big" onClick={() => setTarget({ kind: "upload" })}>
              <Icon name="plus" size={18} /> Create upload link
            </button>
          </div>
        )}
      </section>
      <section className="panel">
        <h2>From your phone</h2>
        <p className="muted">
          In the Ferry Android app, use <strong>Send → My devices</strong> to upload to this server, or open <strong>Receive</strong> on the phone to get files directly from nearby devices over Wi-Fi — no internet needed.
        </p>
      </section>
      {active.length > 1 && (
        <section className="panel">
          <h2>Other active upload links</h2>
          <ul className="mini-list">
            {active.slice(1).map((s) => (
              <li key={s.id}>
                <Icon name="inbox" size={18} />
                <span className="ellipsis">{s.name}</span>
                <StatusChip status={s.status} />
                <span className="muted small">{s.uploadCount} received</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      <ShareDialog target={target} onClose={() => setTarget(null)} onSaved={reload} />
    </div>
  );
}
