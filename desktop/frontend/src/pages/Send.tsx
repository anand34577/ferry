import { useEffect, useMemo, useState } from "react";
import { app, peerKey, type Item, type Peer } from "../api";
import { useApp } from "../App";
import { Icon, type IconName } from "../Icon";
import { Empty, Switch, useAction, useToast } from "../ui";
import { EXPIRY, ago, bytes, plural } from "../format";

type Dest = "nearby" | "devices" | "link";

export function SendPage() {
  const { selection, setSelection, state } = useApp();
  const [items, setItems] = useState<Item[]>([]);
  const [dest, setDest] = useState<Dest>("nearby");
  const toast = useToast();

  useEffect(() => {
    if (!selection.length) return setItems([]);
    app.Describe(selection).then((l) => setItems(l ?? []), toast.error);
  }, [selection]); // eslint-disable-line react-hooks/exhaustive-deps

  const add = async (folder: boolean) => {
    try {
      const picked = folder ? [await app.ChooseFolder("Choose a folder to send")].filter(Boolean) : ((await app.ChooseFiles()) ?? []);
      if (picked.length) setSelection((cur) => Array.from(new Set([...cur, ...picked])));
    } catch (e) {
      toast.error(e);
    }
  };
  const total = items.reduce((n, i) => n + i.size, 0);
  const count = items.reduce((n, i) => n + i.files, 0);
  const bad = items.filter((i) => i.error);

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>Send files</h1>
          <p className="muted">To devices nearby, to your own devices anywhere, or to anyone with a link.</p>
        </div>
      </header>

      {!selection.length ? (
        <div className="drop" style={{ ["--wails-drop-target" as string]: "drop" }}>
          <div className="big-ico">
            <Icon name="upload" size={32} />
          </div>
          <div className="stack tight center">
            <h2>Drop files or folders here</h2>
            <p className="muted">Or right-click files in Explorer → Send to → Ferry</p>
          </div>
          <div className="row">
            <button className="btn primary lg" onClick={() => add(false)}>
              <Icon name="file" size={18} /> Choose files
            </button>
            <button className="btn lg" onClick={() => add(true)}>
              <Icon name="folder" size={18} /> Choose folder
            </button>
          </div>
        </div>
      ) : (
        <section className="card">
          <div className="card-head">
            <div>
              <h2>{plural(count, "file")} selected</h2>
              <p className="muted small">{bytes(total)} in total</p>
            </div>
            <div className="row">
              <button className="btn sm" onClick={() => add(false)}>
                <Icon name="plus" size={16} /> Files
              </button>
              <button className="btn sm" onClick={() => add(true)}>
                <Icon name="folderPlus" size={16} /> Folder
              </button>
              <button className="btn sm ghost" onClick={() => setSelection([])}>
                Clear
              </button>
            </div>
          </div>
          <div className="selection">
            {items.map((i) => (
              <div key={i.path} className="sel-item">
                <div className={"file-ico" + (i.isDir ? " dir" : "")}>
                  <Icon name={i.isDir ? "folder" : "file"} size={18} />
                </div>
                <div className="grow">
                  <div className="ellipsis" title={i.path}>
                    {i.name}
                  </div>
                  <div className={"small " + (i.error ? "" : "muted")} style={i.error ? { color: "var(--err)" } : undefined}>
                    {i.error ? i.error : i.isDir ? `${plural(i.files, "file")} · ${bytes(i.size)}` : bytes(i.size)}
                  </div>
                </div>
                <button className="btn icon ghost" aria-label={`Remove ${i.name}`} onClick={() => setSelection((cur) => cur.filter((p) => p !== i.path))}>
                  <Icon name="x" size={16} />
                </button>
              </div>
            ))}
          </div>
        </section>
      )}

      <section className="stack">
        <div className="row" style={{ justifyContent: "space-between" }}>
          <div className="seg" role="tablist">
            {(
              [
                ["nearby", "Nearby", "wifi"],
                ["devices", "My devices", "laptop"],
                ["link", "Link", "link"],
              ] as [Dest, string, IconName][]
            ).map(([d, l, ic]) => (
              <button key={d} role="tab" aria-selected={dest === d} className={dest === d ? "on" : ""} onClick={() => setDest(d)}>
                <Icon name={ic} size={17} /> {l}
              </button>
            ))}
          </div>
        </div>
        {dest === "nearby" && <Nearby ready={!!selection.length && !bad.length} />}
        {dest === "devices" && <MyDevices ready={!!selection.length && !bad.length} />}
        {dest === "link" && <LinkForm ready={!!selection.length && !bad.length} defaultExpiry={state.settings.linkExpiry} />}
      </section>
    </div>
  );
}

