import { useState } from "react";
import { app } from "../api";
import { useApp } from "../App";
import { Icon } from "../Icon";
import { QR, Switch, useToast } from "../ui";

export function ReceivePage() {
  const { state, setState } = useApp();
  const toast = useToast();
  const s = state.settings;
  const id = state.identity;
  const [pin, setPin] = useState(s.pin);
  const save = (p: Parameters<typeof app.SaveSettings>[0], ok?: string) =>
    app.SaveSettings(p).then((n) => (setState(n), ok && toast.ok(ok)), toast.error);
  const addrs = (id.addrs ?? []).filter((a) => !a.virtual);

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>Receive files</h1>
          <p className="muted">Nearby phones and computers with Ferry or LocalSend can send to this PC. You're always asked first.</p>
        </div>
      </header>

      {id.error && (
        <div className="notice err">
          <Icon name="alert" size={18} /> {id.error}
        </div>
      )}

      <section className="card hero">
        <div className="stack">
          <div className="row">
            <span className={"dot" + (s.receiving && id.listening ? " ok" : "")} />
            <h2>{s.receiving ? `Visible as “${s.alias}”` : "Not receiving"}</h2>
          </div>
          <Switch
            checked={s.receiving}
            onChange={(v) => save({ receiving: v })}
            label="Receive files from nearby devices"
            hint="Ferry keeps receiving from the tray while its window is closed."
          />
          {s.receiving && addrs.length > 0 && (
            <>
              <div className="divider" />
              <dl className="facts">
                <dt>Pairing code</dt>
                <dd>
                  <span className="code selectable">{id.pairCode}</span>
                  <button className="btn icon ghost" aria-label="Copy pairing code" onClick={() => app.CopyText(id.pairCode).then(() => toast.ok("Pairing code copied"))}>
                    <Icon name="copy" size={16} />
                  </button>
                </dd>
                <dt>Address</dt>
                <dd className="selectable mono">
                  {addrs.map((a) => a.ip).join(", ")} · port {id.port}
                </dd>
                <dt>Identity</dt>
                <dd className="mono small ellipsis selectable" title={id.fingerprint}>
                  {id.fingerprint.slice(0, 16)}…
                </dd>
              </dl>
              <p className="hint">On a phone: Ferry → Send → Connect, then scan the code or type the pairing code. LocalSend finds this PC by itself.</p>
            </>
          )}
          {s.receiving && !addrs.length && (
            <div className="notice warn">
              <Icon name="wifi" size={18} /> This PC isn't connected to a local network. Connect to Wi-Fi or Ethernet (or a phone's hotspot) to receive nearby.
            </div>
          )}
        </div>
        {s.receiving && id.qr && (
          <div className="stack tight center">
            <QR value={id.qr} size={168} />
            <span className="hint">Scan with the Ferry app</span>
          </div>
        )}
      </section>

      <section className="card">
        <div className="card-head">
          <h2>Security</h2>
        </div>
        <div className="setting">
          <div>
            <div>Require a PIN</div>
            <div className="hint">Senders must enter it before you are even asked. After 5 wrong tries, the PIN is locked for a minute.</div>
          </div>
          <div className="row">
            <input
              type="text"
              inputMode="numeric"
              style={{ width: 120 }}
              placeholder="PIN"
              value={pin}
              maxLength={12}
              onChange={(e) => setPin(e.target.value)}
              onBlur={() => pin !== s.pin && save({ pin }, pin ? "PIN saved" : undefined)}
            />
            <Switch checked={s.requirePin && !!s.pin} disabled={!pin.trim() || pin !== s.pin} onChange={(v) => save({ requirePin: v })} label={s.requirePin && s.pin ? "On" : "Off"} />
          </div>
        </div>
        <div className="setting">
          <div>
            <div>Save received files in</div>
            <div className="hint">
              <span className="kbd-path">{s.downloadDir}</span>
            </div>
          </div>
          <div className="row">
            <button className="btn sm" onClick={() => app.OpenDownloads().catch(toast.error)}>
              Open
            </button>
            <button
              className="btn sm"
              onClick={async () => {
                const d = await app.ChooseFolder("Save received files in…").catch(() => "");
                if (d) save({ downloadDir: d }, "Download folder changed");
              }}
            >
              Change…
            </button>
          </div>
        </div>
      </section>

      <div className="notice">
        <Icon name="info" size={18} />
        <div className="small">
          <strong>Other devices can't find this PC?</strong> Make sure both are on the same network and that Windows treats it as a <em>Private network</em> (Settings → Network &
          internet → your Wi-Fi → Private). Guest and office networks often block devices from seeing each other — then use a link or send through your server instead.
        </div>
      </div>
    </div>
  );
}
