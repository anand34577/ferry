import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { CLIENT_API_VERSION, get, onUnauthorized, post, type ServerInfo, type User } from "./api";
import { setServerTime } from "./format";

interface AuthState {
  user: User | null;
  info: ServerInfo | null;
  loading: boolean;
  setupNeeded: boolean;
  compat: string | null; // human-readable version mismatch message
  setUser: (u: User | null) => void;
  logout: () => Promise<void>;
  reload: () => void;
}

const Ctx = createContext<AuthState>(null!);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [info, setInfo] = useState<ServerInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [setupNeeded, setSetupNeeded] = useState(false);
  const [compat, setCompat] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let alive = true;
    (async () => {
      setLoading(true);
      try {
        const i = await get<ServerInfo>("/api/v1/info");
        if (!alive) return;
        setInfo(i);
        setServerTime(i.serverTime);
        document.title = i.siteName;
        if (i.minClientApiVersion > CLIENT_API_VERSION) setCompat(`This page is older than the server (Ferry ${i.version}). Reload the page to update.`);
        else if (i.apiVersion < CLIENT_API_VERSION) setCompat(`The server runs an older Ferry (${i.version}). Ask the administrator to update it.`);
        try {
          const me = await get<{ user: User }>("/api/v1/me");
          if (alive) setUser(me.user);
        } catch {
          const s = await get<{ needed: boolean }>("/api/v1/setup").catch(() => ({ needed: false }));
          if (alive) {
            setUser(null);
            setSetupNeeded(s.needed);
          }
        }
      } catch {
        if (alive) setCompat("Can't reach the Ferry server. Check that it is running and try again.");
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => {
      alive = false;
    };
  }, [tick]);

  useEffect(() => onUnauthorized(() => setUser(null)), []);

  const logout = useCallback(async () => {
    await post("/api/v1/auth/logout").catch(() => {});
    setUser(null);
  }, []);

  return (
    <Ctx.Provider value={{ user, info, loading, setupNeeded, compat, setUser: (u) => (setUser(u), u && setSetupNeeded(false)), logout, reload: () => setTick((t) => t + 1) }}>
      {children}
    </Ctx.Provider>
  );
}

export const useAuth = () => useContext(Ctx);

// ---------- theme ----------
export type Theme = "system" | "light" | "dark";
export function getTheme(): Theme {
  try {
    return (localStorage.getItem("ferry-theme") as Theme) || "system";
  } catch {
    return "system";
  }
}
export function applyTheme(t: Theme) {
  try {
    localStorage.setItem("ferry-theme", t);
  } catch {
    /* private mode */
  }
  if (t === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
}

// Small persisted per-browser preferences.
export function pref<T>(key: string, def: T): T {
  try {
    const v = localStorage.getItem("ferry-pref-" + key);
    return v === null ? def : (JSON.parse(v) as T);
  } catch {
    return def;
  }
}
export function setPref(key: string, v: unknown) {
  try {
    localStorage.setItem("ferry-pref-" + key, JSON.stringify(v));
  } catch {
    /* ignore */
  }
}