function deviceIcon(type: string): IconName {
  if (type === "desktop" || type === "laptop" || type === "windows" || type === "web") return "laptop";
  if (type === "server" || type === "headless") return "server";
  return "phone";
}

function Nearby({ ready }: { ready: boolean }) {
  const { state, selection, go } = useApp();
  const { busy, run } = useAction();
  const toast = useToast();
  const [addr, setAddr] = useState("");
  const peers = state.peers ?? [];

  useEffect(() => {
    app.Discover(true);
    const t = setTimeout(() => peers.length || app.Scan(), 6000); // multicast silent → try a scan
    return () => {
      clearTimeout(t);
      app.Discover(false);
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const send = async (p: Peer) => {
    const id = await run(() => app.SendDirect(peerKey(p), selection));
    if (id) {
      toast.ok(`Sending to ${p.alias}`);
      go("transfers");
    }
  };
  const connect = async () => {
    const p = await run(() => app.Connect(addr.trim()));
    if (p) {
      setAddr("");
      toast.ok(`Found ${p.alias}`);
    }
  };

  return (
    <div className="card stack">
      <div className="row" style={{ justifyContent: "space-between" }}>
        {state.scanning ? (
          <span className="scanning">
            <span className="pulse" /> Looking for devices on your network…
          </span>
        ) : (
          <span className="muted small">Devices with Ferry or LocalSend open on the same Wi-Fi or network.</span>
        )}
        <button className="btn sm" onClick={() => app.Scan()} disabled={state.scanning}>
          <Icon name="retry" size={16} /> Scan network
        </button>
      </div>
      {peers.length ? (
        <div className="devices">
          {peers.map((p) => (
            <div key={peerKey(p)} className="device-wrap">
            <button className="device" onClick={() => send(p)} disabled={!ready || busy} title={ready ? `Send to ${p.alias}` : "Choose files first"}>
              <div className="dev-ico">
                <Icon name={deviceIcon(p.type)} size={22} />
              </div>
              <div style={{ minWidth: 0, width: "100%" }}>
                <div className="ellipsis" style={{ fontWeight: 600 }}>
                  {p.alias}
                </div>
                <div className="muted small ellipsis">{p.model || p.ip}</div>
              </div>
              <div className="row wrap" style={{ gap: 6 }}>
                {p.ferry ? <span className="chip accent">Ferry</span> : <span className="chip">LocalSend</span>}
                {!p.https && <span className="chip warn">Unencrypted</span>}
                {(state.settings.trusted ?? []).some((t) => t.fingerprint.toLowerCase() === p.fingerprint.toLowerCase()) && <span className="chip ok">Trusted</span>}
              </div>
            </button>
            <button className="btn icon ghost forget" aria-label={`Forget ${p.alias}`} title="Remove from the list" onClick={() => app.ForgetPeer(peerKey(p))}>
              <Icon name="x" size={15} />
            </button>
            </div>
          ))}
        </div>
      ) : (
        <Empty icon="wifi" title="No devices found yet" text={<>Open Ferry or LocalSend on the other device and keep it on the same network. If it still doesn't appear, connect by address below — some networks (guest Wi-Fi, offices) block devices from finding each other.</>} />
      )}
      <div className="divider" />
      <div className="row">
        <input
          type="text"
          placeholder="Connect by IP address, pairing code (e.g. 60A-R0BQ) or ferry:// link"
          value={addr}
          onChange={(e) => setAddr(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && addr.trim() && connect()}
        />
        <button className="btn" onClick={connect} disabled={!addr.trim() || busy}>
          Connect
        </button>
      </div>
    </div>
  );
}

function MyDevices({ ready }: { ready: boolean }) {
  const { state, selection, go, setState } = useApp();
  const { busy, run } = useAction();
  const toast = useToast();
  const acc = (state.accounts ?? []).find((a) => a.active);
  const devices = state.devices ?? [];
  if (!acc)
    return (
      <div className="card">
        <Empty icon="server" title="Connect your Ferry server" text="Send to your phone or other computers anywhere — through your own server when they're not nearby.">
          <button className="btn primary" onClick={() => go("server")}>
            Sign in to your server
          </button>
        </Empty>
      </div>
    );
  const send = async (id: string, name: string) => {
    const t = await run(() => app.SendToDevice(id, selection));
    if (t) {
      toast.ok(`Sending to ${name}`);
      go("transfers");
    }
  };
  return (
    <div className="card stack">
      <div className="row" style={{ justifyContent: "space-between" }}>
        <span className="muted small">Direct when the device is nearby, otherwise through {acc.siteName}. Offline devices get the files when they come online.</span>
        <button className="btn sm" onClick={() => app.RefreshDevices().then(setState, toast.error)}>
          <Icon name="retry" size={16} /> Refresh
        </button>
      </div>
      {state.serverError && (
        <div className="notice err">
          <Icon name="alert" size={18} /> {state.serverError}
        </div>
      )}
      {devices.length ? (
        <div className="devices">
          {devices.map((d) => (
            <button key={d.id} className="device" onClick={() => send(d.id, d.name)} disabled={!ready || busy} title={ready ? `Send to ${d.name}` : "Choose files first"}>
              <div className="dev-ico">
                <Icon name={d.platform === "android" ? "phone" : "laptop"} size={22} />
              </div>
              <div style={{ minWidth: 0, width: "100%" }}>
                <div className="ellipsis" style={{ fontWeight: 600 }}>
                  {d.name}
                </div>
                <div className="muted small row" style={{ gap: 6 }}>
                  <span className={"dot" + (d.online ? " ok" : "")} /> {d.online ? "Online" : `Seen ${ago(d.lastSeen)}`}
                </div>
              </div>
              {d.lanAddrs?.length ? <span className="chip ok">Nearby</span> : <span className="chip">Via server</span>}
            </button>
          ))}
        </div>
      ) : (
        <Empty icon="laptop" title="No other devices yet" text="Sign in to the Ferry app on your phone or another computer with the same account; it appears here." />
      )}
    </div>
  );
}

function LinkForm({ ready, defaultExpiry }: { ready: boolean; defaultExpiry: number }) {
  const { state, selection, go } = useApp();
  const { busy, run } = useAction();
  const toast = useToast();
  const [expiry, setExpiry] = useState(defaultExpiry);
  const [limit, setLimit] = useState<"none" | "once" | "n">("none");
  const [n, setN] = useState(5);
  const [usePw, setUsePw] = useState(false);
  const [pw, setPw] = useState("");
  const [msg, setMsg] = useState("");
  const acc = useMemo(() => (state.accounts ?? []).find((a) => a.active), [state.accounts]);
  if (!acc)
    return (
      <div className="card">
        <Empty icon="link" title="Links need your Ferry server" text="Files are uploaded to your server and anyone with the link can download them — no app needed.">
          <button className="btn primary" onClick={() => go("server")}>
            Sign in to your server
          </button>
        </Empty>
      </div>
    );
  const create = async () => {
    if (usePw && (pw.length < 4 || pw.length > 72)) return toast.error(new Error("Link passwords must be 4–72 characters"));
    const id = await run(() =>
      app.CreateLink(selection, { expiresIn: expiry, maxDownloads: limit === "none" ? 0 : limit === "once" ? 1 : Math.max(2, n), password: usePw ? pw : "", message: msg.trim() }),
    );
    if (id) {
      toast.ok("Uploading — the link appears when it's ready");
      go("transfers");
    }
  };
  return (
    <div className="card stack">
      <p className="muted small">Uploads to {acc.siteName} and creates a link anyone can open in a browser.</p>
      <div className="row wrap" style={{ alignItems: "flex-end", gap: 16 }}>
        <label className="field" style={{ minWidth: 180 }}>
          <span>Expires after</span>
          <select value={expiry} onChange={(e) => setExpiry(Number(e.target.value))}>
            {EXPIRY.map(([l, v]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <label className="field" style={{ minWidth: 180 }}>
          <span>Downloads</span>
          <select value={limit} onChange={(e) => setLimit(e.target.value as typeof limit)}>
            <option value="none">Unlimited</option>
            <option value="once">Once (one-time link)</option>
            <option value="n">Limited number…</option>
          </select>
        </label>
        {limit === "n" && (
          <label className="field" style={{ width: 110 }}>
            <span>Times</span>
            <input type="number" min={2} max={10000} value={n} onChange={(e) => setN(Number(e.target.value))} />
          </label>
        )}
      </div>
      <Switch checked={usePw} onChange={setUsePw} label="Protect with a password" hint="Share the password separately, e.g. by phone." />
      {usePw && <input type="password" placeholder="Link password (4–72 characters)" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" />}
      <label className="field">
        <span>
          Message <span className="muted">(optional, shown on the download page)</span>
        </span>
        <textarea value={msg} onChange={(e) => setMsg(e.target.value)} maxLength={2000} />
      </label>
      <div>
        <button className="btn primary lg" disabled={!ready || busy} onClick={create} title={ready ? undefined : "Choose files first"}>
          <Icon name="link" size={18} /> Create link
        </button>
      </div>
    </div>
  );
}
