// Upload manager: resumable tus uploads with pause/resume/cancel/retry, 3 in parallel,
// end-to-end SHA-256 verification, and automatic resume after reloads or network loss.
import * as tus from "tus-js-client";
import { createSHA256 } from "hash-wasm";
import { useSyncExternalStore } from "react";
import { CLIENT_HEADER, del } from "./api";

export type UpStatus = "queued" | "uploading" | "paused" | "verifying" | "completed" | "failed" | "cancelled" | "skipped";

export interface UpItem {
  id: string;
  name: string;
  size: number;
  sent: number;
  speed: number;
  status: UpStatus;
  error?: string;
  fileId?: string;
  folderId: string;
  transferId?: string;
  batch?: string;
}

export interface UpOpts {
  folderId?: string;
  transferId?: string;
  conflict?: "keep_both" | "replace" | "skip";
  name?: string;
  /** Groups items so a page can show only its own uploads. */
  batch?: string;
}

interface Job {
  file: File;
  opts: UpOpts;
  upload?: tus.Upload;
  hash?: Promise<string>;
  hashAbort?: { aborted: boolean };
  done: (it: UpItem) => void;
  lastT: number;
  lastSent: number;
}

const PARALLEL = 3;
let seq = 0;

class UploadManager {
  items: UpItem[] = [];
  private jobs = new Map<string, Job>();
  private listeners = new Set<() => void>();

