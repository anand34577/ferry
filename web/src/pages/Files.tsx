import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ApiError, downloadZip, fileUrl, get, patch, post, type FileItem, type Folder, type Listing } from "../lib/api";
import { formatBytes, previewable, relativeTime } from "../lib/format";
import { uploads, type UpOpts } from "../lib/uploads";
import { pref, setPref } from "../lib/auth";
import { Icon } from "../components/Icon";
import { EmptyState, ErrorBox, FileBadge, Loading, Menu, Modal, useDialogs, useToast } from "../components/ui";
import { ShareDialog, type ShareTarget } from "../components/ShareDialog";

type Key = string; // "f:<id>" | "d:<id>"
type Conflict = "keep_both" | "replace" | "skip" | "rename";

export function Files() {
  const [params, setParams] = useSearchParams();
  const folderId = params.get("folder") ?? "";
  const q = params.get("q") ?? "";
  const [sort, setSort] = useState(() => pref("sort", "name"));
  const [listing, setListing] = useState<Listing | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Set<Key>>(new Set());
  const [search, setSearch] = useState(q);
  const [share, setShare] = useState<ShareTarget | null>(null);
  const [preview, setPreview] = useState<FileItem | null>(null);
  const [moving, setMoving] = useState<{ files: string[]; folders: string[] } | null>(null);
  const [conflicts, setConflicts] = useState<{ names: string[]; resolve: (m: Map<string, { c: Conflict; name?: string }> | null) => void } | null>(null);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const folderInput = useRef<HTMLInputElement>(null);
  const toast = useToast();
  const dialogs = useDialogs();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [s, order] = sort.split(":");
      const url = `/api/v1/files?folder=${encodeURIComponent(folderId)}&sort=${s}&order=${order ?? "asc"}${q ? "&q=" + encodeURIComponent(q) : ""}`;
      setListing(await get<Listing>(url));
      setError(null);
    } catch (e) {
      setError(e);
    } finally {
      setLoading(false);
    }
  }, [folderId, q, sort]);

  useEffect(() => {
    load();
    setSelected(new Set());
  }, [load]);

  useEffect(() => {
    if (params.get("upload") === "1") {
      fileInput.current?.click();
      params.delete("upload");
      setParams(params, { replace: true });
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const go = (id: string) => setParams(id ? { folder: id } : {});

  const sharedSet = useMemo(() => new Set(listing?.shared ?? []), [listing]);
  const allKeys = useMemo(() => [...(listing?.folders.map((f) => "d:" + f.id) ?? []), ...(listing?.files.map((f) => "f:" + f.id) ?? [])], [listing]);
  const selFiles = [...selected].filter((k) => k.startsWith("f:")).map((k) => k.slice(2));
  const selFolders = [...selected].filter((k) => k.startsWith("d:")).map((k) => k.slice(2));

  const toggle = (k: Key) =>
    setSelected((s) => {
      const n = new Set(s);
      n.has(k) ? n.delete(k) : n.add(k);
      return n;
    });

  // ---- uploading ----
  const ensureFolder = async (parentId: string, name: string, cache: Map<string, string>): Promise<string> => {
    const key = parentId + "/" + name;
    if (cache.has(key)) return cache.get(key)!;
    try {
      const f = await post<Folder>("/api/v1/folders", { name, parentId });
      cache.set(key, f.id);
      return f.id;
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 409) throw e;
      const l = await get<Listing>(`/api/v1/files?folder=${encodeURIComponent(parentId)}`);
      const found = l.folders.find((x) => x.name === name);
      if (!found) throw e;
      cache.set(key, found.id);
      return found.id;
    }
  };

  const askConflicts = (names: string[]) => new Promise<Map<string, { c: Conflict; name?: string }> | null>((resolve) => setConflicts({ names, resolve }));

  const startUpload = async (files: File[]) => {
    if (!files.length) return;
    const target = folderId;
    const cache = new Map<string, string>();
    const plain = files.filter((f) => !f.webkitRelativePath);
    const nested = files.filter((f) => f.webkitRelativePath);
    const jobs: Promise<unknown>[] = [];
    try {
      if (plain.length) {
        const { conflicts: c } = await post<{ conflicts: string[] }>("/api/v1/files/check", { folderId: target, names: plain.map((f) => f.name) });
        let decisions = new Map<string, { c: Conflict; name?: string }>();
        if (c.length) {
          const d = await askConflicts(c);
          if (!d) return;
          decisions = d;
        }
        for (const f of plain) {
          const d = decisions.get(f.name);
          if (d?.c === "skip") continue;
          const opts: UpOpts = { folderId: target };
          if (d?.c === "replace") opts.conflict = "replace";
          if (d?.c === "rename" && d.name) opts.name = d.name;
          jobs.push(uploads.add(f, opts));
        }
      }
      for (const f of nested) {
        const parts = f.webkitRelativePath.split("/").slice(0, -1);
        let parent = target;
        for (const p of parts) parent = await ensureFolder(parent, p, cache);
        jobs.push(uploads.add(f, { folderId: parent }));
      }
    } catch (e) {
      toast.error(e);
    }
    if (nested.length) load();
    let pending = jobs.length;
    jobs.forEach((j) =>
      j.then(() => {
        pending--;
        if (pending === 0 || pending % 5 === 0) load();
      }),
    );
  };

  // ---- actions ----
  const rename = async (kind: "file" | "folder", id: string, current: string) => {
    const name = await dialogs.prompt(`Rename ${kind}`, "New name", current, "Rename");
    if (!name || name === current) return;
    try {
      await patch(`/api/v1/${kind === "file" ? "files" : "folders"}/${id}`, { name });
      load();
    } catch (e) {
      toast.error(e);
    }
  };

  const remove = async (files: string[], folders: string[]) => {
    const n = files.length + folders.length;
    const ok = await dialogs.confirm(
      `Delete ${n === 1 ? "this item" : n + " items"}?`,
      `${folders.length ? "Folders are deleted with everything inside them. " : ""}Links that include these items will stop working. This can't be undone.`,
      "Delete",
      true,
    );
    if (!ok) return;
    try {
      await post("/api/v1/files/batch", { action: "delete", fileIds: files, folderIds: folders });
      toast.ok(n === 1 ? "Deleted" : `Deleted ${n} items`);
      setSelected(new Set());
      load();
    } catch (e) {
      toast.error(e);
    }
  };

  const newFolder = async () => {
    const name = await dialogs.prompt("New folder", "Folder name", "", "Create");
    if (!name) return;
    try {
      await post("/api/v1/folders", { name, parentId: folderId });
      load();
    } catch (e) {
      toast.error(e);
    }
  };

  const download = (files: string[], folders: string[]) => {
    if (files.length === 1 && folders.length === 0) {
      const a = document.createElement("a");
      a.href = fileUrl(files[0]);
      a.download = "";
      a.click();
    } else downloadZip(files, folders).catch(toast.error);
  };

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragging(false);
    // Dropped folders show up as entries too; skip them by kind, not by size (empty files are valid).
    // dataTransfer.files lines up with the items of kind "file" (text items are not files).
    const isDir = Array.from(e.dataTransfer.items ?? []).filter((it) => it.kind === "file").map((it) => it.webkitGetAsEntry?.()?.isDirectory === true);
    const files = Array.from(e.dataTransfer.files).filter((_, i) => !isDir[i]);
    if (isDir.some(Boolean)) toast.info("To upload a folder, use “Upload folder”.");
    if (files.length) startUpload(files);
  };

  const empty = listing && listing.files.length === 0 && listing.folders.length === 0;

  return (
    <div
      className="page files-page"
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files")) {
          e.preventDefault();
          setDragging(true);
        }
      }}
      onDragLeave={(e) => e.currentTarget === e.target && setDragging(false)}
      onDrop={onDrop}
    >
      <header className="page-head">
        <div className="crumbs-wrap">
          <nav className="crumbs" aria-label="Folder path">
            <button onClick={() => go("")} className={folderId || q ? "" : "current"}>
              My files
            </button>
            {q && (
              <>
                <Icon name="chevronRight" size={16} />
                <span className="current">Search: “{q}”</span>
              </>
            )}
            {!q &&
              listing?.breadcrumbs.map((b, i) => (
                <span key={b.id} className="crumb">
                  <Icon name="chevronRight" size={16} />
                  <button onClick={() => go(b.id)} className={i === listing.breadcrumbs.length - 1 ? "current" : ""}>
                    {b.name}
                  </button>
                </span>
              ))}
          </nav>
        </div>
        <div className="toolbar">
          <form
            className="search"
            role="search"
            onSubmit={(e) => {
              e.preventDefault();
              setParams(search.trim() ? { q: search.trim() } : folderId ? { folder: folderId } : {});
            }}
          >
            <Icon name="search" size={18} />
            <input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search files" aria-label="Search files" type="search" />
          </form>
          <select
            value={sort}
            onChange={(e) => {
              setSort(e.target.value);
              setPref("sort", e.target.value);
            }}
            aria-label="Sort by"
          >
            <option value="name">Name A–Z</option>
            <option value="name:desc">Name Z–A</option>
            <option value="date:desc">Newest first</option>
            <option value="date">Oldest first</option>
            <option value="size:desc">Largest first</option>
            <option value="size">Smallest first</option>
          </select>
          <button className="btn" onClick={newFolder}>
            <Icon name="folderPlus" size={18} /> <span className="hide-sm">New folder</span>
          </button>
          <button className="btn" onClick={() => folderInput.current?.click()}>
            <Icon name="folder" size={18} /> <span className="hide-sm">Upload folder</span>
          </button>
          <button className="btn primary" onClick={() => fileInput.current?.click()}>
            <Icon name="upload" size={18} /> Upload
          </button>
          <input ref={fileInput} type="file" multiple hidden onChange={(e) => (startUpload(Array.from(e.target.files ?? [])), (e.target.value = ""))} />
          <input
            ref={folderInput}
            type="file"
            multiple
            hidden
            // @ts-expect-error non-standard but universally supported attribute
            webkitdirectory=""
            onChange={(e) => (startUpload(Array.from(e.target.files ?? [])), (e.target.value = ""))}
          />
        </div>
      </header>

      {selected.size > 0 && (
        <div className="selbar" role="toolbar" aria-label="Selection actions">
          <span>{selected.size} selected</span>
          <button className="btn sm" onClick={() => setShare({ kind: "download", fileIds: selFiles, folderIds: selFolders })}>
            <Icon name="link" size={16} /> Share
          </button>
          <button className="btn sm" onClick={() => download(selFiles, selFolders)}>
            <Icon name="download" size={16} /> Download
          </button>
          <button className="btn sm" onClick={() => setMoving({ files: selFiles, folders: selFolders })}>
            <Icon name="move" size={16} /> Move
          </button>
          <button className="btn sm danger" onClick={() => remove(selFiles, selFolders)}>
            <Icon name="trash" size={16} /> Delete
          </button>
          <button className="icon-btn sm" aria-label="Clear selection" onClick={() => setSelected(new Set())}>
            <Icon name="x" size={16} />
          </button>
        </div>
      )}

      {error != null && <ErrorBox error={error} onRetry={load} />}
      {loading && !listing && <Loading />}
      {empty && !q && (
        <EmptyState icon="folder" title={folderId ? "This folder is empty" : "You don't have any files yet"} text="Drag files here, or use the buttons below.">
          <button className="btn primary" onClick={() => fileInput.current?.click()}>
            <Icon name="upload" size={18} /> Upload files
          </button>
          <button className="btn" onClick={newFolder}>
            <Icon name="folderPlus" size={18} /> New folder
          </button>
        </EmptyState>
      )}
      {empty && q && <EmptyState icon="search" title="No results" text={`Nothing matches “${q}”.`} />}

      {listing && !empty && (
        <div className="table files-table" role="table" aria-label="Files">
          <div className="thead" role="row">
            <label className="cell check" role="columnheader">
              <input
                type="checkbox"
                aria-label="Select all"
                checked={selected.size > 0 && selected.size === allKeys.length}
                onChange={(e) => setSelected(e.target.checked ? new Set(allKeys) : new Set())}
              />
            </label>
            <span className="cell grow" role="columnheader">
              Name
            </span>
            <span className="cell size hide-sm" role="columnheader">
              Size
            </span>
            <span className="cell date hide-sm" role="columnheader">
              Modified
            </span>
            <span className="cell act" role="columnheader">
              <span className="sr-only">Actions</span>
            </span>
          </div>
          {listing.folders.map((f) => (
            <div key={f.id} className={"trow" + (selected.has("d:" + f.id) ? " sel" : "")} role="row" onDoubleClick={() => go(f.id)}>
              <label className="cell check">
                <input type="checkbox" aria-label={`Select ${f.name}`} checked={selected.has("d:" + f.id)} onChange={() => toggle("d:" + f.id)} />
              </label>
              <button className="cell grow name-cell" onClick={() => go(f.id)}>
                <FileBadge name={f.name} folder />
                <span className="ellipsis">{f.name}</span>
                {sharedSet.has(f.id) && <Icon name="link" size={15} className="shared-ico" label="Shared" />}
              </button>
              <span className="cell size hide-sm muted">—</span>
              <span className="cell date hide-sm muted">{relativeTime(f.updatedAt)}</span>
              <span className="cell act">
                <Menu
                  items={[
                    { label: "Share", icon: "link", onClick: () => setShare({ kind: "download", fileIds: [], folderIds: [f.id], name: f.name }) },
                    { label: "Download as zip", icon: "download", onClick: () => download([], [f.id]) },
                    { label: "Rename", icon: "edit", onClick: () => rename("folder", f.id, f.name) },
                    { label: "Move", icon: "move", onClick: () => setMoving({ files: [], folders: [f.id] }) },
                    { label: "Delete", icon: "trash", danger: true, onClick: () => remove([], [f.id]) },
                  ]}
                />
              </span>
            </div>
          ))}
          {listing.files.map((f) => (
            <div key={f.id} className={"trow" + (selected.has("f:" + f.id) ? " sel" : "")} role="row">
              <label className="cell check">
                <input type="checkbox" aria-label={`Select ${f.name}`} checked={selected.has("f:" + f.id)} onChange={() => toggle("f:" + f.id)} />
              </label>
              <button className="cell grow name-cell" onClick={() => (previewable(f.mime) ? setPreview(f) : toggle("f:" + f.id))} title={f.name}>
                <FileBadge name={f.name} mime={f.mime} />
                <span className="ellipsis">{f.name}</span>
                {sharedSet.has(f.id) && <Icon name="link" size={15} className="shared-ico" label="Shared" />}
              </button>
              <span className="cell size hide-sm muted">{formatBytes(f.size)}</span>
              <span className="cell date hide-sm muted">{relativeTime(f.updatedAt)}</span>
              <span className="cell act">
                <Menu
                  items={[
                    previewable(f.mime) && { label: "Preview", icon: "eye", onClick: () => setPreview(f) },
                    { label: "Download", icon: "download", onClick: () => download([f.id], []) },
                    { label: "Share", icon: "link", onClick: () => setShare({ kind: "download", fileIds: [f.id], folderIds: [], name: f.name }) },
                    { label: "Rename", icon: "edit", onClick: () => rename("file", f.id, f.name) },
                    { label: "Move", icon: "move", onClick: () => setMoving({ files: [f.id], folders: [] }) },
                    { label: "Delete", icon: "trash", danger: true, onClick: () => remove([f.id], []) },
                  ]}
                />
              </span>
            </div>
          ))}
        </div>
      )}

      {dragging && (
        <div className="drop-overlay" aria-hidden="true">
          <Icon name="upload" size={40} />
          <p>Drop files to upload{listing?.breadcrumbs.length ? ` to “${listing.breadcrumbs[listing.breadcrumbs.length - 1].name}”` : ""}</p>
        </div>
      )}

      <ShareDialog target={share} onClose={() => setShare(null)} onSaved={() => load()} />
      <PreviewModal file={preview} onClose={() => setPreview(null)} />
      <MoveDialog
        items={moving}
        onClose={() => setMoving(null)}
        onMoved={() => {
          setMoving(null);
          setSelected(new Set());
          load();
        }}
      />
      <ConflictDialog req={conflicts} onDone={() => setConflicts(null)} />
    </div>
  );
}

