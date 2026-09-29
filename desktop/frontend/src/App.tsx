import { createContext, useContext, useEffect, useState, type Dispatch, type SetStateAction } from "react";
import { app, on, type Device, type Incoming, type Peer, type State, type Transfer } from "./api";
import { Icon, type IconName } from "./Icon";
import { Modal, QR, Switch, useToast } from "./ui";
import { bytes, plural } from "./format";
import { SendPage } from "./pages/Send";
import { ReceivePage } from "./pages/Receive";
import { TransfersPage } from "./pages/Transfers";
import { ServerPage } from "./pages/Server";
import { SettingsPage } from "./pages/Settings";

export type Page = "send" | "receive" | "transfers" | "server" | "settings";

interface Ctx {
  state: State;
  setState: Dispatch<SetStateAction<State | null>>;
  go: (p: Page) => void;
  selection: string[];
  setSelection: Dispatch<SetStateAction<string[]>>;
}
const AppCtx = createContext<Ctx>(null!);
export const useApp = () => useContext(AppCtx);

function applyTheme(t: string) {
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
  else delete document.documentElement.dataset.theme;
}

export default function App() {
  const [state, setState] = useState<State | null>(null);
  const [error, setError] = useState("");
  const [page, setPage] = useState<Page>("send");
  const [selection, setSelection] = useState<string[]>([]);
  const [pin, setPin] = useState<{ transferId: string; peer: string; wrong: boolean } | null>(null);
  const [link, setLink] = useState<{ transferId: string; url: string } | null>(null);

  useEffect(() => {
    app
      .GetState()
      .then((s) => {
        setState(s);
        if (s.pending?.length) setSelection(s.pending);
      })
      .catch((e) => setError(String(e?.message ?? e)));
    const offs = [
      on<Transfer[]>("transfers", (t) => setState((s) => s && { ...s, transfers: t })),
      on<Peer[]>("peers", (p) => setState((s) => s && { ...s, peers: p, scanning: s.scanning })),
      on<Incoming[]>("incoming", (i) => setState((s) => s && { ...s, incoming: i })),
      on<{ devices: Device[] | null; online: boolean; error: string }>("server", (d) => setState((s) => s && { ...s, devices: d.devices, online: d.online, serverError: d.error })),
      on<State>("state", (n) => setState(n)),
      on<string[]>("files", (f) => {
        setSelection((cur) => Array.from(new Set([...cur, ...f])));
        setPage("send");
      }),
      on<{ transferId: string; peer: string; wrong: boolean } | null>("pin", setPin),
      on<{ transferId: string; url: string }>("link", setLink),
    ];
    return () => offs.forEach((f) => f());
  }, []);

  useEffect(() => applyTheme(state?.settings.theme ?? "system"), [state?.settings.theme]);

  if (!state)
    return (
      <div className="empty" style={{ height: "100%", justifyContent: "center" }}>
        {error ? (
          <>
            <div className="ico">
              <Icon name="alert" size={26} />
            </div>
            <h3>Ferry couldn't start</h3>
            <p className="small">{error}</p>
          </>
        ) : (
          <div className="scanning">
            <span className="pulse" /> Starting Ferry…
          </div>
        )}
      </div>
    );

  const active = (state.transfers ?? []).filter((t) => !["completed", "failed", "cancelled", "rejected"].includes(t.status) || (t.status === "failed" && t.canRetry));
  const nav: [Page, string, IconName, number?][] = [
    ["send", "Send", "send"],
    ["receive", "Receive", "download"],
    ["transfers", "Transfers", "clock", active.length || undefined],
    ["server", "My devices", "server"],
    ["settings", "Settings", "gear"],
  ];

  return (
    <AppCtx.Provider value={{ state, setState, go: setPage, selection, setSelection }}>
      <div className="shell">
        <aside className="side">
          <div className="brand">
            <span className="logo" aria-hidden="true" />
            Ferry
          </div>
          {nav.map(([p, label, icon, badge]) => (
            <button key={p} className={"nav" + (page === p ? " on" : "")} onClick={() => setPage(p)} aria-current={page === p ? "page" : undefined}>
              <Icon name={icon} size={19} />
              {label}
              {badge ? <span className="badge">{badge}</span> : null}
            </button>
          ))}
          <SideStatus />
        </aside>
        <main>
          {page === "send" && <SendPage />}
          {page === "receive" && <ReceivePage />}
          {page === "transfers" && <TransfersPage />}
          {page === "server" && <ServerPage />}
          {page === "settings" && <SettingsPage />}
        </main>
      </div>
      {(state.incoming ?? []).slice(0, 1).map((i) => (
        <IncomingDialog key={i.request.id} inc={i} />
      ))}
      {pin && <PinDialog req={pin} onDone={() => setPin(null)} />}
      {link && <LinkDialog url={link.url} onClose={() => setLink(null)} />}
    </AppCtx.Provider>
  );
}

