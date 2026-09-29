import { useState } from "react";
import { app, message } from "../api";
import { useApp } from "../App";
import { Icon } from "../Icon";
import { Empty, useAction, useToast } from "../ui";
import { ago } from "../format";

export function ServerPage() {
  const { state, setState } = useApp();
  const accounts = state.accounts ?? [];
  const acc = accounts.find((a) => a.active);
  const [adding, setAdding] = useState(false);
  const toast = useToast();
  const { run } = useAction();

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>My devices</h1>
          <p className="muted">Connect to your Ferry server to send to your devices anywhere and share links.</p>
        </div>
        {acc && !adding && (
          <button className="btn sm" onClick={() => setAdding(true)}>
            <Icon name="plus" size={16} /> Add server
          </button>
        )}
      </header>

      {(!acc || adding) && <SignIn onDone={() => setAdding(false)} onCancel={acc ? () => setAdding(false) : undefined} />}

      {acc && (
        <section className="card">
          <div className="card-head">
            <div className="row">
              <div className="t-ico">
                <Icon name="server" size={19} />
              </div>
              <div>
                <h2>{acc.siteName}</h2>
                <div className="muted small row" style={{ gap: 6 }}>
                  <span className={"dot" + (state.online ? " ok" : state.serverError ? " err" : " warn")} />
                  {state.online ? "Connected" : state.serverError ? "Not connected" : "Connecting…"} · {acc.email}
                  {!acc.url.startsWith("https://") && <span className="chip warn">Not encrypted (HTTP)</span>}
                </div>
              </div>
            </div>
            <div className="row">
              <button className="btn sm" onClick={() => app.OpenWebApp("files").catch(toast.error)}>
                <Icon name="folder" size={16} /> Files
              </button>
              <button className="btn sm" onClick={() => app.OpenWebApp("links").catch(toast.error)}>
                <Icon name="link" size={16} /> Links
              </button>
              {acc.isAdmin && (
                <button className="btn sm" onClick={() => app.OpenWebApp("admin").catch(toast.error)}>
                  <Icon name="shield" size={16} /> Admin
                </button>
              )}
            </div>
          </div>
          {state.serverError && (
            <div className="notice err" style={{ marginBottom: 12 }}>
              <Icon name="alert" size={18} />
              <div className="grow">{state.serverError}</div>
              {state.serverError.includes("Sign in again") && (
                <button className="btn sm" onClick={() => setAdding(true)}>
                  Sign in
                </button>
              )}
            </div>
          )}
          <h3 style={{ margin: "6px 0 10px" }}>Devices on this account</h3>
          {(state.devices ?? []).length ? (
            <div className="list">
              {(state.devices ?? []).map((d) => (
                <div key={d.id} className="t-item">
                  <div className="t-ico">
                    <Icon name={d.platform === "android" ? "phone" : "laptop"} size={19} />
                  </div>
                  <div className="grow">
                    <strong>{d.name}</strong>
                    <div className="t-meta">
                      <span className="row" style={{ gap: 6 }}>
                        <span className={"dot" + (d.online ? " ok" : "")} /> {d.online ? "Online" : `Seen ${ago(d.lastSeen)}`}
                      </span>
                      <span style={{ textTransform: "capitalize" }}>{d.platform}</span>
                      {d.lanAddrs?.length ? <span>On a local network</span> : null}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <Empty icon="phone" title="Only this PC so far" text="Sign in to the Ferry Android app (or Ferry on another PC) with the same account to send between your devices." />
          )}
        </section>
      )}

      {accounts.length > 0 && (
        <section className="card">
          <div className="card-head">
            <h2>Servers</h2>
          </div>
          <div className="list">
            {accounts.map((a) => (
              <div key={a.id} className="t-item">
                <div className="grow">
                  <div className="t-title">
                    <strong>{a.siteName}</strong>
                    {a.active && <span className="chip accent">In use</span>}
                  </div>
                  <div className="muted small ellipsis">
                    {a.email} · {a.url}
                  </div>
                </div>
                <div className="actions">
                  {!a.active && (
                    <button className="btn sm" onClick={() => run(() => app.SwitchAccount(a.id).then(setState))}>
                      Use
                    </button>
                  )}
                  <button className="btn sm ghost danger" onClick={() => run(() => app.SignOut(a.id).then(setState), "Signed out")}>
                    Sign out
                  </button>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function SignIn({ onDone, onCancel }: { onDone: () => void; onCancel?: () => void }) {
  const { setState } = useApp();
  const toast = useToast();
  const [step, setStep] = useState<"url" | "login">("url");
  const [url, setUrl] = useState("");
  const [server, setServer] = useState<{ url: string; siteName: string; sso: boolean; secure: boolean } | null>(null);
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const check = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr("");
    setBusy(true);
    try {
      setServer(await app.CheckServer(url));
      setStep("login");
    } catch (x) {
      setErr(message(x));
    } finally {
      setBusy(false);
    }
  };
  const login = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!server) return;
    setErr("");
    setBusy(true);
    try {
      setState(await app.SignIn(server.url, email, pw, needCode ? code : ""));
      toast.ok(`Signed in to ${server.siteName}`);
      onDone();
    } catch (x) {
      const m = message(x);
      if (/authenticator|6-digit|two-factor/i.test(m) && !needCode) setNeedCode(true);
      else setErr(m);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="card" style={{ maxWidth: 520 }}>
      {step === "url" ? (
        <form className="stack" onSubmit={check}>
          <div>
            <h2>Connect to your Ferry server</h2>
            <p className="muted small">The address you open Ferry at in a browser, e.g. files.example.com or 192.168.1.10:8080.</p>
          </div>
          {err && (
            <div className="notice err">
              <Icon name="alert" size={18} /> {err}
            </div>
          )}
          <label className="field">
            <span>Server address</span>
            <input type="text" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://files.example.com" autoFocus required spellCheck={false} />
          </label>
          <div className="row">
            <button className="btn primary" disabled={busy || !url.trim()}>
              {busy ? "Checking…" : "Continue"}
            </button>
            {onCancel && (
              <button type="button" className="btn ghost" onClick={onCancel}>
                Cancel
              </button>
            )}
          </div>
        </form>
      ) : (
        <form className="stack" onSubmit={login}>
          <div>
            <h2>Sign in to {server?.siteName}</h2>
            <p className="muted small">
              {server?.url}{" "}
              <button type="button" className="link-btn" onClick={() => (setStep("url"), setErr(""), setNeedCode(false))}>
                Change
              </button>
            </p>
          </div>
          {server && !server.secure && (
            <div className="notice warn">
              <Icon name="alert" size={18} />
              <span className="small">This server doesn't use HTTPS, so your password travels unencrypted. Fine on your home network; use HTTPS over the internet.</span>
            </div>
          )}
          {err && (
            <div className="notice err">
              <Icon name="alert" size={18} /> {err}
            </div>
          )}
          <label className="field">
            <span>Email</span>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" required autoFocus />
          </label>
          <label className="field" hidden={needCode}>
            <span>Password</span>
            <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" required />
          </label>
          {needCode && (
            <label className="field">
              <span>Two-factor code</span>
              <input type="text" inputMode="numeric" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" maxLength={7} required autoFocus />
              <span className="hint">The 6-digit code from your authenticator app.</span>
            </label>
          )}
          {server?.sso && (
            <p className="hint">
              Your server uses single sign-on. The app signs in with a password: if your account only uses single sign-on, set a password in the web app under Settings first.
            </p>
          )}
          <div className="row">
            <button className="btn primary" disabled={busy}>
              {busy ? "Signing in…" : "Sign in"}
            </button>
            {onCancel && (
              <button type="button" className="btn ghost" onClick={onCancel}>
                Cancel
              </button>
            )}
          </div>
        </form>
      )}
    </section>
  );
}
