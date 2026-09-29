import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import QRCode from "qrcode";
import { Icon, type IconName } from "./Icon";
import { message } from "./api";

type Toast = { id: number; text: string; err?: boolean };
const ToastCtx = createContext<{ ok: (t: string) => void; error: (e: unknown) => void }>(null!);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([]);
  const push = useCallback((text: string, err?: boolean) => {
    const id = Date.now() + Math.random();
    setList((l) => [...l.slice(-3), { id, text, err }]);
    setTimeout(() => setList((l) => l.filter((t) => t.id !== id)), err ? 8000 : 3500);
  }, []);
  const api = useRef({ ok: (t: string) => push(t), error: (e: unknown) => push(message(e), true) });
  return (
    <ToastCtx.Provider value={api.current}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {list.map((t) => (
          <div key={t.id} className={"toast" + (t.err ? " err" : "")}>
            <Icon name={t.err ? "alert" : "check"} size={18} />
            <span>{t.text}</span>
            <button onClick={() => setList((l) => l.filter((x) => x.id !== t.id))} aria-label="Dismiss">
              <Icon name="x" size={16} />
            </button>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}

export const useToast = () => useContext(ToastCtx);

export function Modal({ title, children, footer, onClose }: { title: string; children: ReactNode; footer?: ReactNode; onClose?: () => void }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && onClose?.();
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, [onClose]);
  return (
    <div className="backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose?.()}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title}>
        <h2>{title}</h2>
        {children}
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
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

export function QR({ value, size = 176 }: { value: string; size?: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    if (ref.current && value) QRCode.toCanvas(ref.current, value, { width: size, margin: 0, errorCorrectionLevel: "M" }).catch(() => {});
  }, [value, size]);
  return (
    <div className="qr-box">
      <canvas ref={ref} width={size} height={size} aria-label="QR code" role="img" />
    </div>
  );
}

export function Empty({ icon, title, text, children }: { icon: IconName; title: string; text?: ReactNode; children?: ReactNode }) {
  return (
    <div className="empty">
      <div className="ico">
        <Icon name={icon} size={26} />
      </div>
      <h3>{title}</h3>
      {text && <p className="small">{text}</p>}
      {children}
    </div>
  );
}

export function Progress({ value, indeterminate }: { value: number; indeterminate?: boolean }) {
  return (
    <div className={"bar" + (indeterminate ? " indet" : "")} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(value * 100)}>
      <i style={{ width: `${Math.min(100, Math.max(0, value * 100))}%` }} />
    </div>
  );
}

/** Runs an async action with a busy flag and error toast. */
export function useAction() {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const run = useCallback(
    async <T,>(fn: () => Promise<T>, ok?: string): Promise<T | undefined> => {
      setBusy(true);
      try {
        const r = await fn();
        if (ok) toast.ok(ok);
        return r;
      } catch (e) {
        toast.error(e);
        return undefined;
      } finally {
        setBusy(false);
      }
    },
    [toast],
  );
  return { busy, run };
}