function SideStatus() {
  const { state, setState, go } = useApp();
  const toast = useToast();
  const s = state.settings;
  const acc = (state.accounts ?? []).find((a) => a.active);
  return (
    <div className="side-foot">
      <div className="status-card">
        <Switch
          checked={s.receiving}
          label={s.receiving ? "Receiving" : "Not receiving"}
          hint={s.receiving ? `Visible as “${s.alias}”` : "Nearby devices can't send"}
          onChange={(v) => app.SaveSettings({ receiving: v }).then(setState).catch(toast.error)}
        />
      </div>
      <button className="nav" onClick={() => go("server")}>
        <span className={"dot" + (acc ? (state.online ? " ok" : state.serverError ? " err" : " warn") : "")} />
        <span className="ellipsis small">{acc ? `${acc.siteName} · ${state.online ? "connected" : state.serverError ? "offline" : "connecting…"}` : "No server connected"}</span>
      </button>
    </div>
  );
}

function IncomingDialog({ inc }: { inc: Incoming }) {
  const { state } = useApp();
  const toast = useToast();
  const [trust, setTrust] = useState(false);
  const r = inc.request;
  const files = r.files ?? [];
  const answer = async (accept: boolean, choose = false) => {
    let dir = "";
    if (accept && (choose || state.settings.askWhereToSave)) {
      dir = await app.ChooseFolder("Save the files in…").catch(() => "");
      if (!dir) return;
    }
    app.RespondIncoming(r.id, accept, trust, dir).catch(toast.error);
  };
  return (
    <Modal
      title={`${r.alias} wants to send you ${files.length === 1 ? "a file" : plural(files.length, "file")}`}
      onClose={() => answer(false)}
      footer={
        <>
          <button className="btn" onClick={() => answer(false)}>
            Decline
          </button>
          <button className="btn" onClick={() => answer(true, true)}>
            Save to…
          </button>
          <button className="btn primary" onClick={() => answer(true)} autoFocus>
            <Icon name="download" size={18} /> Accept
          </button>
        </>
      }
    >
      <div className="row">
        <div className="t-ico in">
          <Icon name={inc.method === "server" ? "server" : "wifi"} size={20} />
        </div>
        <div className="grow">
          <strong>{r.alias}</strong> {r.trusted && <span className="chip ok">Trusted</span>}
          <div className="muted small">
            {inc.method === "server" ? `Your device, through ${r.model || "your server"}` : r.model ? `${r.model} · on this network` : "On this network"} · {bytes(r.totalBytes)}
          </div>
        </div>
      </div>
      <div className="file-list">
        {files.slice(0, 200).map((f, i) => (
          <div key={i}>
            <span className="grow ellipsis" title={f.name}>
              {f.name}
            </span>
            <span className="muted">{bytes(f.size)}</span>
          </div>
        ))}
        {files.length > 200 && <div className="muted">…and {files.length - 200} more</div>}
      </div>
      <p className="muted small">
        Saved to <span className="kbd-path">{state.settings.downloadDir}</span>
      </p>
      {inc.method === "direct" && r.fingerprint && !r.trusted && <Switch checked={trust} onChange={setTrust} label="Remember this device as trusted" hint="You can let trusted devices send without asking in Settings." />}
    </Modal>
  );
}

function PinDialog({ req, onDone }: { req: { transferId: string; peer: string; wrong: boolean }; onDone: () => void }) {
  const [pin, setPin] = useState("");
  const send = (v: string) => {
    app.AnswerPIN(req.transferId, v);
    onDone();
  };
  return (
    <Modal
      title={`${req.peer} requires a PIN`}
      onClose={() => send("")}
      footer={
        <>
          <button className="btn" onClick={() => send("")}>
            Cancel
          </button>
          <button className="btn primary" disabled={!pin.trim()} onClick={() => send(pin.trim())}>
            Send
          </button>
        </>
      }
    >
      {req.wrong && (
        <div className="notice err">
          <Icon name="alert" size={18} /> That PIN was wrong. Try again.
        </div>
      )}
      <label className="field">
        <span>PIN shown on {req.peer}</span>
        <input type="password" value={pin} onChange={(e) => setPin(e.target.value)} autoFocus onKeyDown={(e) => e.key === "Enter" && pin.trim() && send(pin.trim())} />
      </label>
    </Modal>
  );
}

function LinkDialog({ url, onClose }: { url: string; onClose: () => void }) {
  const toast = useToast();
  return (
    <Modal
      title="Your link is ready"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={() => app.OpenURL(url)}>
            <Icon name="globe" size={18} /> Open
          </button>
          <button className="btn primary" onClick={() => app.CopyText(url).then(() => toast.ok("Link copied"), toast.error)}>
            <Icon name="copy" size={18} /> Copy link
          </button>
        </>
      }
    >
      <p className="muted">Anyone with this link can download the files — no app or account needed.</p>
      <div className="row" style={{ alignItems: "flex-start" }}>
        <QR value={url} size={148} />
        <div className="grow stack tight">
          <input type="text" readOnly value={url} onFocus={(e) => e.target.select()} className="mono" />
          <span className="hint">Scan the code with a phone to open the link there.</span>
        </div>
      </div>
    </Modal>
  );
}
