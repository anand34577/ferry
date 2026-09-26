import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { CLIENT_API_VERSION, get, onUnauthorized, post, type Me, type ServerInfo, type User } from "./api";
import { setServerTime } from "./format";

interface AuthState {
  user: User | null;
  info: ServerInfo | null;
  loading: boolean;
  setupNeeded: boolean;
  compat: string | null; // human-readable version mismatch message
  me: Me | null;
  refreshMe: () => Promise<void>;
  refreshInfo: () => void;
  setUser: (u: User | null) => void;
  logout: () => Promise<void>;
  reload: () => void;
}

const Ctx = createContext<AuthState>(null!);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [me, setMe] = useState<Me | null>(null);
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
          const m = await get<Me>("/api/v1/me");
          if (alive) (setUser(m.user), setMe(m));
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

  const refreshMe = useCallback(() => get<Me>("/api/v1/me").then((m) => (setUser(m.user), setMe(m))), []);

  const logout = useCallback(async () => {
    await post("/api/v1/auth/logout").catch(() => {});
    setUser(null);
  }, []);

  return (
    <Ctx.Provider value={{ user, info, loading, setupNeeded, compat, me, refreshMe, refreshInfo: () => void get<ServerInfo>("/api/v1/info").then(setInfo).catch(() => {}), setUser: (u) => (setUser(u), u ? (setSetupNeeded(false), refreshMe().catch(() => {})) : setMe(null)), logout, reload: () => setTick((t) => t + 1) }}>
      {children}
    </Ctx.Provider>
  );
}

export const useAuth = () => useContext(Ctx);

// ---------- theme ----------
// [id, label, mode, swatch colours (background, surface, accent)]
export const THEMES = [
  ["system", "System", "auto", ["#f5f4f0", "#0e0f11", "#2456f5"]],
  ["light", "Light", "light", ["#f5f4f0", "#ffffff", "#2456f5"]],
  ["dark", "Dark", "dark", ["#0e0f11", "#17181b", "#7090ff"]],
  ["ocean", "Ocean", "light", ["#eef5f7", "#ffffff", "#0b7285"]],
  ["forest", "Forest", "light", ["#f1f4ef", "#ffffff", "#2b7a3d"]],
  ["sunset", "Sunset", "light", ["#faf3ee", "#ffffff", "#c2410c"]],
  ["rose", "Rose", "light", ["#fbf2f5", "#ffffff", "#be185d"]],
  ["sepia", "Sepia", "light", ["#f4ecd8", "#fbf6e9", "#8a5a19"]],
  ["midnight", "Midnight", "dark", ["#0b1020", "#141b33", "#8b9cff"]],
  ["nord", "Nord", "dark", ["#2e3440", "#3b4252", "#88c0d0"]],
  ["grape", "Grape", "dark", ["#16111f", "#211a2e", "#bd93f9"]],
  ["contrast", "High contrast", "dark", ["#000000", "#000000", "#ffd400"]],
] as const;
export type Theme = (typeof THEMES)[number][0];
export function getTheme(): Theme {
  try {
    const t = localStorage.getItem("ferry-theme");
    return THEMES.some((x) => x[0] === t) ? (t as Theme) : "system";
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
  const def = THEMES.find((x) => x[0] === t) ?? THEMES[0];
  const root = document.documentElement.dataset;
  if (def[0] === "system") delete root.theme;
  else root.theme = def[0];
  if (def[2] === "dark") root.mode = "dark";
  else delete root.mode;
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
