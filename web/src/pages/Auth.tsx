import { useState } from "react";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, post, type User } from "../lib/api";
import { useAuth } from "../lib/auth";
import { ErrorBox } from "../components/ui";

// Handles first-run setup, sign-in and (if enabled) sign-up.
export function AuthPage({ mode }: { mode: "login" | "setup" | "forgot" | "reset" }) {
  if (mode === "forgot" || mode === "reset") return <PasswordReset mode={mode} />;
  return <SignIn mode={mode} />;
}

function SignIn({ mode }: { mode: "login" | "setup" }) {
  const { user, info, setupNeeded, setUser } = useAuth();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [signup, setSignup] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [needCode, setNeedCode] = useState(false);
  const [code, setCode] = useState("");

  const isSetup = mode === "setup" || setupNeeded;
  const next = params.get("next");
  // Only same-site relative paths are followed (no open redirects).
  const safeNext = next && next.startsWith("/") && !next.startsWith("//") ? next : "/";

  if (user) {
    if (safeNext.startsWith("/s/") || safeNext.startsWith("/u/")) {
      window.location.replace(safeNext); // server-rendered share pages
      return null;
    }
    return <Navigate to={safeNext} replace />;
  }
  if (mode === "setup" && !setupNeeded) return <Navigate to="/login" replace />;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if ((isSetup || signup) && password !== confirm) {
      setError(new Error("The passwords don't match."));
      return;
    }
    setBusy(true);
    try {
      const path = isSetup ? "/api/v1/setup" : signup ? "/api/v1/auth/signup" : "/api/v1/auth/login";
      const res = await post<{ user: User }>(path, { email, password, name, ...(needCode ? { code } : {}) });
      setUser(res.user);
      if (safeNext.startsWith("/s/") || safeNext.startsWith("/u/")) window.location.replace(safeNext);
      else navigate(safeNext, { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.code === "totp_required") setNeedCode(true);
      else setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth">
      <div className="auth-card">
        <div className="brand big">
          <span className="logo" aria-hidden="true" />
          <span>{info?.siteName ?? "Ferry"}</span>
        </div>
        <h1>{isSetup ? "Welcome to Ferry" : signup ? "Create your account" : "Sign in"}</h1>
        <p className="muted">
          {isSetup
            ? "Create the administrator account for this server. You can add more people later."
            : "Send files to anyone, anywhere — with or without the internet."}
        </p>
        {error != null && <ErrorBox error={error} />}
        <form onSubmit={submit} className="form">
          {(isSetup || signup) && (
            <label className="field">
              <span>Your name</span>
              <input value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" required maxLength={100} />
            </label>
          )}
          <label className="field">
            <span>Email</span>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required autoFocus />
          </label>
          {needCode && (
            <label className="field">
              <span>Two-factor code</span>
              <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]{6,7}" required autoFocus maxLength={7} />
              <small className="hint">Open your authenticator app and enter the 6-digit code for {info?.siteName ?? "Ferry"}.</small>
            </label>
          )}
          <label className="field" hidden={needCode}>
            <span>Password</span>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={isSetup || signup ? "new-password" : "current-password"} required minLength={isSetup || signup ? 8 : 1} maxLength={72} />
          </label>
          {(isSetup || signup) && (
            <label className="field">
              <span>Confirm password</span>
              <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required minLength={8} maxLength={72} />
              <small className="hint">At least 8 characters.</small>
            </label>
          )}
          <button className="btn primary block" disabled={busy}>
            {busy ? "Please wait…" : isSetup ? "Create admin account" : signup ? "Create account" : "Sign in"}
          </button>
        </form>
        {!isSetup && info?.allowSignup && (
          <p className="center muted">
            {signup ? "Already have an account? " : "New here? "}
            <button className="link-btn" onClick={() => setSignup(!signup)}>
              {signup ? "Sign in" : "Create an account"}
            </button>
          </p>
        )}
        {!isSetup && !signup && info?.capabilities.includes("password-reset") && (
          <p className="center muted small">
            <Link to="/forgot">Forgot your password?</Link>
          </p>
        )}
        {!isSetup && !info?.allowSignup && <p className="center muted small">Accounts are created by the server administrator.</p>}
      </div>
    </div>
  );
}

function PasswordReset({ mode }: { mode: "forgot" | "reset" }) {
  const { info } = useAuth();
  const [params] = useSearchParams();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (mode === "reset" && password !== confirm) return setError(new Error("The passwords don't match."));
    setBusy(true);
    try {
      if (mode === "forgot") await post("/api/v1/auth/forgot", { email });
      else await post("/api/v1/auth/reset", { token: params.get("token") ?? "", password });
      setDone(true);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth">
      <div className="auth-card">
        <div className="brand big">
          <span className="logo" aria-hidden="true" />
          <span>{info?.siteName ?? "Ferry"}</span>
        </div>
        <h1>{mode === "forgot" ? "Reset your password" : "Choose a new password"}</h1>
        {done ? (
          <>
            <p className="muted" role="status">
              {mode === "forgot"
                ? "If an account exists for that email, we've sent a link to reset the password. It works for one hour."
                : "Your password was changed and you were signed out everywhere. Sign in with the new password."}
            </p>
            <Link className="btn primary block" to="/login">
              Back to sign in
            </Link>
          </>
        ) : (
          <>
            {error != null && <ErrorBox error={error} />}
            <form onSubmit={submit} className="form">
              {mode === "forgot" ? (
                <label className="field">
                  <span>Email</span>
                  <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required autoFocus />
                </label>
              ) : (
                <>
                  <label className="field">
                    <span>New password</span>
                    <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required minLength={8} maxLength={72} autoFocus />
                  </label>
                  <label className="field">
                    <span>Repeat new password</span>
                    <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required minLength={8} maxLength={72} />
                    <small className="hint">At least 8 characters.</small>
                  </label>
                </>
              )}
              <button className="btn primary block" disabled={busy}>
                {busy ? "Please wait…" : mode === "forgot" ? "Send reset link" : "Change password"}
              </button>
            </form>
            <p className="center muted small">
              <Link to="/login">Back to sign in</Link>
            </p>
          </>
        )}
      </div>
    </div>
  );
}
