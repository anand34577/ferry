import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import QRCode from "qrcode";
import { Icon, type IconName } from "./Icon";
import { ApiError } from "../lib/api";
import { fileExt, fileKind } from "../lib/format";

// ---------- Modal (native <dialog>: focus trap, Esc to close, inert background) ----------

export function Modal({ open, onClose, title, children, footer, wide }: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      className={"modal" + (wide ? " wide" : "")}
      onClose={onClose}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => e.target === ref.current && onClose()}
      aria-labelledby="modal-title"
    >
      {open && (
        <div className="modal-body">
          <header className="modal-head">
            <h2 id="modal-title">{title}</h2>
            <button className="icon-btn" onClick={onClose} aria-label="Close">
              <Icon name="x" />
            </button>
          </header>
          <div className="modal-content">{children}</div>
          {footer && <footer className="modal-foot">{footer}</footer>}
        </div>
      )}
    </dialog>
  );
}

// ---------- Toasts ----------

interface Toast {
  id: number;
  kind: "ok" | "error" | "info";
  text: string;
  detail?: string;
}
const ToastCtx = createContext<(kind: Toast["kind"], text: string, detail?: string) => void>(() => {});
let toastSeq = 0;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((kind: Toast["kind"], text: string, detail?: string) => {
    const id = ++toastSeq;
    setToasts((t) => [...t.slice(-3), { id, kind, text, detail }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), kind === "error" ? 9000 : 4000);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={"toast " + t.kind}>
            <Icon name={t.kind === "ok" ? "check" : t.kind === "error" ? "alert" : "info"} />
            <div className="toast-text">
              <span>{t.text}</span>
              {t.detail && (
                <details>
                  <summary>Details</summary>
                  <code>{t.detail}</code>
                </details>
              )}
            </div>
            <button className="icon-btn sm" aria-label="Dismiss" onClick={() => setToasts((x) => x.filter((y) => y.id !== t.id))}>
              <Icon name="x" size={16} />
            </button>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}

export function useToast() {
  const push = useContext(ToastCtx);
  return {
    ok: (t: string) => push("ok", t),
    info: (t: string) => push("info", t),
    error: (e: unknown) => {
      if (e instanceof ApiError) push("error", e.message, e.status ? `${e.status} ${e.code}` : e.code);
      else push("error", e instanceof Error ? e.message : String(e));
    },
  };
}

// ---------- small building blocks ----------

export function EmptyState({ icon, title, text, children }: { icon: IconName; title: string; text?: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-icon">
        <Icon name={icon} size={28} />
      </div>
      <h3>{title}</h3>
      {text && <p>{text}</p>}
      {children && <div className="empty-actions">{children}</div>}
    </div>
  );
}

export function FileBadge({ name, mime, folder, size = "md" }: { name: string; mime?: string; folder?: boolean; size?: "sm" | "md" | "lg" }) {
  if (folder)
    return (
      <span className={`fbadge ${size} k-folder`} aria-hidden="true">
        <Icon name="folder" size={size === "lg" ? 26 : 20} />
      </span>
    );
  return (
    <span className={`fbadge ${size} k-${fileKind(mime ?? "", name)}`} aria-hidden="true">
      {fileExt(name)}
    </span>
  );
}

export function Spinner({ label = "Loading" }: { label?: string }) {
  return <span className="spinner" role="progressbar" aria-label={label} />;
}

export function Loading() {
  return (
    <div className="loading">
      <Spinner />
    </div>
  );
}

export function Progress({ value, label }: { value: number; label?: string }) {
  const pct = Math.max(0, Math.min(1, value)) * 100;
  return (
    <div className="progress" role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100} aria-label={label}>
      <i style={{ width: pct + "%" }} />
    </div>
  );
}

export function Switch({ checked, onChange, label, hint, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; hint?: string; disabled?: boolean }) {
  return (
    <label className={"switch-row" + (disabled ? " disabled" : "")}>
      <span className="switch-text">
        <span>{label}</span>
        {hint && <small>{hint}</small>}
      </span>
      <input type="checkbox" role="switch" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span className="switch" aria-hidden="true" />
    </label>
  );
}

type MenuItem = { label: string; icon: IconName; onClick: () => void; danger?: boolean };