function PreviewModal({ file, onClose }: { file: FileItem | null; onClose: () => void }) {
  const [text, setText] = useState<string | null>(null);
  const kind = file?.mime.split("/")[0];
  useEffect(() => {
    setText(null);
    if (file && file.mime.startsWith("text/")) {
      fetch(fileUrl(file.id, true), { headers: { Range: "bytes=0-262143" } })
        .then((r) => r.text())
        .then(setText)
        .catch(() => setText("Couldn't load a preview."));
    }
  }, [file]);
  return (
    <Modal
      open={!!file}
      onClose={onClose}
      title={file?.name ?? ""}
      wide
      footer={
        file && (
          <a className="btn primary" href={fileUrl(file.id)} download>
            <Icon name="download" size={18} /> Download ({formatBytes(file.size)})
          </a>
        )
      }
    >
      {file && (
        <div className="preview">
          {kind === "image" && <img src={fileUrl(file.id, true)} alt={file.name} />}
          {kind === "video" && <video src={fileUrl(file.id, true)} controls autoPlay />}
          {kind === "audio" && <audio src={fileUrl(file.id, true)} controls autoPlay />}
          {file.mime === "application/pdf" && <iframe src={fileUrl(file.id, true)} title={file.name} />}
          {kind === "text" && <pre>{text ?? "Loading…"}</pre>}
          <p className="muted small mono">SHA-256 {file.sha256}</p>
        </div>
      )}
    </Modal>
  );
}

