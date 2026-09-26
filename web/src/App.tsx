import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import type { ReactNode } from "react";
import { AuthProvider, useAuth } from "./lib/auth";
import { DialogProvider, EmptyState, Loading, ToastProvider } from "./components/ui";
import { Layout } from "./components/Layout";
import { AuthPage } from "./pages/Auth";
import { Dashboard } from "./pages/Dashboard";
import { Files } from "./pages/Files";
import { Links } from "./pages/Links";
import { Devices } from "./pages/Devices";
import { History } from "./pages/History";
import { Send } from "./pages/Send";
import { Receive } from "./pages/Receive";
import { Settings } from "./pages/Settings";
import { Admin } from "./pages/Admin";

function Guard({ children, admin }: { children: ReactNode; admin?: boolean }) {
  const { user, loading, setupNeeded, compat } = useAuth();
  const loc = useLocation();
  if (loading) return <Loading />;
  if (compat && !user) return <Compat />;
  if (!user) return <Navigate to={setupNeeded ? "/setup" : `/login?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />;
  if (admin && user.role !== "admin") return <Navigate to="/" replace />;
  return <>{children}</>;
}

function Compat() {
  const { compat, reload } = useAuth();
  return (
    <div className="auth">
      <div className="auth-card">
        <EmptyState icon="alert" title="Can't continue" text={compat ?? ""}>
          <button className="btn primary" onClick={() => (compat?.includes("Reload") ? window.location.reload() : reload())}>
            Try again
          </button>
        </EmptyState>
      </div>
    </div>
  );
}

function CompatBanner() {
  const { compat, user } = useAuth();
  return compat && user ? (
    <div className="banner" role="alert">
      {compat}
    </div>
  ) : null;
}

export default function App() {
  return (
    <BrowserRouter>
      <ToastProvider>
        <DialogProvider>
          <AuthProvider>
            <CompatBanner />
            <Routes>
              <Route path="/login" element={<AuthPage mode="login" />} />
              <Route path="/setup" element={<AuthPage mode="setup" />} />
              <Route path="/forgot" element={<AuthPage mode="forgot" />} />
              <Route path="/reset" element={<AuthPage mode="reset" />} />
              <Route
                element={
                  <Guard>
                    <Layout />
                  </Guard>
                }
              >
                <Route index element={<Dashboard />} />
                <Route path="files" element={<Files />} />
                <Route path="links" element={<Links />} />
                <Route path="devices" element={<Devices />} />
                <Route path="history" element={<History />} />
                <Route path="send" element={<Send />} />
                <Route path="receive" element={<Receive />} />
                <Route path="settings" element={<Settings />} />
                <Route
                  path="admin"
                  element={
                    <Guard admin>
                      <Admin />
                    </Guard>
                  }
                />
                <Route path="*" element={<EmptyState icon="alert" title="Page not found" text="That page doesn't exist." />} />
              </Route>
            </Routes>
          </AuthProvider>
        </DialogProvider>
      </ToastProvider>
    </BrowserRouter>
  );
}
