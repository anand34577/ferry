import { app, type Transfer } from "../api";
import { useApp } from "../App";
import { Icon, type IconName } from "../Icon";
import { Empty, Progress, useToast } from "../ui";
import { ago, bytes, eta, plural, speed } from "../format";

const FINAL = ["completed", "failed", "cancelled", "rejected"];

const LABEL: Record<string, string> = {
  created: "Preparing",
  waiting: "Waiting",
  connecting: "Connecting",
  transferring: "Transferring",
  verifying: "Verifying",
  completed: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
  rejected: "Declined",
  interrupted: "Interrupted",
};

export function TransfersPage() {
  const { state } = useApp();
  const list = state.transfers ?? [];
  const active = list.filter((t) => !FINAL.includes(t.status) || (t.status === "failed" && t.canRetry));
  const history = list.filter((t) => !active.includes(t));
  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>Transfers</h1>
          <p className="muted">Everything you've sent and received on this PC.</p>
        </div>
        {history.length > 0 && (
          <button className="btn sm ghost" onClick={() => app.ClearHistory()}>
            Clear history
          </button>
        )}
      </header>
      {!list.length ? (
        <div className="card">
          <Empty icon="clock" title="No transfers yet" text="Files you send or receive show up here, with their progress." />
        </div>
      ) : (
        <>
          {active.length > 0 && (
            <section className="card">
              <div className="card-head">
                <h2>In progress</h2>
              </div>
              <div className="list">
                {active.map((t) => (
                  <Row key={t.id} t={t} />
                ))}
              </div>
            </section>
          )}
          {history.length > 0 && (
            <section className="card">
              <div className="card-head">
                <h2>History</h2>
              </div>
              <div className="list">
                {history.map((t) => (
                  <Row key={t.id} t={t} />
                ))}
              </div>
            </section>
          )}
        </>
      )}
    </div>
  );
}

function Row({ t }: { t: Transfer }) {
  const toast = useToast();
  const files = t.files ?? [];
  const done = FINAL.includes(t.status);
  const bad = t.status === "failed" || t.status === "rejected";
  const title = files.length === 1 ? files[0].name : plural(files.length, "file");
  const icon: IconName = t.method === "link" ? "link" : t.method === "server" ? "server" : t.direction === "received" ? "download" : "send";
  const to = t.direction === "received" ? `from ${t.peer}` : t.method === "link" ? "as a link" : `to ${t.peer}`;
  const via = t.method === "direct" ? "Direct" : t.method === "server" ? "Via server" : "Link";
  const frac = t.total > 0 ? t.done / t.total : 0;
  const running = t.status === "transferring" && !t.paused;
  return (
    <div className="t-item">
      <div className={"t-ico" + (bad ? " bad" : t.direction === "received" ? " in" : "")}>
        <Icon name={icon} size={19} />
      </div>
      <div className="grow">
        <div className="t-title">
          <strong className="ellipsis" title={files.map((f) => f.name).join("\n")}>
            {title}
          </strong>
          <span className="muted small ellipsis">{to}</span>
        </div>
        {!done && <Progress value={frac} indeterminate={["connecting", "created"].includes(t.status) || (t.status === "waiting" && frac === 0)} />}
        <div className="t-meta">
          <span style={bad ? { color: "var(--err)" } : t.status === "completed" ? { color: "var(--ok)" } : undefined}>
            {t.paused ? "Paused" : LABEL[t.status] ?? t.status}
          </span>
          <span>
            {done ? bytes(t.total) : `${bytes(t.done)} of ${bytes(t.total)}`}
          </span>
          {running && t.speed > 0 && <span>{speed(t.speed)}</span>}
          {running && t.speed > 0 && <span>{eta(t.total - t.done, t.speed)}</span>}
          <span>{via}</span>
          {done && <span>{ago(t.updatedAt)}</span>}
        </div>
        {(t.error || t.note) && (
          <div className="t-note" style={{ color: t.error && bad ? "var(--err)" : "var(--muted)" }}>
            {t.error || t.note}
          </div>
        )}
        {t.shareUrl && (
          <div className="row" style={{ marginTop: 6 }}>
            <input type="text" readOnly value={t.shareUrl} className="mono" onFocus={(e) => e.target.select()} style={{ minHeight: 32 }} />
            <button className="btn sm" onClick={() => app.CopyText(t.shareUrl!).then(() => toast.ok("Link copied"), toast.error)}>
              <Icon name="copy" size={15} /> Copy
            </button>
          </div>
        )}
      </div>
      <div className="actions">
        {!done && t.canPause && !t.paused && t.status !== "verifying" && (
          <button className="btn icon ghost" title="Pause" aria-label="Pause" onClick={() => app.Pause(t.id)}>
            <Icon name="pause" size={17} />
          </button>
        )}
        {t.paused && (
          <button className="btn icon ghost" title="Resume" aria-label="Resume" onClick={() => app.Resume(t.id)}>
            <Icon name="play" size={17} />
          </button>
        )}
        {t.status === "failed" && t.canRetry && (
          <button className="btn icon ghost" title="Retry" aria-label="Retry" onClick={() => app.Retry(t.id)}>
            <Icon name="retry" size={17} />
          </button>
        )}
        {t.direction === "received" && t.status === "completed" && (
          <button className="btn icon ghost" title="Show in folder" aria-label="Show in folder" onClick={() => app.ShowInFolder(t.id).catch(toast.error)}>
            <Icon name="folder" size={17} />
          </button>
        )}
        {!done ? (
          <button className="btn icon ghost" title="Cancel" aria-label="Cancel" onClick={() => app.Cancel(t.id)}>
            <Icon name="x" size={17} />
          </button>
        ) : (
          <button className="btn icon ghost" title="Remove from the list" aria-label="Remove from the list" onClick={() => app.Dismiss(t.id)}>
            <Icon name="trash" size={16} />
          </button>
        )}
      </div>
    </div>
  );
}
