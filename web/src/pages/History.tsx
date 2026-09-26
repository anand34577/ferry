import { useEffect, useState } from "react";
import { del, get, post, type Transfer } from "../lib/api";
import { formatBytes, formatDate, isFinalStatus, relativeTime, statusLabel } from "../lib/format";
import { Icon } from "../components/Icon";
import { EmptyState, ErrorBox, Loading, useAsync, useDialogs, useToast } from "../components/ui";

const FILTERS: [string, string][] = [
  ["all", "All"],
  ["sent", "Sent"],
  ["received", "Received"],
  ["failed", "Failed"],
  ["cancelled", "Cancelled"],
  ["expired", "Expired"],
];

export function History() {
  const [filter, setFilter] = useState("all");
  const query = filter === "sent" || filter === "received" ? `direction=${filter}` : filter === "all" ? "" : `status=${filter === "failed" ? "failed,rejected,interrupted" : filter}`;
  const toast = useToast();
  const PAGE = 100;
  const { data, error, loading, reload } = useAsync(() => get<{ transfers: Transfer[] }>(`/api/v1/transfers?limit=${PAGE}&${query}`), [query]);
  // Older pages, fetched with the created-at cursor of the last row shown.
  const [more, setMore] = useState<Transfer[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  useEffect(() => {
    setMore([]);
    setHasMore((data?.transfers.length ?? 0) === PAGE);
  }, [data]);
  const all = [...(data?.transfers ?? []), ...more];
  const loadMore = async () => {
    setLoadingMore(true);
    try {
      const next = (await get<{ transfers: Transfer[] }>(`/api/v1/transfers?limit=${PAGE}&before=${all[all.length - 1].createdAt}&${query}`)).transfers;
      setMore((m) => [...m, ...next]);
      setHasMore(next.length === PAGE);
    } catch (e) {
      toast.error(e);
    } finally {
      setLoadingMore(false);
    }
  };
  const dialogs = useDialogs();

  const clear = async () => {
    if (!(await dialogs.confirm("Clear history?", "Finished transfers are removed from your history. Your files are not affected.", "Clear"))) return;
    try {
      await post("/api/v1/transfers/clear");
      reload();
    } catch (e) {
      toast.error(e);
    }
  };

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>History</h1>
          <p className="muted">Transfers across all your devices.</p>
        </div>
        <div className="toolbar">
          <button className="btn" onClick={clear}>
            <Icon name="trash" size={18} /> Clear history
          </button>
        </div>
      </header>
      <div className="chips-row" role="tablist" aria-label="Filter">
        {FILTERS.map(([k, l]) => (
          <button key={k} role="tab" aria-selected={filter === k} className={"chip-btn" + (filter === k ? " on" : "")} onClick={() => setFilter(k)}>
            {l}
          </button>
        ))}
      </div>
      {error != null && <ErrorBox error={error} onRetry={reload} />}
      {loading && !data && <Loading />}
      {data && all.length === 0 && <EmptyState icon="clock" title="Nothing here" text="Transfers to and from your devices appear in this list." />}
      <ul className="rows">
        {all.map((t) => (
          <TransferRow
            key={t.id}
            t={t}
            onRemove={async () => {
              try {
                await del(`/api/v1/transfers/${t.id}`);
                reload();
              } catch (e) {
                toast.error(e);
              }
            }}
          />
        ))}
      </ul>
      {hasMore && (
        <button className="btn self-start" onClick={loadMore} disabled={loadingMore}>
          {loadingMore ? "Loading…" : "Show older"}
        </button>
      )}
    </div>
  );
}

export function TransferRow({ t, compact, onRemove }: { t: Transfer; compact?: boolean; onRemove?: () => void }) {
  const peer = t.direction === "sent" ? t.targetDeviceName || t.peer || "Link recipient" : t.sourceDeviceName || t.peer || "Unknown device";
  const bad = ["failed", "rejected", "interrupted", "cancelled", "expired"].includes(t.status);
  return (
    <li className="trow-item">
      <span className={"dir " + t.direction} aria-hidden="true">
        <Icon name={t.direction === "sent" ? "upload" : "download"} size={18} />
      </span>
      <div className="grow">
        <div className="row-title">
          <span className="ellipsis">
            {t.direction === "sent" ? "To " : "From "}
            <strong>{peer}</strong>
          </span>
          <span className="chip method">{t.method === "direct" ? "Direct (LAN)" : t.method === "link" ? "Link" : "Via server"}</span>
        </div>
        <div className="muted small row-meta">
          <span>
            {t.fileCount} file{t.fileCount === 1 ? "" : "s"} · {formatBytes(t.totalBytes)}
          </span>
          <span title={formatDate(t.createdAt)}>{relativeTime(t.createdAt)}</span>
          {!compact && t.error && <span className="err">{t.error}</span>}
        </div>
      </div>
      <span className={"chip " + (t.status === "completed" ? "ok" : bad ? "warn" : "info")}>{statusLabel[t.status]}</span>
      {onRemove && isFinalStatus(t.status) && (
        <button className="icon-btn sm" aria-label="Remove from history" onClick={onRemove}>
          <Icon name="x" size={16} />
        </button>
      )}
    </li>
  );
}