function MoveDialog({ items, onClose, onMoved }: { items: { files: string[]; folders: string[] } | null; onClose: () => void; onMoved: () => void }) {
  const [at, setAt] = useState("");
  const [listing, setListing] = useState<Listing | null>(null);
  const toast = useToast();
  useEffect(() => {
    if (items) setAt("");
  }, [items]);
  useEffect(() => {
    if (items) get<Listing>(`/api/v1/files?folder=${encodeURIComponent(at)}`).then(setListing).catch(toast.error);
  }, [at, items]); // eslint-disable-line react-hooks/exhaustive-deps
  const move = async () => {
    if (!items) return;
    try {
      const r = await post<{ ok: boolean; message?: string }>("/api/v1/files/batch", { action: "move", fileIds: items.files, folderIds: items.folders, targetFolderId: at });
      if (!r.ok && r.message) toast.info(r.message);
      else toast.ok("Moved");
      onMoved();
    } catch (e) {
      toast.error(e);
    }
  };
  const moving = new Set(items?.folders ?? []);
  return (
    <Modal
      open={!!items}
      onClose={onClose}
      title="Move to…"
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={move}>
            Move here
          </button>
        </>
      }
    >
      <nav className="crumbs small" aria-label="Destination path">
        <button onClick={() => setAt("")}>My files</button>
        {listing?.breadcrumbs.map((b) => (
          <span key={b.id} className="crumb">
            <Icon name="chevronRight" size={14} />
            <button onClick={() => setAt(b.id)}>{b.name}</button>
          </span>
        ))}
      </nav>
      <ul className="picker">
        {listing?.folders.filter((f) => !moving.has(f.id)).length === 0 && <li className="muted">No subfolders</li>}
        {listing?.folders
          .filter((f) => !moving.has(f.id))
          .map((f) => (
            <li key={f.id}>
              <button onClick={() => setAt(f.id)}>
                <Icon name="folder" size={18} /> {f.name}
                <Icon name="chevronRight" size={16} className="push" />
              </button>
            </li>
          ))}
      </ul>
    </Modal>
  );
}

