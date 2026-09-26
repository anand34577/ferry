import { useState } from "react";
import { get, type LinkAnalytics as Data, type LinkEvent } from "../lib/api";
import { describeAgent, formatDate, relativeTime } from "../lib/format";
import { ErrorBox, Loading, Modal, useAsync } from "./ui";

const KIND_LABEL: Record<LinkEvent["kind"], string> = {
  view: "Opened",
  download: "Downloaded",
  preview: "Previewed",
  upload: "Uploaded",
  password_failed: "Wrong password",
};

/** Analytics for one link: totals, daily activity, visitors' browsers and the recent access log. */
export function LinkAnalyticsDialog({ shareId, onClose }: { shareId: string | null; onClose: () => void }) {
  return (
    <Modal open={!!shareId} onClose={onClose} title="Link analytics" wide>
      {shareId && <Body id={shareId} />}
    </Modal>
  );
}

function Body({ id }: { id: string }) {
  const [days, setDays] = useState(30);
  const { data, error, reload } = useAsync(() => get<Data>(`/api/v1/shares/${id}/analytics?days=${days}`), [id, days]);
  if (error) return <ErrorBox error={error} onRetry={reload} />;
  if (!data) return <Loading />;
  const upload = data.share.kind === "upload";
  const t = data.totals;
  const tiles: [string, number][] = upload
    ? [["Page opens", t.view], ["Files received", t.upload], ["Unique visitors", data.visitors], ["Wrong passwords", t.password_failed]]
    : [["Page opens", t.view], ["Downloads", t.download], ["Previews", t.preview], ["Unique visitors", data.visitors], ["Wrong passwords", t.password_failed]];

  // Daily buckets (local days) for the window, oldest first.
  const dayMs = 86400000;
  const start = new Date();
  start.setHours(0, 0, 0, 0);
  const buckets = Array.from({ length: days }, (_, i) => ({ at: start.getTime() - (days - 1 - i) * dayMs, n: 0 }));
  for (const e of data.events) {
    const i = Math.floor((e.at - buckets[0].at) / dayMs);
    if (i >= 0 && i < days) buckets[i].n++;
  }
  const max = Math.max(1, ...buckets.map((b) => b.n));
  const browsers = count(data.events.filter((e) => e.kind !== "upload" || e.userAgent).map((e) => describeAgent(e.userAgent)));
  const referrers = count(data.events.filter((e) => e.referrer).map((e) => hostOf(e.referrer)));

  return (
    <div className="stack-lg">
      <p className="muted small">
        <strong>{data.share.name}</strong> · created {relativeTime(data.share.createdAt)}. Totals are all-time; the chart and lists cover the selected period.
      </p>
      <div className="stat-grid compact">
        {tiles.map(([l, v]) => (
          <div key={l} className="stat">
            <span className="muted small">{l}</span>
            <strong>{v.toLocaleString()}</strong>
          </div>
        ))}
      </div>
      <section>
        <header className="panel-head">
          <h3>Activity per day</h3>
          <select value={days} onChange={(e) => setDays(Number(e.target.value))} aria-label="Period">
            <option value={7}>Last 7 days</option>
            <option value={30}>Last 30 days</option>
            <option value={90}>Last 90 days</option>
          </select>
        </header>
        <div className="bars" role="img" aria-label={`Link activity per day over the last ${days} days, peak ${max}`}>
          {buckets.map((b) => (
            <div key={b.at} className="bar-col" title={`${new Date(b.at).toLocaleDateString()}: ${b.n} event${b.n === 1 ? "" : "s"}`}>
              <i style={{ height: b.n ? `${Math.max(4, (b.n / max) * 100)}%` : 0 }} />
            </div>
          ))}
        </div>
        <div className="bars-axis muted small">
          <span>{new Date(buckets[0].at).toLocaleDateString(undefined, { month: "short", day: "numeric" })}</span>
          <span>peak {max}/day</span>
          <span>Today</span>
        </div>
      </section>
      {(browsers.length > 0 || referrers.length > 0) && (
        <div className="grid2">
          <Breakdown title="Browsers" rows={browsers} />
          <Breakdown title="Came from" rows={referrers.length ? referrers : [["Direct / not shared", data.events.length]]} />
        </div>
      )}
      <section>
        <h3>Recent activity</h3>
        {data.events.length === 0 ? (
          <p className="muted">No activity in this period yet.</p>
        ) : (
          <div className="table-scroll">
            <table className="dtable">
              <thead>
                <tr>
                  <th>When</th>
                  <th>What</th>
                  <th>Visitor</th>
                </tr>
              </thead>
              <tbody>
                {data.events.slice(0, 100).map((e, i) => (
                  <tr key={i}>
                    <td className="small muted" title={formatDate(e.at)}>
                      {relativeTime(e.at)}
                    </td>
                    <td className="small">
                      <span className={"chip " + (e.kind === "password_failed" ? "warn" : e.kind === "view" ? "muted" : "info")}>{KIND_LABEL[e.kind] ?? e.kind}</span> {e.detail}
                    </td>
                    <td className="small muted">
                      {e.ip || "—"}
                      {e.userAgent && <div>{describeAgent(e.userAgent)}</div>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function Breakdown({ title, rows }: { title: string; rows: [string, number][] }) {
  const total = rows.reduce((a, [, n]) => a + n, 0) || 1;
  return (
    <section>
      <h3>{title}</h3>
      <ul className="breakdown">
        {rows.slice(0, 6).map(([k, n]) => (
          <li key={k}>
            <span className="ellipsis">{k}</span>
            <span className="muted small">{Math.round((n / total) * 100)}%</span>
            <i style={{ width: `${(n / total) * 100}%` }} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function count(xs: string[]): [string, number][] {
  const m = new Map<string, number>();
  for (const x of xs) m.set(x, (m.get(x) ?? 0) + 1);
  return [...m.entries()].sort((a, b) => b[1] - a[1]);
}

function hostOf(u: string) {
  try {
    return new URL(u).host;
  } catch {
    return u;
  }
}
