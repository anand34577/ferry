import { useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, get, patch, post, type Device, type Folder, type Listing, type Share, type Transfer, shareLink } from "../lib/api";
import { formatBytes, isFinalStatus, relativeTime, statusLabel } from "../lib/format";
import { uploads, useUploads } from "../lib/uploads";
import { Icon } from "../components/Icon";
import { CopyField, FileBadge, Progress, QR, useAsync, useToast } from "../components/ui";
import { ShareDialog, type ShareTarget } from "../components/ShareDialog";

// The "Send" flow: pick files → choose a link or one of my devices → watch it arrive.
export function Send() {
  const [params] = useSearchParams();
  const [files, setFiles] = useState<File[]>([]);
  const [target, setTarget] = useState<string>(params.get("device") ?? "link");
  const [phase, setPhase] = useState<"pick" | "sending" | "done">("pick");
  const [transfer, setTransfer] = useState<Transfer | null>(null);
  const [share, setShare] = useState<ShareTarget | null>(null);
  const [created, setCreated] = useState<Share | null>(null);
  const [ids, setIds] = useState<string[]>([]);
  const [batch, setBatch] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const toast = useToast();
  const navigate = useNavigate();
  const devices = useAsync(() => get<{ devices: Device[] }>("/api/v1/devices"), []);
  const items = useUploads();
  const total = files.reduce((a, f) => a + f.size, 0);

  // Poll the device transfer until it finishes.
  useEffect(() => {
    if (!transfer || isFinalStatus(transfer.status)) return;
    const t = setInterval(() => get<Transfer>(`/api/v1/transfers/${transfer.id}`).then(setTransfer).catch(() => {}), 3000);
    return () => clearInterval(t);
  }, [transfer]);

  // Copy the FileList now: it's live, and resetting the input's value (to allow re-picking) empties it
  // before a deferred state update would read it.
  const add = (list: FileList | null) => {
    const picked = Array.from(list ?? []);
    if (picked.length) setFiles((f) => [...f, ...picked]);
  };

  const sentFolder = async (): Promise<string> => {
    try {
      return (await post<Folder>("/api/v1/folders", { name: "Sent", parentId: "" })).id;
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const l = await get<Listing>("/api/v1/files?folder=");
        return l.folders.find((f) => f.name === "Sent")!.id;
      }
      throw e;
    }
  };

  const send = async () => {
    setPhase("sending");
    const b = "send-" + Date.now();
    setBatch(b);
    try {
      if (target === "link") {
        const folder = await sentFolder();
        const res = await Promise.all(files.map((f) => uploads.add(f, { folderId: folder, batch: b })));
        const ok = res.filter((r) => r.status === "completed" && r.fileId).map((r) => r.fileId!);
        if (!ok.length) throw new Error("No files were uploaded.");
        if (ok.length < files.length) toast.info(`${files.length - ok.length} file(s) failed to upload — the link contains the rest.`);
        setIds(ok);
        setShare({ kind: "download", fileIds: ok, folderIds: [], name: files.length === 1 ? files[0].name : undefined });
      } else {
        const t = await post<Transfer>("/api/v1/transfers", { targetDeviceId: target, fileCount: files.length, totalBytes: total });
        setTransfer(t);
        const res = await Promise.all(files.map((f) => uploads.add(f, { transferId: t.id, batch: b })));
        const failed = res.filter((r) => r.status !== "completed" && r.status !== "skipped").length;
        if (failed === files.length) {
          setTransfer(await patch<Transfer>(`/api/v1/transfers/${t.id}`, { status: "failed", error: "Upload to the server failed" }));
        } else {
          setTransfer(await patch<Transfer>(`/api/v1/transfers/${t.id}`, { status: "waiting", fileCount: files.length - failed, error: failed ? `${failed} file(s) failed to upload` : "" }));
        }
      }
      setPhase("done");
    } catch (e) {
      toast.error(e);
      setPhase("pick");
    }
  };

  const reset = () => {
    setFiles([]);
    setTransfer(null);
    setCreated(null);
    setIds([]);
    setPhase("pick");
  };

  const device = devices.data?.devices.find((d) => d.id === target);
  const mine = items.filter((i) => batch && i.batch === batch);

  return (
    <div className="page narrow-page">
      <header className="page-head">
        <div>
          <h1>Send files</h1>
          <p className="muted">Share with anyone using a link, or push to one of your devices.</p>
        </div>
      </header>

      {phase === "pick" && (
        <>
          <label
            className={"dropzone" + (files.length ? " compact" : "")}
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              add(e.dataTransfer.files);
            }}
          >
            <input ref={input} type="file" multiple onChange={(e) => (add(e.target.files), (e.target.value = ""))} />
            <Icon name="upload" size={28} />
            <strong>{files.length ? "Add more files" : "Choose files or drop them here"}</strong>
            <span className="muted small">Any type, any size your server allows</span>
          </label>

          {files.length > 0 && (
            <ul className="pick-list">
              {files.map((f, i) => (
                <li key={i}>
                  <FileBadge name={f.name} mime={f.type} size="sm" />
                  <span className="ellipsis">{f.name}</span>
                  <span className="muted small">{formatBytes(f.size)}</span>
                  <button className="icon-btn sm" aria-label={`Remove ${f.name}`} onClick={() => setFiles(files.filter((_, j) => j !== i))}>
                    <Icon name="x" size={16} />
                  </button>
                </li>
              ))}
              <li className="muted small total">
                {files.length} file{files.length === 1 ? "" : "s"} · {formatBytes(total)}
              </li>
            </ul>
          )}

          <h2 className="section-title">Send to</h2>
          <div className="targets" role="radiogroup" aria-label="Destination">
            <label className={"target" + (target === "link" ? " on" : "")}>
              <input type="radio" name="target" checked={target === "link"} onChange={() => setTarget("link")} />
              <Icon name="link" size={22} />
              <span>
                <strong>Create a link</strong>
                <small>Anyone with the link can download — no app needed</small>
              </span>
            </label>
            {devices.data?.devices.map((d) => (
              <label key={d.id} className={"target" + (target === d.id ? " on" : "")}>
                <input type="radio" name="target" checked={target === d.id} onChange={() => setTarget(d.id)} />
                <Icon name={d.platform === "android" ? "phone" : "laptop"} size={22} />
                <span>
                  <strong>{d.name}</strong>
                  <small>
                    {d.online ? "Ready to receive" : `Last seen ${relativeTime(d.lastSeen)} — delivered when it's back online`} · via server
                  </small>
                </span>
                <span className={"dot " + (d.online ? "on" : "")} />
              </label>
            ))}
          </div>
          <div className="sticky-actions">
            <button className="btn primary big" disabled={!files.length} onClick={send}>
              <Icon name="send" size={18} /> {target === "link" ? "Upload & create link" : `Send to ${device?.name ?? "device"}`}
            </button>
          </div>
        </>
      )}

      {phase !== "pick" && (
        <section className="panel">
          <h2>{target === "link" ? "Uploading…" : `Sending to ${device?.name}`}</h2>
          <ul className="rows">
            {mine.map((i) => (
              <li key={i.id} className="trow-item">
                <FileBadge name={i.name} size="sm" />
                <div className="grow">
                  <div className="row-title">
                    <span className="ellipsis">{i.name}</span>
                    <span className="muted small">{i.status === "failed" ? i.error : i.status}</span>
                  </div>
                  <Progress value={i.size ? i.sent / i.size : 1} label={i.name} />
                </div>
              </li>
            ))}
          </ul>
          {transfer && (
            <div className={"transfer-status " + transfer.status}>
              <Icon name={transfer.status === "completed" ? "check" : isFinalStatus(transfer.status) ? "alert" : "clock"} />
              <div>
                <strong>{statusLabel[transfer.status]}</strong>
                <p className="muted small">
                  {transfer.status === "waiting" && "Files are on your server. They'll be delivered as soon as the device opens Ferry and accepts."}
                  {transfer.status === "transferring" && "The device is downloading the files…"}
                  {transfer.status === "completed" && "The device received and verified all files."}
                  {transfer.status === "rejected" && "The device declined the transfer. The temporary files were deleted."}
                  {transfer.status === "failed" && (transfer.error || "The transfer failed. You can try again.")}
                  {transfer.status === "expired" && "Nobody picked up the files in time."}
                </p>
              </div>
            </div>
          )}
          {phase === "done" && (
            <div className="row-actions">
              <button className="btn" onClick={reset}>
                Send more
              </button>
              {created && (
                <button className="btn" onClick={() => navigate("/links")}>
                  Manage links
                </button>
              )}
            </div>
          )}
          {created && (
            <div className="share-result">
              <QR value={shareLink(created)} size={160} />
              <CopyField value={shareLink(created)} />
            </div>
          )}
        </section>
      )}
      <ShareDialog
        target={share}
        onClose={() => {
          setShare(null);
          if (!created && ids.length) toast.info("Your files are saved in the “Sent” folder — you can share them any time from Files.");
        }}
        onSaved={setCreated}
      />
    </div>
  );
}