// The popup is position:fixed next to its button, so tables and panels with overflow never clip it;
// it opens upwards when there isn't room below.
export function Menu({ items, label = "More actions" }: { items: (MenuItem | false | null | undefined)[]; label?: string }) {
  const [pos, setPos] = useState<React.CSSProperties | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  const list = items.filter(Boolean) as MenuItem[];
  const open = pos !== null;
  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent ? e.key === "Escape" : !ref.current?.contains(e.target as Node)) setPos(null);
    };
    const dismiss = () => setPos(null);
    document.addEventListener("mousedown", close);
    document.addEventListener("touchstart", close);
    document.addEventListener("keydown", close);
    window.addEventListener("resize", dismiss);
    window.addEventListener("scroll", dismiss, true);
    pop.current?.querySelector("button")?.focus();
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("touchstart", close);
      document.removeEventListener("keydown", close);
      window.removeEventListener("resize", dismiss);
      window.removeEventListener("scroll", dismiss, true);
    };
  }, [open]);
  const toggle = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation();
    if (open) return setPos(null);
    const r = e.currentTarget.getBoundingClientRect();
    const height = list.length * 42 + 14;
    const right = Math.max(8, window.innerWidth - r.right);
    setPos(r.bottom + height + 8 > window.innerHeight && r.top > height ? { right, bottom: window.innerHeight - r.top + 4 } : { right, top: r.bottom + 4 });
  };
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const btns = Array.from(pop.current?.querySelectorAll("button") ?? []);
    const i = btns.indexOf(document.activeElement as HTMLButtonElement);
    btns[(i + (e.key === "ArrowDown" ? 1 : btns.length - 1)) % btns.length]?.focus();
  };
  return (
    <div className="menu" ref={ref}>
      <button className="icon-btn" aria-label={label} aria-haspopup="menu" aria-expanded={open} onClick={toggle}>
        <Icon name="more" />
      </button>
      {open && (
        <div className="menu-pop" role="menu" ref={pop} style={pos} onKeyDown={onKey}>
          {list.map((i) => (
            <button
              key={i.label}
              role="menuitem"
              className={i.danger ? "danger" : ""}
              onClick={(e) => {
                e.stopPropagation();
                setPos(null);
                i.onClick();
              }}
            >
              <Icon name={i.icon} size={18} />
              {i.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// Promise-based confirm/prompt dialogs, so call sites stay linear.
type DialogReq =
  | { kind: "confirm"; title: string; text: string; action: string; danger?: boolean; resolve: (v: boolean) => void }
  | { kind: "prompt"; title: string; label: string; value: string; action: string; resolve: (v: string | null) => void };
const DialogCtx = createContext<(r: DialogReq) => void>(() => {});

export function DialogProvider({ children }: { children: ReactNode }) {
  const [req, setReq] = useState<DialogReq | null>(null);
  const [val, setVal] = useState("");
  const close = (v: boolean | string | null) => {
    if (!req) return;
    if (req.kind === "confirm") req.resolve(v === true);
    else req.resolve(typeof v === "string" ? v : null);
    setReq(null);
  };
  return (
    <DialogCtx.Provider value={(r) => (setVal(r.kind === "prompt" ? r.value : ""), setReq(r))}>
      {children}
      <Modal
        open={!!req}
        onClose={() => close(null)}
        title={req?.title ?? ""}
        footer={
          <>
            <button className="btn" onClick={() => close(null)}>
              Cancel
            </button>
            <button
              className={"btn " + (req?.kind === "confirm" && req.danger ? "danger-solid" : "primary")}
              onClick={() => close(req?.kind === "prompt" ? val.trim() || null : true)}
              form={req?.kind === "prompt" ? "prompt-form" : undefined}
              type={req?.kind === "prompt" ? "submit" : "button"}
            >
              {req?.action}
            </button>
          </>
        }
      >
        {req?.kind === "confirm" && <p className="muted">{req.text}</p>}
        {req?.kind === "prompt" && (
          <form
            id="prompt-form"
            onSubmit={(e) => {
              e.preventDefault();
              close(val.trim() || null);
            }}
          >
            <label className="field">
              <span>{req.label}</span>
              <input autoFocus value={val} onChange={(e) => setVal(e.target.value)} onFocus={(e) => {
                const dot = e.target.value.lastIndexOf(".");
                e.target.setSelectionRange(0, dot > 0 ? dot : e.target.value.length);
              }} />
            </label>
          </form>
        )}
      </Modal>
    </DialogCtx.Provider>
  );
}

export function useDialogs() {
  const open = useContext(DialogCtx);
  return {
    confirm: (title: string, text: string, action = "Confirm", danger = false) =>
      new Promise<boolean>((resolve) => open({ kind: "confirm", title, text, action, danger, resolve })),
    prompt: (title: string, label: string, value = "", action = "Save") =>
      new Promise<string | null>((resolve) => open({ kind: "prompt", title, label, value, action, resolve })),
  };
}

export function QR({ value, size = 200 }: { value: string; size?: number }) {
  const [src, setSrc] = useState("");
  useEffect(() => {
    QRCode.toDataURL(value, { width: size * 2, margin: 1, errorCorrectionLevel: "M" }).then(setSrc).catch(() => setSrc(""));
  }, [value, size]);
  return src ? <img className="qr" src={src} width={size} height={size} alt="QR code for the link" /> : <div className="qr" style={{ width: size, height: size }} />;
}

export function CopyField({ value, label = "Link" }: { value: string; label?: string }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      // Clipboard API needs HTTPS; fall back to selection copy.
      const ta = document.createElement("textarea");
      ta.value = value;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    setCopied(true);
    toast.ok("Link copied");
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className="copy-field">
      <input readOnly value={value} aria-label={label} onFocus={(e) => e.target.select()} />
      <button className="btn primary" onClick={copy}>
        <Icon name={copied ? "check" : "copy"} size={18} />
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]) {
  const [state, setState] = useState<{ data?: T; error?: unknown; loading: boolean }>({ loading: true });
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    setState((s) => ({ ...s, loading: true }));
    fn().then(
      (data) => alive && setState({ data, loading: false }),
      (error) => alive && setState({ error, loading: false }),
    );
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);
  return { ...state, reload: () => setTick((t) => t + 1) };
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const msg = error instanceof Error ? error.message : "Something went wrong.";
  return (
    <div className="error-box" role="alert">
      <Icon name="alert" />
      <div>
        <p>{msg}</p>
        {error instanceof ApiError && error.status > 0 && (
          <details>
            <summary>Details</summary>
            <code>
              {error.status} {error.code}
            </code>
          </details>
        )}
      </div>
      {onRetry && (
        <button className="btn sm" onClick={onRetry}>
          <Icon name="retry" size={16} /> Retry
        </button>
      )}
    </div>
  );
}

export function StatusChip({ status }: { status: string }) {
  const map: Record<string, [string, string]> = {
    active: ["Active", "ok"],
    expired: ["Expired", "muted"],
    revoked: ["Disabled", "muted"],
    exhausted: ["Limit reached", "warn"],
  };
  const [text, cls] = map[status] ?? [status, "muted"];
  return <span className={"chip " + cls}>{text}</span>;
}
