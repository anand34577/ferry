import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { del, get, patch, post, type Share } from "../lib/api";
import { formatBytes, formatDate, relativeTime } from "../lib/format";
import { Icon } from "../components/Icon";
import { CopyField, EmptyState, ErrorBox, Loading, Menu, Modal, QR, StatusChip, useAsync, useDialogs, useToast } from "../components/ui";
import { ShareDialog, type ShareTarget } from "../components/ShareDialog";

export function Links() {
  const [tab, setTab] = useState<"download" | "upload">("download");
  const { data, error, loading, reload } = useAsync(() => get<{ shares: Share[] }>("/api/v1/shares"), []);
  const [edit, setEdit] = useState<ShareTarget | null>(null);
  const [qr, setQr] = useState<Share | null>(null);
  const toast = useToast();
  const dialogs = useDialogs();
  const navigate = useNavigate();
  const list = data?.shares.filter((s) => s.kind === tab) ?? [];

  const act = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn();
      toast.ok(okMsg);
      reload();
    } catch (e) {
      toast.error(e);
    }
  };
  const copy = async (s: Share) => {
    try {
      await navigator.clipboard.writeText(s.url);
      toast.ok("Link copied");
    } catch {
      setQr(s);
    }
  };

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>Links</h1>
          <p className="muted">Everything you've shared, and links people can upload to.</p>
        </div>
        <div className="toolbar">
          <button className="btn" onClick={() => setEdit({ kind: "upload" })}>
            <Icon name="inbox" size={18} /> New upload link
          </button>
          <button className="btn primary" onClick={() => navigate("/send")}>
            <Icon name="link" size={18} /> Share files
          </button>
        </div>
      </header>
      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={tab === "download"} onClick={() => setTab("download")}>
          Share links <span className="count">{data?.shares.filter((s) => s.kind === "download").length ?? ""}</span>
        </button>
        <button role="tab" aria-selected={tab === "upload"} onClick={() => setTab("upload")}>
          Upload links <span className="count">{data?.shares.filter((s) => s.kind === "upload").length ?? ""}</span>
        </button>
      </div>
      {error != null && <ErrorBox error={error} onRetry={reload} />}
      {loading && !data && <Loading />}
      {data && list.length === 0 && (
        tab === "download" ? (
          <EmptyState icon="link" title="No share links yet" text="Select files and choose Share to create a link.">
            <button className="btn primary" onClick={() => navigate("/send")}>
              <Icon name="send" size={18} /> Share something
            </button>
          </EmptyState>
        ) : (
          <EmptyState icon="inbox" title="No upload links yet" text="An upload link lets anyone send you files — no account needed.">
            <button className="btn primary" onClick={() => setEdit({ kind: "upload" })}>
              <Icon name="plus" size={18} /> Create upload link
            </button>
          </EmptyState>
        )
      )}
      <ul className="cards">
        {list.map((s) => (
          <li key={s.id} className={"link-card" + (s.status !== "active" ? " dim" : "")}>
            <div className="link-main">
              <div className="link-title">
                <Icon name={s.kind === "upload" ? "inbox" : "link"} size={18} />
                <strong className="ellipsis">{s.name}</strong>
                <StatusChip status={s.status} />
                {s.hasPassword && <Icon name="lock" size={15} label="Password protected" />}
                {s.requireAuth && <Icon name="users" size={15} label="Sign-in required" />}
              </div>
              <div className="link-meta muted small">
                {s.kind === "download" ? (
                  <>
                    <span>{s.itemCount} item{s.itemCount === 1 ? "" : "s"}</span>
                    <span>
                      {s.downloadCount} download{s.downloadCount === 1 ? "" : "s"}
                      {s.maxDownloads > 0 ? ` of ${s.maxDownloads}` : ""}
                    </span>
                    {s.maxDownloads === 1 && <span>One-time</span>}
                  </>
                ) : (
                  <>
                    <span>
                      {s.uploadCount} file{s.uploadCount === 1 ? "" : "s"} received{s.maxFiles ? ` of ${s.maxFiles}` : ""}
                    </span>
                    <span>{formatBytes(s.uploadedBytes)}</span>
                  </>
                )}
                <span title={s.expiresAt ? formatDate(s.expiresAt) : undefined}>
                  {s.expiresAt ? (s.status === "expired" ? "expired " : "expires ") + relativeTime(s.expiresAt) : "no expiry"}
                </span>
                <span>created {relativeTime(s.createdAt)}</span>
              </div>
            </div>
            <div className="link-actions">
              <button className="btn sm" onClick={() => copy(s)} disabled={s.status === "revoked"}>
                <Icon name="copy" size={16} /> Copy
              </button>
              <button className="icon-btn" aria-label="Show QR code" onClick={() => setQr(s)}>
                <Icon name="qr" size={18} />
              </button>
              <Menu
                items={[
                  { label: "Edit", icon: "edit", onClick: () => setEdit({ edit: s }) },
                  s.kind === "upload" && { label: "Open destination folder", icon: "folder", onClick: () => navigate(`/files?folder=${s.folderId}`) },
                  s.revoked
                    ? { label: "Enable", icon: "check", onClick: () => act(() => patch(`/api/v1/shares/${s.id}`, { revoked: false }), "Link enabled") }
                    : { label: "Disable", icon: "x", onClick: () => act(() => patch(`/api/v1/shares/${s.id}`, { revoked: true }), "Link disabled") },
                  {
                    label: "New link address",
                    icon: "retry",
                    onClick: async () => {
                      if (await dialogs.confirm("Generate a new address?", "The current link will stop working immediately. Anyone who needs access will need the new link.", "Generate"))
                        act(() => post(`/api/v1/shares/${s.id}/regenerate`), "New link generated");
                    },
                  },
                  {
                    label: "Delete",
                    icon: "trash",
                    danger: true,
                    onClick: async () => {
                      if (await dialogs.confirm("Delete this link?", s.kind === "upload" ? "People can no longer upload. Files already received stay in your folder." : "The link stops working immediately. Your files are not deleted.", "Delete", true))
                        act(() => del(`/api/v1/shares/${s.id}`), "Link deleted");
                    },
                  },
                ]}
              />
            </div>
          </li>
        ))}
      </ul>
      <ShareDialog target={edit} onClose={() => setEdit(null)} onSaved={reload} />
      <Modal open={!!qr} onClose={() => setQr(null)} title={qr?.name ?? ""}>
        {qr && (
          <div className="share-result">
            <QR value={qr.url} size={220} />
            <CopyField value={qr.url} />
          </div>
        )}
      </Modal>
    </div>
  );
}
