import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { del, get, patch, post, type SessionInfo, type User } from "../lib/api";
import { applyTheme, getTheme, pref, setPref, useAuth, type Theme } from "../lib/auth";
import { EXPIRY_OPTIONS, describeAgent, formatDate, relativeTime } from "../lib/format";
import { Icon, type IconName } from "../components/Icon";
import { CopyField, ErrorBox, QR, useAsync, useDialogs, useToast } from "../components/ui";

export function Settings() {
  const { user, info, setUser, logout } = useAuth();
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
      toast.ok("Password changed. Other devices were signed out.");
    } catch (err) {
      toast.error(err);
    }
  };

  const themes: [Theme, string, IconName][] = [
    ["system", "System", "monitor"],
    ["light", "Light", "sun"],
    ["dark", "Dark", "moon"],
  ];

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
        <div className="seg big" role="radiogroup" aria-label="Theme">
          {themes.map(([t, l, i]) => (
            <label key={t} className={theme === t ? "on" : ""}>
              <input type="radio" name="theme" checked={theme === t} onChange={() => (setTheme(t), applyTheme(t))} />
              <Icon name={i} size={18} /> {l}
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
        <h2>Change password</h2>
        <form className="form" onSubmit={changePw}>
          <label className="field">
            <span>Current password</span>
            <input type="password" value={cur} onChange={(e) => setCur(e.target.value)} autoComplete="current-password" required />
          </label>
          <label className="field">
            <span>New password</span>
            <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" required minLength={8} maxLength={72} />
          </label>
          <label className="field">
            <span>Repeat new password</span>
            <input type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" required minLength={8} maxLength={72} />
          </label>
          <button className="btn primary self-start">Change password</button>
        </form>
      </section>

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
