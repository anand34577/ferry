import { useEffect, useState } from "react";
import { patch, post, type Share } from "../lib/api";
import { EXPIRY_OPTIONS, formatDate, relativeTime, serverNow } from "../lib/format";
import { useAuth, pref, setPref } from "../lib/auth";
import { CopyField, Modal, QR, Switch, useToast } from "./ui";
import { Icon } from "./Icon";

export type ShareTarget =
  | { kind: "download"; fileIds: string[]; folderIds: string[]; name?: string }
  | { kind: "upload"; folderId?: string; name?: string }
  | { edit: Share };

const MB = 1024 * 1024;

export function ShareDialog({ target, onClose, onSaved }: { target: ShareTarget | null; onClose: () => void; onSaved?: (s: Share) => void }) {
  const { info } = useAuth();
  const toast = useToast();
  const edit = target && "edit" in target ? target.edit : null;
  const kind = edit ? edit.kind : target && "kind" in target ? target.kind : "download";

  const [name, setName] = useState("");
  const [message, setMessage] = useState("");
  const [expiry, setExpiry] = useState<number>(7 * 86400); // seconds; -1 = keep / custom
  const [customExpiry, setCustomExpiry] = useState("");
  const [usePassword, setUsePassword] = useState(false);
  const [password, setPassword] = useState("");
  const [limitMode, setLimitMode] = useState<"none" | "once" | "n">("none");
  const [limitN, setLimitN] = useState(5);
  const [allowPreview, setAllowPreview] = useState(true);
  const [allowDownload, setAllowDownload] = useState(true);
  const [requireAuth, setRequireAuth] = useState(false);
  const [maxFileMB, setMaxFileMB] = useState(0);
  const [maxFiles, setMaxFiles] = useState(0);
  const [types, setTypes] = useState("");
  const [allowList, setAllowList] = useState(false);
  const [allowDelete, setAllowDelete] = useState(true);
  const [notify, setNotify] = useState(false);
  const [saving, setSaving] = useState(false);
  const [result, setResult] = useState<Share | null>(null);
  const [emailTo, setEmailTo] = useState("");

  useEffect(() => {
    if (!target) return;
    setResult(null);
    setPassword("");
    if (edit) {
      setName(edit.name);
      setMessage(edit.message);
      setExpiry(-1);
      setCustomExpiry(edit.expiresAt ? toLocalInput(edit.expiresAt) : "");
      setUsePassword(edit.hasPassword);
      setLimitMode(edit.maxDownloads === 0 ? "none" : edit.maxDownloads === 1 ? "once" : "n");
      setLimitN(edit.maxDownloads > 1 ? edit.maxDownloads : 5);
      setAllowPreview(edit.allowPreview);
      setAllowDownload(edit.allowDownload);
      setRequireAuth(edit.requireAuth);
      setMaxFileMB(Math.round(edit.maxFileBytes / MB));
      setMaxFiles(edit.maxFiles);
      setTypes(edit.allowedTypes);
      setAllowList(edit.allowList);
      setAllowDelete(edit.allowDelete);
      setNotify(edit.notify);
    } else {
      setName("name" in target && target.name ? target.name : "");
      setMessage("");
      setExpiry(pref("defaultExpiry", 7 * 86400));
      setCustomExpiry("");
      setUsePassword(false);
      setLimitMode("none");
      setAllowPreview(true);
      setAllowDownload(true);
      setRequireAuth(info?.publicSharing === false);
      setMaxFileMB(0);
      setMaxFiles(0);
      setTypes("");
      setAllowList(false);
      setAllowDelete(true);
      setNotify(false);
    }
  }, [target]); // eslint-disable-line react-hooks/exhaustive-deps

  const save = async () => {
    const body: Record<string, unknown> = {
      name: name.trim() || undefined,
      message,
      maxDownloads: limitMode === "none" ? 0 : limitMode === "once" ? 1 : Math.max(2, limitN),
      allowPreview,
      allowDownload,
      requireAuth,
      maxFileBytes: maxFileMB > 0 ? maxFileMB * MB : 0,
      maxFiles,
      allowedTypes: types,
      allowList,
      allowDelete,
      notify,
    };
    if (expiry >= 0) body.expiresIn = expiry;
    else if (customExpiry) {
      const t = new Date(customExpiry).getTime();
      if (!(t > Date.now())) return toast.error(new Error("Choose an expiry time in the future."));
      body.expiresIn = Math.round((t - Date.now()) / 1000); // relative → immune to clock differences
    } else body.expiresIn = 0;
    if (!usePassword) body.password = "";
    else if (password) body.password = password;
    else if (!edit?.hasPassword) return toast.error(new Error("Enter a password or turn password protection off."));
    setSaving(true);
    try {
      let s: Share;
      if (edit) s = await patch<Share>(`/api/v1/shares/${edit.id}`, body);
      else if (target && "kind" in target && target.kind === "download")
        s = await post<Share>("/api/v1/shares", { ...body, kind: "download", fileIds: target.fileIds, folderIds: target.folderIds });
      else s = await post<Share>("/api/v1/shares", { ...body, kind: "upload", folderId: target && "kind" in target && target.kind === "upload" ? target.folderId : undefined });
      if (!edit && expiry >= 0) setPref("defaultExpiry", expiry);
      onSaved?.(s);
      if (edit) {
        toast.ok("Link updated");
        onClose();
      } else setResult(s);
    } catch (e) {
      toast.error(e);
    } finally {
      setSaving(false);
    }
  };

  const sendEmail = async () => {
    if (!result) return;
    try {
      await post(`/api/v1/shares/${result.id}/email`, { to: emailTo });
      toast.ok("Email sent");
      setEmailTo("");
    } catch (e) {
      toast.error(e);
    }
  };

  const emailOn = info?.capabilities.includes("email");
  const title = result ? "Link ready" : edit ? "Edit link" : kind === "upload" ? "Create upload link" : "Create share link";

  return (
    <Modal
      open={!!target}
      onClose={onClose}
      title={title}
      footer={
        result ? (
          <button className="btn primary" onClick={onClose}>
            Done
          </button>
        ) : (
          <>
            <button className="btn" onClick={onClose}>
              Cancel
            </button>
            <button className="btn primary" onClick={save} disabled={saving}>
              {saving ? "Saving…" : edit ? "Save changes" : "Create link"}
            </button>
          </>
        )
      }
    >
      {result ? (
        <div className="share-result">
          <QR value={result.url} size={180} />
          <p className="muted center">
            {result.kind === "upload" ? "Anyone with this link can upload files to you" : "Anyone with this link can download"}
            {result.hasPassword ? " (password required)" : ""}
            {result.expiresAt ? ` · expires ${relativeTime(result.expiresAt)}` : ""}
            {result.maxDownloads === 1 ? " · one-time download" : ""}
          </p>
          <CopyField value={result.url} />
          {emailOn && (
            <form
              className="inline-form"
              onSubmit={(e) => {
                e.preventDefault();
                sendEmail();
              }}
            >
              <input type="email" placeholder="Email the link to…" value={emailTo} onChange={(e) => setEmailTo(e.target.value)} required aria-label="Recipient email" />
              <button className="btn" type="submit">
                <Icon name="mail" size={18} /> Send
              </button>
            </form>
          )}
        </div>
      ) : (
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          <label className="field">
            <span>Link name</span>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder={kind === "upload" ? "e.g. Tax documents 2026" : "Shown to recipients"} maxLength={200} />
          </label>
          <div className="grid2">
            <label className="field">
              <span>Expires</span>
              <select value={expiry} onChange={(e) => setExpiry(Number(e.target.value))}>
                {edit && <option value={-1}>{edit.expiresAt ? (customExpiry ? "Custom date" : "Keep current") : "Never (current)"}</option>}
                {!edit && <option value={-1}>Custom date…</option>}
                {EXPIRY_OPTIONS.map(([l, v]) => (
                  <option key={v} value={v}>
                    {l}
                  </option>
                ))}
              </select>
            </label>
            {expiry === -1 && (
              <label className="field">
                <span>Expiry date</span>
                <input type="datetime-local" value={customExpiry} min={toLocalInput(serverNow() + 60000)} onChange={(e) => setCustomExpiry(e.target.value)} />
              </label>
            )}
          </div>
          {edit && edit.expiresAt > 0 && expiry === -1 && <p className="hint">Currently expires {formatDate(edit.expiresAt)} ({relativeTime(edit.expiresAt)}).</p>}

          <Switch checked={usePassword} onChange={setUsePassword} label="Password protection" hint="Recipients must enter a password. It's never part of the link." />
          {usePassword && (
            <label className="field">
              <span>{edit?.hasPassword ? "New password (leave empty to keep current)" : "Password"}</span>
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" minLength={4} maxLength={72} />
            </label>
          )}

          {kind === "download" ? (
            <>
              <fieldset className="field">
                <legend>Download limit</legend>
                <div className="seg">
                  {(["none", "once", "n"] as const).map((m) => (
                    <label key={m} className={limitMode === m ? "on" : ""}>
                      <input type="radio" name="limit" checked={limitMode === m} onChange={() => setLimitMode(m)} />
                      {m === "none" ? "Unlimited" : m === "once" ? "One-time" : "Limited"}
                    </label>
                  ))}
                </div>
                {limitMode === "n" && (
                  <input type="number" min={2} max={100000} value={limitN} onChange={(e) => setLimitN(Number(e.target.value))} aria-label="Maximum downloads" />
                )}
                {limitMode !== "none" && <small className="hint">Each recipient session counts once, even if a download is resumed. Preview is disabled for limited links.</small>}
              </fieldset>
              <Switch checked={allowDownload} onChange={setAllowDownload} label="Allow downloads" hint="Turn off for preview-only links." />
              <Switch checked={allowPreview && limitMode === "none"} disabled={limitMode !== "none"} onChange={setAllowPreview} label="Allow preview in browser" />
            </>
          ) : (
            <>
              <div className="grid2">
                <label className="field">
                  <span>Max file size (MB, 0 = no limit)</span>
                  <input type="number" min={0} value={maxFileMB} onChange={(e) => setMaxFileMB(Number(e.target.value))} />
                </label>
                <label className="field">
                  <span>Max number of files (0 = no limit)</span>
                  <input type="number" min={0} value={maxFiles} onChange={(e) => setMaxFiles(Number(e.target.value))} />
                </label>
              </div>
              <label className="field">
                <span>Allowed file types</span>
                <input value={types} onChange={(e) => setTypes(e.target.value)} placeholder="e.g. pdf, jpg, png — empty allows all" />
              </label>
              <Switch checked={allowList} onChange={setAllowList} label="Show received files to uploaders" hint="Uploaders can see names of all files sent through this link." />
              <Switch checked={allowDelete} onChange={setAllowDelete} label="Let uploaders delete their own files" />
              {emailOn && <Switch checked={notify} onChange={setNotify} label="Email me when files arrive" />}
            </>
          )}
          <Switch checked={requireAuth} onChange={setRequireAuth} disabled={info?.publicSharing === false} label="Require sign-in" hint="Only people with an account on this server can open the link." />
          <label className="field">
            <span>Message to recipients (optional)</span>
            <textarea value={message} onChange={(e) => setMessage(e.target.value)} rows={2} maxLength={2000} />
          </label>
        </form>
      )}
    </Modal>
  );
}

function toLocalInput(ms: number) {
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