  constructor() {
    window.addEventListener("online", () => {
      for (const it of this.items) if (it.status === "failed" && it.error?.startsWith("Connection")) this.retry(it.id);
    });
    window.addEventListener("beforeunload", (e) => {
      if (this.items.some((i) => i.status === "uploading" || i.status === "queued")) e.preventDefault();
    });
  }

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };
  snapshot = () => this.items;

  private set(id: string, patch: Partial<UpItem>) {
    this.items = this.items.map((i) => (i.id === id ? { ...i, ...patch } : i));
    this.listeners.forEach((f) => f());
  }
  private get(id: string) {
    return this.items.find((i) => i.id === id)!;
  }

  /** Queue a file; resolves when it reaches a final state. */
  add(file: File, opts: UpOpts = {}): Promise<UpItem> {
    const id = `up${++seq}`;
    const item: UpItem = { id, name: opts.name ?? file.name, size: file.size, sent: 0, speed: 0, status: "queued", folderId: opts.folderId ?? "", transferId: opts.transferId, batch: opts.batch };
    this.items = [...this.items, item];
    this.listeners.forEach((f) => f());
    return new Promise((done) => {
      this.jobs.set(id, { file, opts, done, lastT: 0, lastSent: 0 });
      this.pump();
    });
  }

  private pump() {
    const running = this.items.filter((i) => i.status === "uploading" || i.status === "verifying").length;
    let slots = PARALLEL - running;
    for (const it of this.items) {
      if (slots <= 0) break;
      if (it.status === "queued") {
        slots--;
        this.start(it.id);
      }
    }
  }

  private finish(id: string, patch: Partial<UpItem>) {
    this.set(id, { ...patch, speed: 0 });
    const job = this.jobs.get(id);
    if (job && ["completed", "cancelled", "skipped"].includes(this.get(id).status)) {
      job.done(this.get(id));
      this.jobs.delete(id);
    } else if (job && this.get(id).status === "failed") {
      job.done(this.get(id)); // caller learns of the failure; the item stays retryable in the tray
    }
    this.pump();
  }

  private async start(id: string) {
    const job = this.jobs.get(id);
    if (!job) return;
    const { file, opts } = job;
    this.set(id, { status: "uploading", error: undefined });
    // Hash in parallel with the upload; compared with the server's hash at the end.
    if (!job.hash) {
      job.hashAbort = { aborted: false };
      job.hash = sha256File(file, job.hashAbort);
    }
    const meta: Record<string, string> = { filename: opts.name ?? file.name, filetype: file.type || "application/octet-stream" };
    if (opts.conflict === "replace") {
      // A replacement overwrites the existing file on completion, so the server must verify the
      // checksum *before* committing (it discards a corrupt upload and keeps the original).
      this.set(id, { status: "verifying" });
      const sha = await job.hash.catch(() => "");
      if (this.get(id)?.status !== "verifying") return; // cancelled meanwhile
      if (sha) meta.sha256 = sha;
      this.set(id, { status: "uploading" });
    }
    if (opts.folderId) meta.folderId = opts.folderId;
    if (opts.transferId) meta.transferId = opts.transferId;
    if (opts.conflict) meta.conflict = opts.conflict;
    job.lastT = performance.now();
    job.lastSent = 0;
    const upload = new tus.Upload(file, {
      endpoint: "/api/v1/uploads",
      chunkSize: 16 * 1024 * 1024,
      retryDelays: [0, 1000, 3000, 5000, 10000, 20000, 30000, 60000],
      metadata: meta,
      headers: { "X-Requested-With": "ferry", "X-Ferry-Client": CLIENT_HEADER },
      storeFingerprintForResuming: true,
      removeFingerprintOnSuccess: true,
      fingerprint: async (f) => ["ferry", f.name, f.size, (f as File).lastModified, opts.folderId ?? "", opts.transferId ?? "", opts.name ?? ""].join("|"),
      onShouldRetry: (err) => {
        const s = (err as tus.DetailedError).originalResponse?.getStatus();
        return !s || s >= 500 || s === 409 || s === 423 || s === 429;
      },
      onProgress: (sent) => {
        const now = performance.now();
        const dt = (now - job.lastT) / 1000;
        if (dt >= 0.5) {
          const inst = (sent - job.lastSent) / dt;
          const prev = this.get(id).speed;
          this.set(id, { sent, speed: prev ? prev * 0.6 + inst * 0.4 : inst });
          job.lastT = now;
          job.lastSent = sent;
        } else {
          this.set(id, { sent });
        }
      },
      onError: (err) => {
        const res = (err as tus.DetailedError).originalResponse;
        let msg = "Connection lost. The upload will resume when you retry or when you're back online.";
        if (res) {
          try {
            msg = JSON.parse(res.getBody()).error.message;
          } catch {
            msg = `Upload failed (server returned ${res.getStatus()}).`;
          }
        }
        this.finish(id, { status: "failed", error: msg });
      },
      onSuccess: async (payload) => {
        const res = payload.lastResponse;
        const fileId = res.getHeader("Ferry-File-Id") ?? undefined;
        const serverSha = res.getHeader("Ferry-Sha256");
        if (res.getHeader("Ferry-Skipped")) {
          job.hashAbort!.aborted = true;
          this.finish(id, { status: "skipped", sent: file.size, fileId });
          return;
        }
        this.set(id, { status: "verifying", sent: file.size, fileId });
        try {
          const local = await job.hash!;
          if (serverSha && local && serverSha !== local) {
            // Never delete a replaced file: the original content is already gone (and replacements are verified server-side).
            if (fileId && opts.conflict !== "replace") await del(`/api/v1/files/${fileId}`).catch(() => {});
            this.finish(id, { status: "failed", error: "The file was corrupted in transit (checksum mismatch). Please retry.", fileId: undefined });
            job.hash = undefined;
            return;
          }
        } catch {
          /* hashing unavailable (e.g. file became unreadable) — the server-side hash still protects integrity */
        }
        this.finish(id, { status: "completed" });
      },
    });
    job.upload = upload;
    upload.findPreviousUploads().then((prev) => {
      if (prev.length) upload.resumeFromPreviousUpload(prev[0]);
      if (this.get(id).status === "uploading") upload.start();
    });
  }

  pause(id: string) {
    const j = this.jobs.get(id);
    if (j?.upload && this.get(id).status === "uploading") {
      j.upload.abort();
      this.set(id, { status: "paused", speed: 0 });
      this.pump();
    }
  }
  resume(id: string) {
    if (this.get(id)?.status === "paused") this.set(id, { status: "queued" }), this.pump();
  }
  retry(id: string) {
    if (this.get(id)?.status === "failed" && this.jobs.has(id)) this.set(id, { status: "queued" }), this.pump();
  }
  cancel(id: string) {
    const j = this.jobs.get(id);
    if (j) {
      if (j.hashAbort) j.hashAbort.aborted = true;
      j.upload?.abort(true).catch(() => {});
    }
    this.finish(id, { status: "cancelled" });
  }
  clearFinished() {
    this.items = this.items.filter((i) => !["completed", "cancelled", "skipped"].includes(i.status) && !(i.status === "failed" && !this.jobs.has(i.id)));
    this.listeners.forEach((f) => f());
  }
}

export async function sha256File(file: Blob, abort: { aborted: boolean }): Promise<string> {
  const h = await createSHA256();
  h.init();
  const step = 8 * 1024 * 1024;
  for (let off = 0; off < file.size; off += step) {
    if (abort.aborted) return "";
    h.update(new Uint8Array(await file.slice(off, off + step).arrayBuffer()));
  }
  return h.digest("hex");
}

export const uploads = new UploadManager();

export function useUploads(): UpItem[] {
  return useSyncExternalStore(uploads.subscribe, uploads.snapshot);
}
