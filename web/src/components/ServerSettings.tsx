import { useEffect, useState } from "react";
import { get, patch, post } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Icon } from "./Icon";
import { CopyField, ErrorBox, Loading, Switch, useToast } from "./ui";

interface Setting {
  key: string;
  env: string;
  group: string;
  label: string;
  help?: string;
  kind: "text" | "url" | "secret" | "bool" | "int" | "size" | "duration" | "choice" | "list";
  choices?: string[];
  value: string;
  hasValue: boolean;
  locked: boolean;
  overridden: boolean;
}

interface Data {
  settings: Setting[];
  oidcCallbackUrl: string;
}

const GROUPS: [string, string, string][] = [
  ["general", "General", "Name, address and who can use this server."],
  ["limits", "Limits", "Sizes accept KB, MB, GB or TB, e.g. 10GB. 0 means no limit."],
  ["email", "Email", "Used for link notifications, emailing links and password reset."],
  ["sso", "Single sign-on (OIDC)", "Let people sign in with Keycloak, Authentik, Google, Microsoft Entra ID or another OpenID Connect provider."],
  ["shortener", "Short links", "Give every link a short address through your Shortr URL shortener."],
  ["retention", "Retention", "Durations like 30d, 12h or 90m."],
];

const PLACEHOLDER: Partial<Record<Setting["kind"], string>> = { size: "e.g. 10GB", duration: "e.g. 30d", url: "https://…", list: "comma-separated" };

/** Admin → Settings: server settings that apply immediately, without a restart. */
export function ServerSettings() {
  const [data, setData] = useState<Data | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const { refreshInfo } = useAuth();
  const toast = useToast();

  const load = (d?: Data) => {
    const apply = (x: Data) => {
      setData(x);
      setDraft(Object.fromEntries(x.settings.map((s) => [s.key, s.kind === "secret" ? "" : s.value])));
    };
    if (d) return apply(d);
    get<Data>("/api/v1/admin/settings").then(apply).catch(setError);
  };
  useEffect(() => load(), []);

  if (error != null) return <ErrorBox error={error} onRetry={() => (setError(null), load())} />;
  if (!data) return <Loading />;

  const changedIn = (group: string) =>
    data.settings.filter((s) => s.group === group && !s.locked && (s.kind === "secret" ? draft[s.key] !== "" : draft[s.key] !== s.value));

  const save = async (group: string, body: Record<string, string | null>, msg = "Settings saved") => {
    setBusy(group);
    try {
      load(await patch<Data>("/api/v1/admin/settings", body));
      refreshInfo();
      toast.ok(msg);
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="settings-groups">
      <p className="muted small">
        Changes apply immediately — no restart needed. Settings given as <code>FERRY_…</code> environment variables are locked here; remove them from the environment to manage them in this page.
      </p>
      {GROUPS.map(([group, title, intro]) => {
        const fields = data.settings.filter((s) => s.group === group);
        const changed = changedIn(group);
        return (
          <section key={group} className="panel">
            <header className="panel-head">
              <h2>{title}</h2>
              {group === "email" && data.settings.find((s) => s.key === "smtp_host")?.value && (
                <button className="btn sm" onClick={() => post("/api/v1/admin/test-email").then(() => toast.ok("Test email sent to your address")).catch(toast.error)}>
                  <Icon name="mail" size={16} /> Send test email
                </button>
              )}
            </header>
            <p className="muted small">{intro}</p>
            {group === "sso" && (
              <div className="field">
                <span>Redirect URI to register in your provider</span>
                <CopyField value={data.oidcCallbackUrl} label="Redirect URI" />
                <small className="hint">
                  Set <strong>Public address</strong> under General first when Ferry runs behind a reverse proxy. Step-by-step guides:{" "}
                  <a href="https://github.com/anand34577/ferry/wiki/Single-Sign-On" target="_blank" rel="noreferrer">
                    Single sign-on
                  </a>
                  .
                </small>
              </div>
            )}
            <form
              className="form"
              onSubmit={(e) => {
                e.preventDefault();
                save(group, Object.fromEntries(changed.map((s) => [s.key, draft[s.key]])));
              }}
            >
              {fields.map((s) => (
                <SettingInput key={s.key} s={s} value={draft[s.key] ?? ""} onChange={(v) => setDraft({ ...draft, [s.key]: v })} onReset={() => save(group, { [s.key]: null }, `${s.label} reset to default`)} />
              ))}
              <div className="row-actions">
                <button className="btn primary" disabled={!changed.length || busy === group}>
                  {busy === group ? "Saving…" : "Save"}
                </button>
                {changed.length > 0 && (
                  <button type="button" className="btn" onClick={() => load(data)}>
                    Discard changes
                  </button>
                )}
              </div>
            </form>
          </section>
        );
      })}
    </div>
  );
}

function SettingInput({ s, value, onChange, onReset }: { s: Setting; value: string; onChange: (v: string) => void; onReset: () => void }) {
  const hint = (
    <>
      {s.help && <small className="hint">{s.help}</small>}
      {s.locked && (
        <small className="hint">
          <Icon name="lock" size={12} /> Set by <code>{s.env}</code> in the server environment.
        </small>
      )}
      {s.overridden && !s.locked && (
        <button type="button" className="link-btn small self-start" onClick={onReset}>
          Reset to default
        </button>
      )}
    </>
  );
  if (s.kind === "bool")
    return (
      <div className="field">
        <Switch checked={value === "true"} onChange={(v) => onChange(String(v))} disabled={s.locked} label={s.label} hint={s.help} />
        {s.locked && (
          <small className="hint">
            <Icon name="lock" size={12} /> Set by <code>{s.env}</code> in the server environment.
          </small>
        )}
        {s.overridden && !s.locked && (
          <button type="button" className="link-btn small self-start" onClick={onReset}>
            Reset to default
          </button>
        )}
      </div>
    );
  return (
    <label className="field">
      <span>{s.label}</span>
      {s.kind === "choice" ? (
        <select value={value} onChange={(e) => onChange(e.target.value)} disabled={s.locked}>
          {s.choices?.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
      ) : (
        <input
          type={s.kind === "secret" ? "password" : s.kind === "int" ? "number" : s.kind === "url" ? "url" : "text"}
          min={s.kind === "int" ? 0 : undefined}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={s.locked}
          placeholder={s.kind === "secret" ? (s.hasValue ? "Saved — leave empty to keep it" : "") : PLACEHOLDER[s.kind]}
          autoComplete={s.kind === "secret" ? "new-password" : "off"}
          spellCheck={false}
        />
      )}
      {hint}
    </label>
  );
}
