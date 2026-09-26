import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { del, get, patch, post, type SessionInfo, type User } from "../lib/api";
import { THEMES, applyTheme, getTheme, pref, setPref, useAuth, type Theme } from "../lib/auth";
import { EXPIRY_OPTIONS, describeAgent, formatDate, relativeTime } from "../lib/format";
import { Icon } from "../components/Icon";
import { CopyField, ErrorBox, QR, useAsync, useDialogs, useToast } from "../components/ui";

export function Settings() {
  const { user, info, setUser, logout, me, refreshMe } = useAuth();
  const hasPassword = me?.hasPassword !== false;
  const toast = useToast();
  const navigate = useNavigate();
  const [name, setName] = useState(user?.name ?? "");
  const [cur, setCur] = useState("");
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [theme, setTheme] = useState<Theme>(getTheme());
  const [expiry, setExpiry] = useState<number>(pref("defaultExpiry", 7 * 86400));

  const saveName = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const r = await patch<{ user: User }>("/api/v1/me", { name });
      setUser(r.user);
      toast.ok("Name updated");
    } catch (err) {
      toast.error(err);
    }
  };
  const changePw = async (e: React.FormEvent) => {
    e.preventDefault();
    if (pw !== pw2) return toast.error(new Error("The new passwords don't match."));
    try {
      await post("/api/v1/me/password", { current: cur, new: pw });
      setCur("");
      setPw("");
      setPw2("");
      toast.ok(hasPassword ? "Password changed. Other devices were signed out." : "Password set. You can now also sign in with your email and password.");
      refreshMe();
    } catch (err) {
      toast.error(err);
    }
  };

  return (
    <div className="page narrow-page">
      <header className="page-head">
        <h1>Settings</h1>
      </header>

      <nav className="mobile-only settings-nav">
        <button className="btn" onClick={() => navigate("/devices")}>
          <Icon name="phone" size={18} /> Devices
        </button>
        <button className="btn" onClick={() => navigate("/history")}>
          <Icon name="clock" size={18} /> History
        </button>
        {user?.role === "admin" && (
          <button className="btn" onClick={() => navigate("/admin")}>
            <Icon name="shield" size={18} /> Admin
          </button>
        )}
      </nav>

      <section className="panel">
        <h2>Appearance</h2>
        <div className="theme-grid" role="radiogroup" aria-label="Theme">
          {THEMES.map(([t, l, mode, [bg, surface, accent]]) => (
            <label key={t} className={"theme-opt" + (theme === t ? " on" : "")}>
              <input type="radio" name="theme" checked={theme === t} onChange={() => (setTheme(t), applyTheme(t))} />
              <span className="theme-swatch" style={{ background: t === "system" ? `linear-gradient(135deg, ${bg} 50%, ${surface} 50%)` : bg }} aria-hidden="true">
                <i style={{ background: t === "system" ? "#888" : surface }} />
                <b style={{ background: accent }} />
              </span>
              <span className="theme-name">
                {l}
                <small>{mode === "auto" ? "Follows your device" : mode === "dark" ? "Dark" : "Light"}</small>
              </span>
            </label>
          ))}
        </div>
      </section>

      <section className="panel">
        <h2>Sharing defaults</h2>
        <label className="field">
          <span>Default link expiry</span>
          <select value={expiry} onChange={(e) => (setExpiry(Number(e.target.value)), setPref("defaultExpiry", Number(e.target.value)))}>
            {EXPIRY_OPTIONS.map(([l, v]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
      </section>

      <section className="panel">
        <h2>Profile</h2>
        <form className="form" onSubmit={saveName}>
          <label className="field">
            <span>Email</span>
            <input value={user?.email ?? ""} disabled />
          </label>
          <label className="field">
            <span>Display name</span>
            <input value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} />
          </label>
          <button className="btn primary self-start">Save</button>
        </form>
      </section>

      <section className="panel">
        <h2>{hasPassword ? "Change password" : "Set a password"}</h2>
        <form className="form" onSubmit={changePw}>
          {!hasPassword && <p className="muted small">Your account signs in with {info?.oidc?.name ?? "single sign-on"}. A password also lets you sign in to the Ferry Android app.</p>}
          {hasPassword && (
            <label className="field">
              <span>Current password</span>
              <input type="password" value={cur} onChange={(e) => setCur(e.target.value)} autoComplete="current-password" required />
            </label>
          )}
          <label className="field">
            <span>New password</span>
            <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" required minLength={8} maxLength={72} />
          </label>
          <label className="field">
            <span>Repeat new password</span>
            <input type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" required minLength={8} maxLength={72} />
          </label>
          <button className="btn primary self-start">{hasPassword ? "Change password" : "Set password"}</button>
        </form>
      </section>

      {info?.oidc && <ConnectedAccounts provider={info.oidc.name} hasPassword={hasPassword} />}
      <Notifications />
      <TwoFactor />
      <Sessions />

      <section className="panel">
        <h2>About</h2>
        <p className="muted">
          {info?.siteName} · Ferry {info?.version}
        </p>
        <button className="btn mobile-only" onClick={() => logout().then(() => navigate("/login"))}>
          <Icon name="logout" size={18} /> Sign out
        </button>
      </section>
    </div>
  );
}

function TwoFactor() {
  const { user, setUser } = useAuth();
  const toast = useToast();
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [pw, setPw] = useState("");
  const refresh = async () => setUser((await get<{ user: User }>("/api/v1/me")).user);

  const start = async () => {
    try {
      setSetup(await post<{ secret: string; uri: string }>("/api/v1/me/totp/setup"));
      setCode("");
    } catch (e) {
      toast.error(e);
    }
  };
  const enable = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await post("/api/v1/me/totp/enable", { code });
      setSetup(null);
      await refresh();
      toast.ok("Two-factor sign-in is on");
    } catch (err) {
      toast.error(err);
    }
  };
  const disable = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await post("/api/v1/me/totp/disable", { password: pw });
      setPw("");
      await refresh();
      toast.ok("Two-factor sign-in is off");
    } catch (err) {
      toast.error(err);
    }
  };

  return (
    <section className="panel">
      <h2>Two-factor sign-in</h2>
      {user?.totpEnabled ? (
        <form className="form" onSubmit={disable}>
          <p className="muted">
            <Icon name="check" size={16} /> On. Signing in asks for a code from your authenticator app.
          </p>
          <label className="field">
            <span>Password (to turn it off)</span>
            <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" required />
          </label>
          <button className="btn self-start">Turn off</button>
        </form>
      ) : setup ? (
        <form className="form" onSubmit={enable}>
          <p className="muted">Scan this code with an authenticator app (e.g. Google Authenticator, Aegis, 1Password), then enter the 6-digit code it shows.</p>
          <QR value={setup.uri} size={180} />
          <CopyField value={setup.secret} label="Setup key" />
          <label className="field">
            <span>Code from the app</span>
            <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]{6,7}" maxLength={7} required />
          </label>
          <div className="row-actions">
            <button className="btn primary">Turn on</button>
            <button type="button" className="btn" onClick={() => setSetup(null)}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <>
          <p className="muted">Protect your account with a code from your phone in addition to your password. If you lose the phone, an administrator can turn it off for you.</p>
          <button className="btn self-start" onClick={start}>
            <Icon name="shield" size={18} /> Set up
          </button>
        </>
      )}
    </section>
  );
}