function ConflictDialog({ req, onDone }: { req: { names: string[]; resolve: (m: Map<string, { c: Conflict; name?: string }> | null) => void } | null; onDone: () => void }) {
  const [choice, setChoice] = useState<Record<string, Conflict>>({});
  const [names, setNames] = useState<Record<string, string>>({});
  useEffect(() => {
    if (req) {
      setChoice(Object.fromEntries(req.names.map((n) => [n, "keep_both" as Conflict])));
      setNames(Object.fromEntries(req.names.map((n) => [n, n])));
    }
  }, [req]);
  const finish = (ok: boolean) => {
    if (!req) return;
    req.resolve(ok ? new Map(req.names.map((n) => [n, { c: choice[n], name: choice[n] === "rename" ? names[n] : undefined }])) : null);
    onDone();
  };
  const all = (c: Conflict) => setChoice(Object.fromEntries((req?.names ?? []).map((n) => [n, c])));
  return (
    <Modal
      open={!!req}
      onClose={() => finish(false)}
      title={req?.names.length === 1 ? "A file with this name already exists" : `${req?.names.length} files already exist here`}
      footer={
        <>
          <button className="btn" onClick={() => finish(false)}>
            Cancel upload
          </button>
          <button className="btn primary" onClick={() => finish(true)}>
            Continue
          </button>
        </>
      }
    >
      <div className="seg-row">
        <span className="muted small">Apply to all:</span>
        <button className="btn sm" onClick={() => all("keep_both")}>Keep both</button>
        <button className="btn sm" onClick={() => all("replace")}>Replace</button>
        <button className="btn sm" onClick={() => all("skip")}>Skip</button>
      </div>
      <ul className="conflicts">
        {req?.names.map((n) => (
          <li key={n}>
            <span className="ellipsis" title={n}>
              {n}
            </span>
            <select value={choice[n]} onChange={(e) => setChoice({ ...choice, [n]: e.target.value as Conflict })} aria-label={`What to do with ${n}`}>
              <option value="keep_both">Keep both</option>
              <option value="replace">Replace existing</option>
              <option value="skip">Skip</option>
              <option value="rename">Rename…</option>
            </select>
            {choice[n] === "rename" && <input value={names[n]} onChange={(e) => setNames({ ...names, [n]: e.target.value })} aria-label={`New name for ${n}`} />}
          </li>
        ))}
      </ul>
    </Modal>
  );
}

