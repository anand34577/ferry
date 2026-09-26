import { useNavigate } from "react-router-dom";
import { del, get, patch, type Device } from "../lib/api";
import { formatDate, relativeTime } from "../lib/format";
import { useAuth } from "../lib/auth";
import { Icon } from "../components/Icon";
import { EmptyState, ErrorBox, Loading, Menu, QR, useAsync, useDialogs, useEvery, useToast } from "../components/ui";

export function Devices() {
  const { data, error, loading, reload } = useAsync(() => get<{ devices: Device[] }>("/api/v1/devices"), []);
  useEvery(reload, 20000);
  const { info } = useAuth();
  const toast = useToast();
  const dialogs = useDialogs();
  const navigate = useNavigate();
  const serverUrl = window.location.origin;
  const pairUrl = `ferry://server?url=${encodeURIComponent(serverUrl)}&name=${encodeURIComponent(info?.siteName ?? "Ferry")}`;

  const rename = async (d: Device) => {
    const name = await dialogs.prompt("Rename device", "Device name", d.name, "Rename");
    if (!name) return;
    try {
      await patch(`/api/v1/devices/${d.id}`, { name });
      reload();
    } catch (e) {
      toast.error(e);
    }
  };
  const revoke = async (d: Device) => {
    if (!(await dialogs.confirm(`Remove “${d.name}”?`, "The device is signed out immediately and can no longer access your files or receive transfers. You can sign in again on it later.", "Remove", true))) return;
    try {
      await del(`/api/v1/devices/${d.id}`);
      toast.ok("Device removed");
      reload();
    } catch (e) {
      toast.error(e);
    }
  };

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>Devices</h1>
          <p className="muted">Phones signed in to your account. Send files to them from anywhere.</p>
        </div>
      </header>
      {error != null && <ErrorBox error={error} onRetry={reload} />}
      {loading && !data && <Loading />}
      {data && data.devices.length === 0 && <EmptyState icon="phone" title="No devices yet" text="Sign in with the Ferry Android app to add your phone." />}
      <ul className="cards">
        {data?.devices.map((d) => (
          <li key={d.id} className="link-card">
            <div className="link-main">
              <div className="link-title">
                <Icon name={d.platform === "android" ? "phone" : "laptop"} size={20} />
                <strong className="ellipsis">{d.name}</strong>
                <span className={"chip " + (d.online ? "ok" : "muted")}>{d.online ? "Online" : "Offline"}</span>
              </div>
              <div className="link-meta muted small">
                <span>{d.platform}{d.appVersion ? ` · app ${d.appVersion}` : ""}</span>
                <span title={formatDate(d.lastSeen)}>last active {relativeTime(d.lastSeen)}</span>
                <span>added {relativeTime(d.createdAt)}</span>
                {d.online && d.lanAddrs?.length ? <span>LAN {d.lanAddrs.join(", ")}</span> : null}
              </div>
            </div>
            <div className="link-actions">
              <button className="btn sm primary" onClick={() => navigate(`/send?device=${d.id}`)}>
                <Icon name="send" size={16} /> Send files
              </button>
              <Menu
                items={[
                  { label: "Rename", icon: "edit", onClick: () => rename(d) },
                  { label: "Remove device", icon: "trash", danger: true, onClick: () => revoke(d) },
                ]}
              />
            </div>
          </li>
        ))}
      </ul>

      <section className="panel pair">
        <div>
          <h2>Add your phone</h2>
          <ol className="steps">
            <li>Install the Ferry app on Android.</li>
            <li>
              Tap <strong>Add server</strong> and scan this code — or enter <code>{serverUrl}</code>.
            </li>
            <li>Sign in with your account. The phone appears here.</li>
          </ol>
          <p className="muted small">
            Phones on the same Wi-Fi can also send files <em>directly</em> to each other, even without internet. Files you send from this browser to a phone travel through your server.
          </p>
        </div>
        <QR value={pairUrl} size={160} />
      </section>
    </div>
  );
}
