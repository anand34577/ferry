package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"ferrydesktop/internal/ferry"
	"ferrydesktop/internal/lan"
)

// lanHost connects the LAN receiver to the app. It is a separate type so its methods aren't exposed to the UI.
type lanHost struct{ a *App }

func (h *lanHost) Self() lan.Info {
	return lan.SelfInfo(h.a.settings.get().Alias, h.a.id.Fingerprint, h.a.port(), nil)
}

func (h *lanHost) Receiving() bool { return h.a.settings.get().Receiving }

func (h *lanHost) PIN() string {
	if s := h.a.settings.get(); s.RequirePIN {
		return s.PIN
	}
	return ""
}

func (h *lanHost) IsTrusted(fp string) bool {
	_, ok := h.a.settings.trusted(fp)
	return ok
}

func (h *lanHost) Seen(p lan.Peer) {
	if h.a.disc != nil {
		h.a.disc.Upsert(p)
	}
}

func (h *lanHost) Ask(in lan.Incoming) (bool, string) {
	s := h.a.settings.get()
	if t, ok := h.a.settings.trusted(in.Fingerprint); ok && in.Trusted && t.AutoAccept {
		return true, s.DownloadDir
	}
	d := h.a.ask(in, "direct")
	if d.accept && d.trust && in.Fingerprint != "" {
		h.a.SetTrusted(in.Fingerprint, in.Alias, false)
	}
	return d.accept, firstNonEmpty(d.dir, s.DownloadDir)
}

// ask shows an incoming request and waits up to 90 seconds for the decision.
func (a *App) ask(in lan.Incoming, method string) decision {
	p := &pendingIncoming{Incoming: in, Method: method, answer: make(chan decision, 1)}
	a.mu.Lock()
	a.incoming[in.ID] = p
	a.mu.Unlock()
	a.log.Info("incoming request", "from", in.Alias, "files", len(in.Files), "via", method)
	a.emitIncoming()
	a.showWindow()
	n := fmt.Sprintf("%d files", len(in.Files))
	if len(in.Files) == 1 {
		n = in.Files[0].Name
	}
	a.notify(in.Alias+" wants to send you files", n+" · open Ferry to accept")
	var d decision
	select {
	case d = <-p.answer:
	case <-time.After(90 * time.Second):
	}
	a.log.Info("incoming request answered", "from", in.Alias, "accept", d.accept)
	a.mu.Lock()
	delete(a.incoming, in.ID)
	a.mu.Unlock()
	a.emitIncoming()
	if d.accept {
		dir := firstNonEmpty(d.dir, a.settings.get().DownloadDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.log.Error("download folder", "err", err)
			return decision{}
		}
		d.dir = dir
	}
	return d
}

func (h *lanHost) RecvStarted(id, peer string, files []lan.IncomingFile, ids []string, dir string) {
	t := &Transfer{ID: id, Direction: "received", Method: "direct", Peer: peer, Status: stWaiting, Note: "Waiting for " + peer + " to start…", SaveDir: dir}
	for i, f := range files {
		t.Files = append(t.Files, TFile{ID: ids[i], Name: f.Name, Size: f.Size, Mime: lan.MimeOf(f.Name)})
	}
	h.a.store.add(t)
}

func (h *lanHost) RecvProgress(id, fileID string, n int64) { h.a.store.progress(id, fileID, n) }

func (h *lanHost) RecvStatus(id, status, note string) { h.a.store.status(id, status, note) }

func (h *lanHost) RecvFileSaved(id, fileID, p string) {
	h.a.store.update(id, func(t *Transfer) {
		for i := range t.Files {
			if t.Files[i].ID == fileID {
				t.Files[i].Path, t.Files[i].Done = p, t.Files[i].Size
			}
		}
	})
}

func (h *lanHost) RecvFinished(id, status, errMsg string) {
	t, ok := h.a.store.get(id)
	if !ok {
		return
	}
	h.a.store.finish(id, status, errMsg)
	if status == stCompleted {
		h.a.received(t)
	}
}

func (a *App) received(t Transfer) {
	a.notify("Received from "+t.Peer, summary(a.store, t.ID))
	if a.settings.get().OpenWhenDone {
		a.ShowInFolder(t.ID)
	}
}