function Sessions() {
  const { data, error, reload } = useAsync(() => get<{ sessions: SessionInfo[] }>("/api/v1/me/sessions"), []);
  const toast = useToast();
  const dialogs = useDialogs();
  const revoke = async (s: SessionInfo) => {
    try {
      await del(`/api/v1/me/sessions/${s.id}`);
      toast.ok("Signed out");
      reload();
    } catch (e) {
      toast.error(e);
    }
  };
  const revokeOthers = async () => {
    if (!(await dialogs.confirm("Sign out everywhere else?", "Every other browser and app signed in to your account is signed out immediately.", "Sign out others", true))) return;
    try {
      await post("/api/v1/me/sessions/revoke-others");
      toast.ok("Other sessions signed out");
      reload();
    } catch (e) {
      toast.error(e);
    }
  };
  const others = data?.sessions.filter((s) => !s.current).length ?? 0;
  return (
    <section className="panel">
      <h2>Where you're signed in</h2>
      {error != null && <ErrorBox error={error} onRetry={reload} />}
      <ul className="rows">
        {data?.sessions.map((s) => (
          <li key={s.id} className="trow-item">
            <Icon name={s.deviceId ? "phone" : "monitor"} size={20} />
            <div className="grow">
              <div className="row-title">
                <span className="ellipsis">
                  <strong>{s.deviceName || describeAgent(s.userAgent)}</strong>
                  {s.current && <> <span className="chip ok">This browser</span></>}
                </span>
              </div>
              <div className="muted small row-meta">
                <span>{s.ip || "unknown IP"}</span>
                <span title={formatDate(s.lastSeen)}>active {relativeTime(s.lastSeen)}</span>
                <span>signed in {relativeTime(s.createdAt)}</span>
              </div>
            </div>
            {!s.current && (
              <button className="btn sm" onClick={() => revoke(s)}>
                Sign out
              </button>
            )}
          </li>
        ))}
      </ul>
      {others > 0 && (
        <button className="btn self-start" onClick={revokeOthers}>
          <Icon name="logout" size={18} /> Sign out everywhere else
        </button>
      )}
    </section>
  );
}

