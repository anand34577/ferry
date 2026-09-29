import { useState } from "react";
import { app, type Settings } from "../api";
import { useApp } from "../App";
import { Icon } from "../Icon";
import { Switch, useToast } from "../ui";
import { EXPIRY } from "../format";

export function SettingsPage() {
  const { state, setState } = useApp();
  const toast = useToast();
  const s = state.settings;
  const [alias, setAlias] = useState(s.alias);
  const save = (p: Partial<Settings>, ok?: string) => app.SaveSettings(p).then((n) => (setState(n), ok && toast.ok(ok)), toast.error);
  const trusted = s.trusted ?? [];
  const reload = () => app.GetState().then(setState);

  return (
    <div className="page">
      <header className="page-head">
        <h1>Settings</h1>
      </header>

      <section className="card">
        <div className="card-head">
          <h2>This PC</h2>
        </div>
        <div className="setting">
          <div>
            <div>Device name</div>
            <div className="hint">How this PC appears to nearby devices and in your device list.</div>
          </div>
          <input
            type="text"
            value={alias}
            maxLength={40}
            style={{ width: 240 }}
            onChange={(e) => setAlias(e.target.value)}
            onBlur={() => alias.trim() !== s.alias && save({ alias: alias.trim() }, "Name saved").then(() => setAlias((a) => a.trim()))}
            onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          />
        </div>
        <div className="setting">
          <Switch checked={s.closeToTray} onChange={(v) => save({ closeToTray: v })} label="Keep running in the tray when closed" hint="So nearby devices and your server can still deliver files. Quit from the tray icon." />
        </div>
        <div className="setting">
          <Switch checked={s.startWithWindows} onChange={(v) => save({ startWithWindows: v })} label="Start with Windows" hint="Starts in the tray when you sign in to Windows." />
        </div>
        <div className="setting">
          <Switch checked={s.notifications} onChange={(v) => save({ notifications: v })} label="Notifications" hint="When someone wants to send you files and when transfers finish." />
        </div>
        <div className="setting">
          <div>
            <div>Appearance</div>
          </div>
          <div className="seg">
            {(["system", "light", "dark"] as const).map((t) => (
              <button key={t} className={s.theme === t ? "on" : ""} onClick={() => save({ theme: t })}>
                <Icon name={t === "system" ? "monitor" : t === "light" ? "sun" : "moon"} size={16} /> {t[0].toUpperCase() + t.slice(1)}
              </button>
            ))}
          </div>
        </div>
      </section>

      <section className="card">
        <div className="card-head">
          <h2>Receiving</h2>
        </div>
        <div className="setting">
          <div>
            <div>Save files in</div>
            <div className="hint">
              <span className="kbd-path">{s.downloadDir}</span>
            </div>
          </div>
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
        <div className="setting">
          <Switch checked={s.askWhereToSave} onChange={(v) => save({ askWhereToSave: v })} label="Ask where to save each time" />
        </div>
        <div className="setting">
          <Switch checked={s.openWhenDone} onChange={(v) => save({ openWhenDone: v })} label="Show files in Explorer when a transfer finishes" />
        </div>
        <div className="setting">
          <Switch checked={s.autoAcceptOwn} onChange={(v) => save({ autoAcceptOwn: v })} label="Accept files from my own devices without asking" hint="Files sent to this PC through your Ferry server." />
        </div>
      </section>

      <section className="card">
        <div className="card-head">
          <div>
            <h2>Trusted devices</h2>
            <p className="muted small">Devices you marked as trusted when accepting files. Their identity is checked cryptographically every time.</p>
          </div>
        </div>
        {trusted.length ? (
          trusted.map((t) => (
            <div key={t.fingerprint} className="setting">
              <div>
                <div>{t.alias}</div>
                <div className="hint mono">{t.fingerprint.slice(0, 16)}…</div>
              </div>
              <div className="row">
                <Switch checked={t.autoAccept} onChange={(v) => app.SetTrusted(t.fingerprint, t.alias, v).then(reload, toast.error)} label="Accept without asking" />
                <button className="btn sm ghost danger" onClick={() => app.Untrust(t.fingerprint).then(reload, toast.error)}>
                  Remove
                </button>
              </div>
            </div>
          ))
        ) : (
          <p className="muted small">None yet. When accepting files, turn on “Remember this device as trusted”.</p>
        )}
      </section>

      <section className="card">
        <div className="card-head">
          <h2>Links</h2>
        </div>
        <div className="setting">
          <div>Default expiry for new links</div>
          <select style={{ width: 160 }} value={s.linkExpiry} onChange={(e) => save({ linkExpiry: Number(e.target.value) })}>
            {EXPIRY.map(([l, v]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </div>
      </section>

      <section className="card">
        <div className="card-head">
          <h2>About</h2>
        </div>
        <p className="muted small">
          Ferry {state.version} for Windows · works with Ferry for Android and LocalSend on any platform.
        </p>
        <div className="row" style={{ marginTop: 12 }}>
          <button className="btn sm" onClick={() => app.OpenURL("https://github.com/anand34577/ferry")}>
            <Icon name="globe" size={16} /> Website
          </button>
          <button className="btn sm ghost danger" onClick={() => app.Quit()}>
            Quit Ferry
          </button>
        </div>
      </section>
    </div>
  );
}