// ---------- server accounts ----------

// client returns an API client for the active account.
func (a *App) client() (*ferry.Client, Account, error) {
	s := a.settings.get()
	if s.ActiveAccount == "" {
		return nil, Account{}, errors.New("sign in to your Ferry server first (Server tab)")
	}
	acc, err := a.settings.account(s.ActiveAccount)
	if err != nil {
		return nil, Account{}, err
	}
	tok, err := unseal(acc.TokenEnc)
	if err != nil {
		return nil, acc, err
	}
	return ferry.New(acc.URL, tok), acc, nil
}

// CheckServer validates an address before signing in; it tries HTTPS first, then HTTP for addresses
// typed without a scheme (a server on the local network).
func (a *App) CheckServer(input string) (map[string]any, error) {
	u, explicit := ferry.NormalizeURL(input)
	if u == "https://" || u == "http://" {
		return nil, errors.New("enter your server's address, e.g. https://files.example.com")
	}
	info, err := ferry.New(u, "").Info(a.ctx)
	if err != nil && !explicit {
		plain := "http://" + strings.TrimPrefix(u, "https://")
		if i2, err2 := ferry.New(plain, "").Info(a.ctx); err2 == nil {
			u, info, err = plain, i2, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"url": u, "siteName": info.SiteName, "version": info.Version, "sso": info.OIDC != nil, "secure": strings.HasPrefix(u, "https://")}, nil
}

// SignIn signs this PC in as a device. code is the two-factor code when the server asked for one
// (error code "totp_required").
func (a *App) SignIn(serverURL, email, password, code string) (State, error) {
	s := a.settings.get()
	var existing *Account
	for _, acc := range s.Accounts {
		if strings.EqualFold(acc.URL, serverURL) && strings.EqualFold(acc.Email, strings.TrimSpace(email)) {
			acc := acc
			existing = &acc
		}
	}
	deviceID := ""
	if existing != nil {
		deviceID = existing.DeviceID // re-login keeps the same device (its history and pairing)
	}
	c := ferry.New(serverURL, "")
	info, err := c.Info(a.ctx)
	if err != nil {
		return a.GetState(), err
	}
	r, err := c.Login(a.ctx, strings.TrimSpace(email), password, strings.TrimSpace(code), s.Alias, deviceID, a.version)
	if err != nil {
		return a.GetState(), err
	}
	enc, err := seal(r.Token)
	if err != nil {
		return a.GetState(), fmt.Errorf("couldn't store the sign-in securely: %w", err)
	}
	acc := Account{ID: newID(), URL: serverURL, SiteName: info.SiteName, Email: r.User.Email, UserName: r.User.Name, IsAdmin: r.User.Role == "admin",
		DeviceID: r.Device.ID, TokenEnc: enc}
	if existing != nil {
		acc.ID = existing.ID
	}
	err = a.settings.update(func(s *Settings) {
		out := s.Accounts[:0]
		for _, x := range s.Accounts {
			if x.ID != acc.ID {
				out = append(out, x)
			}
		}
		s.Accounts = append(out, acc)
		s.ActiveAccount = acc.ID
	})
	if err != nil {
		return a.GetState(), err
	}
	a.refreshServer()
	return a.GetState(), nil
}

func (a *App) SignOut(accountID string) (State, error) {
	acc, err := a.settings.account(accountID)
	if err != nil {
		return a.GetState(), err
	}
	if tok, err := unseal(acc.TokenEnc); err == nil {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		c := ferry.New(acc.URL, tok)
		c.ClearPresence(ctx)
		c.Logout(ctx)
		cancel()
	}
	a.settings.update(func(s *Settings) {
		out := s.Accounts[:0]
		for _, x := range s.Accounts {
			if x.ID != accountID {
				out = append(out, x)
			}
		}
		s.Accounts = out
		if s.ActiveAccount == accountID {
			s.ActiveAccount = ""
			if len(out) > 0 {
				s.ActiveAccount = out[0].ID
			}
		}
	})
	a.mu.Lock()
	a.devices, a.online, a.serverErr = nil, false, ""
	a.mu.Unlock()
	a.refreshServer()
	return a.GetState(), nil
}

func (a *App) SwitchAccount(accountID string) (State, error) {
	if _, err := a.settings.account(accountID); err != nil {
		return a.GetState(), err
	}
	a.settings.update(func(s *Settings) { s.ActiveAccount = accountID })
	a.mu.Lock()
	a.devices = nil
	a.mu.Unlock()
	a.refreshServer()
	return a.GetState(), nil
}

// OpenWebApp opens the server's web app (files, links, admin) in the browser.
func (a *App) OpenWebApp(page string) error {
	_, acc, err := a.client()
	if err != nil {
		return err
	}
	a.OpenURL(acc.URL + "/" + strings.TrimLeft(page, "/"))
	return nil
}

// RefreshDevices reloads my devices from the server.
func (a *App) RefreshDevices() State {
	a.refreshServer()
	return a.GetState()
}

var serverWake = make(chan struct{}, 1)

func (a *App) refreshServer() {
	select {
	case serverWake <- struct{}{}:
	default:
	}
}

// serverLoop keeps the device online on my server: presence for direct transfers, the device list and
// the inbox of files sent to this PC.
func (a *App) serverLoop() {
	lastPresence := time.Time{}
	for {
		a.syncServer(&lastPresence)
		select {
		case <-serverWake:
		case <-time.After(15 * time.Second):
		}
	}
}

func (a *App) syncServer(lastPresence *time.Time) {
	c, acc, err := a.client()
	if err != nil {
		a.setServer(nil, false, "")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	devs, err := c.Devices(ctx)
	if err != nil {
		var fe *ferry.Error
		msg := err.Error()
		if errors.As(err, &fe) && fe.Status == 401 {
			msg = "Your session on " + acc.SiteName + " ended. Sign in again."
			a.settings.update(func(s *Settings) {
				for i := range s.Accounts {
					if s.Accounts[i].ID == acc.ID {
						s.Accounts[i].TokenEnc = ""
					}
				}
			})
		}
		a.setServer(nil, false, msg)
		return
	}
	a.setServer(devs, true, "")
	if s := a.settings.get(); s.Receiving && a.recv != nil && a.id != nil && time.Since(*lastPresence) > 60*time.Second {
		var ips []string
		for _, ad := range lan.LocalAddrs() {
			if !ad.Virtual {
				ips = append(ips, ad.IP)
			}
		}
		if len(ips) > 0 && c.Presence(ctx, ips, a.recv.Port, a.id.Fingerprint) == nil {
			*lastPresence = time.Now()
		}
	}
	a.pollInbox(ctx, c, acc)
}

func (a *App) setServer(devs []ferry.Device, online bool, errMsg string) {
	a.mu.Lock()
	var others []ferry.Device
	for _, d := range devs {
		if !d.Current {
			others = append(others, d)
		}
	}
	changed := a.online != online || a.serverErr != errMsg || len(a.devices) != len(others)
	a.devices, a.online, a.serverErr = others, online, errMsg
	a.mu.Unlock()
	wruntime.EventsEmit(a.ctx, "server", map[string]any{"devices": others, "online": online, "error": errMsg})
	if changed {
		updateTray(a)
	}
}

// pollInbox picks up files sent to this PC from the web app or my other devices.
func (a *App) pollInbox(ctx context.Context, c *ferry.Client, acc Account) {
	list, err := c.Inbox(ctx)
	if err != nil {
		return
	}
	for _, tr := range list {
		a.mu.Lock()
		seen := a.inboxSeen[tr.ID]
		a.inboxSeen[tr.ID] = true
		a.mu.Unlock()
		if seen || len(tr.Files) == 0 {
			continue
		}
		go func(tr ferry.Transfer) {
			from := firstNonEmpty(tr.SourceDeviceName, "Web browser")
			in := lan.Incoming{ID: tr.ID, Alias: from, Model: acc.SiteName, Trusted: true}
			for _, f := range tr.Files {
				in.Files = append(in.Files, lan.IncomingFile{Name: f.Name, Size: f.Size})
				in.TotalBytes += f.Size
			}
			// A transfer this PC already accepted (e.g. before a restart) resumes without asking again.
			dir := a.settings.get().DownloadDir
			if tr.Status == stWaiting && !a.settings.get().AutoAcceptOwn {
				d := a.ask(in, "server")
				if !d.accept {
					bg, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					c.PatchTransfer(bg, tr.ID, stRejected, -1, "")
					cancel()
					return
				}
				dir = d.dir
			}
			os.MkdirAll(dir, 0o755)
			a.downloadInbox(tr, from, dir)
		}(tr)
	}
}

func (a *App) downloadInbox(tr ferry.Transfer, from, dir string) {
	t := &Transfer{ID: newID(), Direction: "received", Method: "server", Peer: from, Status: stConnecting, SaveDir: dir, CanPause: true, ServerTransferID: tr.ID}
	for _, f := range tr.Files {
		t.Files = append(t.Files, TFile{ID: f.ID, Name: f.Name, Size: f.Size, Mime: lan.MimeOf(f.Name), SHA256: f.SHA256, ServerFileID: f.ID})
	}
	a.store.add(t)
	var run func()
	run = func() {
		c := a.store.control(t.ID)
		c.restart = run
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel = cancel
		go func() {
			defer cancel()
			cl, _, err := a.client()
			if err == nil {
				cl.PatchTransfer(ctx, tr.ID, stTransferring, -1, "")
				a.store.status(t.ID, stTransferring, "")
				cur, _ := a.store.get(t.ID)
				for _, f := range cur.Files {
					if f.Path != "" {
						continue // saved before a pause
					}
					var saved string
					saved, err = a.downloadOne(ctx, cl, t.ID, dir, f)
					if err != nil {
						break
					}
					a.store.update(t.ID, func(t *Transfer) {
						for i := range t.Files {
							if t.Files[i].ID == f.ID {
								t.Files[i].Path = saved
							}
						}
					})
				}
			}
			bg, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			switch {
			case err == nil:
				cl.PatchTransfer(bg, tr.ID, stCompleted, t.Total, "")
				a.store.finish(t.ID, stCompleted, "")
				if ft, ok := a.store.find(t.ID); ok {
					a.received(ft)
				}
			case c.paused:
				a.store.update(t.ID, func(t *Transfer) { t.Status, t.Paused, t.Speed, t.Note = stInterrupted, true, 0, "Paused" })
			case ctx.Err() != nil || errors.Is(err, ferry.ErrCancelled):
				if cl != nil {
					cl.PatchTransfer(bg, tr.ID, stCancelled, -1, "")
				}
				a.store.finish(t.ID, stCancelled, "Cancelled")
			default:
				if cl != nil {
					cl.PatchTransfer(bg, tr.ID, stInterrupted, -1, err.Error())
				}
				a.store.finish(t.ID, stFailed, err.Error()+" Partial data is kept — retry to resume.")
			}
		}()
	}
	a.store.control(t.ID).retry = func() {
		a.mu.Lock()
		delete(a.inboxSeen, tr.ID)
		a.mu.Unlock()
		a.downloadInbox(tr, from, dir)
	}
	run()
}

// downloadOne downloads into a ".ferrypart" file (kept across pauses) and renames it when verified.
func (a *App) downloadOne(ctx context.Context, cl *ferry.Client, id, dir string, f TFile) (string, error) {
	dirs, name := lan.SafeRelativePath(f.Name)
	target := filepath.Join(append([]string{dir}, dirs...)...)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	part := filepath.Join(target, name+"."+f.ServerFileID[:min(8, len(f.ServerFileID))]+".ferrypart")
	if err := cl.Download(ctx, f.ServerFileID, part, f.Size, f.SHA256, func(n int64) { a.store.progress(id, f.ID, n) }); err != nil {
		return "", err
	}
	final, err := uniquePath(filepath.Join(target, name))
	if err != nil {
		return "", err
	}
	return final, os.Rename(part, final)
}

func uniquePath(p string) (string, error) {
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for i := 1; i < 10000; i++ {
		c := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(c); errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
	}
	return "", fmt.Errorf("too many files named %s", path.Base(p))
}