function Notifications() {
  const { me, refreshMe, info } = useAuth();
  const toast = useToast();
  const [url, setUrl] = useState(me?.gotifyUrl ?? "");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const configured = !!me?.gotifyConfigured;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await patch("/api/v1/me", token ? { gotifyUrl: url, gotifyToken: token } : { gotifyUrl: url });
      setToken("");
      await refreshMe();
      toast.ok(url ? "Gotify settings saved" : "Gotify notifications turned off");
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
    }
  };
  const test = async () => {
    setBusy(true);
    try {
      await post("/api/v1/me/gotify/test");
      toast.ok("Test message sent — check Gotify");
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="panel">
      <h2>Notifications</h2>
      <p className="muted small">
        Links with <strong>Notify me</strong> turned on alert you when files arrive or are downloaded
        {info?.capabilities.includes("email") ? " — by email, and" : ""} by push through your own <a href="https://gotify.net" target="_blank" rel="noreferrer">Gotify</a> server.
      </p>
      <form className="form" onSubmit={save}>
        <label className="field">
          <span>Gotify server</span>
          <input type="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://gotify.example.com" inputMode="url" />
        </label>
        <label className="field">
          <span>Application token {configured && <span className="chip ok">saved</span>}</span>
          <input type="password" value={token} onChange={(e) => setToken(e.target.value)} placeholder={configured ? "Leave empty to keep the saved token" : "Create an application in Gotify and paste its token"} autoComplete="off" required={!configured && !!url} />
        </label>
        <div className="row-actions">
          <button className="btn primary" disabled={busy}>
            Save
          </button>
          {configured && (
            <button type="button" className="btn" onClick={test} disabled={busy}>
              <Icon name="send" size={18} /> Send test
            </button>
          )}
        </div>
      </form>
    </section>
  );
}

interface Identity {
  id: string;
  email: string;
  provider: string;
  createdAt: number;
  lastLogin: number;
}

function ConnectedAccounts({ provider, hasPassword }: { provider: string; hasPassword: boolean }) {
  const { data, error, reload } = useAsync(() => get<{ identities: Identity[] }>("/api/v1/me/identities"), []);
  const [params, setParams] = useSearchParams();
  const toast = useToast();
  const dialogs = useDialogs();
  useEffect(() => {
    if (params.get("sso") === "connected") {
      toast.ok(`${provider} connected`);
      setParams({}, { replace: true });
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const remove = async (i: Identity) => {
    if (!(await dialogs.confirm(`Disconnect ${provider}?`, `You won't be able to sign in with ${i.email || provider} anymore until you connect it again.`, "Disconnect", true))) return;
    try {
      await del(`/api/v1/me/identities/${i.id}`);
      toast.ok(`${provider} disconnected`);
      reload();
    } catch (e) {
      toast.error(e);
    }
  };
  const list = data?.identities ?? [];
  return (
    <section className="panel">
      <h2>Connected accounts</h2>
      <p className="muted small">Sign in with {provider} instead of your password.</p>
      {error != null && <ErrorBox error={error} onRetry={reload} />}
      <ul className="rows">
        {list.map((i) => (
          <li key={i.id} className="trow-item">
            <Icon name="shield" size={20} />
            <div className="grow">
              <strong>{i.provider}</strong>
              <div className="muted small row-meta">
                <span>{i.email || "no email shared"}</span>
                <span>connected {relativeTime(i.createdAt)}</span>
                {i.lastLogin > 0 && <span>last used {relativeTime(i.lastLogin)}</span>}
              </div>
            </div>
            <button className="btn sm" onClick={() => remove(i)} disabled={!hasPassword && list.length === 1} title={!hasPassword && list.length === 1 ? "Set a password first" : undefined}>
              Disconnect
            </button>
          </li>
        ))}
      </ul>
      {data && list.length === 0 && (
        <a className="btn self-start" href="/api/v1/auth/oidc/start?mode=link">
          <Icon name="link" size={18} /> Connect {provider}
        </a>
      )}
    </section>
  );
}
