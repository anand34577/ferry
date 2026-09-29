package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"ferrydesktop/internal/ferry"
	"ferrydesktop/internal/lan"
)

// ---------- nearby devices ----------

// Discover makes discovery announce every few seconds while the Send screen is open.
func (a *App) Discover(active bool) {
	if a.disc != nil {
		a.disc.SetFast(active)
		if active {
			a.disc.Announce()
		}
	}
}

// Scan probes the local network for devices when multicast discovery is blocked.
func (a *App) Scan() {
	if a.disc != nil {
		go a.disc.Scan(context.Background())
	}
}

// Connect adds a device by IP address, pairing code or ferry://peer link.
func (a *App) Connect(input string) (lan.Peer, error) {
	if a.disc == nil {
		return lan.Peer{}, errNoLAN
	}
	t, err := lan.ParseTarget(input)
	if err != nil {
		return lan.Peer{}, err
	}
	var last error
	for _, h := range t.Hosts {
		p, err := a.disc.Connect(a.ctx, h, t.Port, t.Fingerprint, t.Source, t.HTTPS)
		if err == nil {
			return p, nil
		}
		last = err
	}
	return lan.Peer{}, friendlyPeer(last, "that device")
}

func (a *App) ForgetPeer(key string) {
	if a.disc != nil {
		a.disc.Remove(key)
	}
}

var errNoLAN = errors.New("nearby transfers aren't available: Ferry couldn't start its network service (see Receive)")

func friendlyPeer(err error, alias string) error {
	var pe *lan.PeerError
	var ne net.Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, lan.ErrIdentityChanged):
		return fmt.Errorf("%s's identity changed or couldn't be verified. For your safety the connection was refused", alias)
	case errors.As(err, &pe):
		return pe
	case errors.As(err, &ne):
		return fmt.Errorf("couldn't connect to %s. Make sure it's on the same network with Ferry or LocalSend open, and that the network allows devices to see each other", alias)
	}
	return fmt.Errorf("couldn't reach %s (%v)", alias, err)
}

// SendDirect sends files straight to a nearby device.
func (a *App) SendDirect(peerKey string, paths []string) (string, error) {
	if a.disc == nil || a.id == nil {
		return "", errNoLAN
	}
	p, ok := a.disc.Get(peerKey)
	if !ok {
		return "", errors.New("that device is no longer available — refresh the list")
	}
	files, err := expand(paths)
	if err != nil {
		return "", err
	}
	return a.sendDirect(p, files, ""), nil
}

type directState struct {
	session string
	tokens  map[string]string
	sent    map[string]string // file ID → SHA-256 sent
}

func (a *App) sendDirect(p lan.Peer, files []TFile, fallbackDevice string) string {
	t := &Transfer{ID: newID(), Direction: "sent", Method: "direct", Peer: p.Alias, Status: stConnecting, Files: files, CanPause: p.Ferry}
	a.store.add(t)
	c := a.store.control(t.ID)
	orig := append([]TFile(nil), files...)
	c.retry = func() { a.sendDirect(p, resetFiles(orig), fallbackDevice) }
	st := &directState{sent: map[string]string{}}
	c.restart = func() { go a.runDirect(t.ID, p, fallbackDevice, st) }
	c.restart()
	return t.ID
}

func resetFiles(fs []TFile) []TFile {
	out := make([]TFile, len(fs))
	for i, f := range fs {
		f.Done, f.ServerFileID = 0, ""
		out[i] = f
	}
	return out
}

func (a *App) runDirect(id string, peer lan.Peer, fallbackDevice string, st *directState) {
	ctx, cancel := context.WithCancel(context.Background())
	c := a.store.control(id)
	c.cancel = cancel
	defer cancel()
	err := func() error {
		a.store.status(id, stConnecting, "Connecting to "+peer.Alias+"…")
		// Pinned to the certificate seen when the device was found; a different certificate is refused.
		info, err := lan.FetchInfo(ctx, peer.IP, peer.Port, peer.CertPin, peer.Source, &peer.HTTPS, 6*time.Second)
		if err != nil {
			return err
		}
		peer.Ferry, peer.CertPin = info.Ferry, info.CertPin
		a.store.update(id, func(t *Transfer) { t.CanPause = peer.Ferry })
		t, _ := a.store.get(id)
		if st.session == "" {
			a.store.status(id, stWaiting, "Waiting for "+peer.Alias+" to accept…")
			meta := map[string]lan.FileMeta{}
			for _, f := range t.Files {
				meta[f.ID] = lan.FileMeta{ID: f.ID, FileName: f.Name, Size: f.Size, FileType: f.Mime}
			}
			self := lan.SelfInfo(a.settings.get().Alias, a.id.Fingerprint, a.port(), nil)
			pin := ""
			for {
				prep, err := lan.Prepare(ctx, peer, self, meta, pin)
				var pe *lan.PeerError
				if errors.As(err, &pe) && pe.Status == 401 {
					if pin, err = a.askPIN(ctx, id, peer.Alias, pin != ""); err != nil {
						return err
					}
					continue
				}
				if err != nil {
					return err
				}
				if prep == nil { // receiver needs nothing
					return nil
				}
				st.session, st.tokens = prep.SessionID, prep.Tokens
				break
			}
		}
		a.store.status(id, stTransferring, "")
		for _, f := range t.Files {
			tok, ok := st.tokens[f.ID]
			if !ok { // the receiver skipped it
				continue
			}
			if _, done := st.sent[f.ID]; done {
				continue
			}
			sha, err := a.sendFile(ctx, id, peer, st, f, tok)
			if err != nil {
				return err
			}
			st.sent[f.ID] = sha
		}
		if peer.Ferry {
			a.store.status(id, stVerifying, "Verifying integrity…")
			for fid, sha := range st.sent {
				if err := lan.Verify(ctx, peer, st.session, fid, st.tokens[fid], sha); err != nil {
					return err
				}
			}
		}
		return nil
	}()
	var pe *lan.PeerError
	switch {
	case err == nil:
		a.store.finish(id, stCompleted, "")
		a.notify("Sent to "+peer.Alias, summary(a.store, id))
	case c.paused:
		a.store.update(id, func(t *Transfer) { t.Status, t.Paused, t.Speed, t.Note = stInterrupted, true, 0, "Paused" })
	case ctx.Err() != nil || errors.Is(err, errCancelled):
		if st.session != "" {
			lan.Cancel(peer, st.session)
		}
		a.store.finish(id, stCancelled, "Cancelled")
	case errors.As(err, &pe) && pe.Status == 403:
		a.store.finish(id, stRejected, peer.Alias+" declined the files.")
	case errors.As(err, &pe) && pe.Status == 409 && st.session == "":
		a.store.finish(id, stFailed, peer.Alias+" is busy with another transfer. Try again in a moment.")
	case errors.As(err, &pe) && strings.Contains(strings.ToLower(pe.Message), "checksum"):
		a.store.finish(id, stFailed, "The file arrived corrupted (checksum mismatch). Please retry.")
	default:
		why := friendlyPeer(err, peer.Alias).Error() + "."
		if fallbackDevice != "" && st.session == "" && a.online {
			a.store.update(id, func(t *Transfer) { t.Method, t.Note = "server", why+" Sending through your server instead." })
			a.runRelay(id, fallbackDevice, peer.Alias)
			return
		}
		a.store.finish(id, stFailed, why)
	}
}

var errCancelled = errors.New("cancelled")

func (a *App) port() int {
	if a.recv != nil {
		return a.recv.Port
	}
	return lan.Port
}

// sendFile streams one file; with Ferry peers it reconnects and resumes from the receiver's offset.
func (a *App) sendFile(ctx context.Context, id string, peer lan.Peer, st *directState, f TFile, token string) (string, error) {
	resuming := len(st.sent) > 0 || f.Done > 0
	for attempt := 0; ; attempt++ {
		var offset int64
		if resuming && peer.Ferry {
			o, err := lan.Offset(ctx, peer, st.session, f.ID, token)
			if err == nil {
				offset = o
			}
		}
		sha, err := func() (string, error) {
			fh, err := os.Open(f.Path)
			if err != nil {
				return "", fmt.Errorf("can't read %q — it may have been moved or deleted", f.Name)
			}
			defer fh.Close()
			h := sha256.New()
			if _, err := io.CopyN(h, fh, offset); err != nil {
				return "", fmt.Errorf("can't read %q: %w", f.Name, err)
			}
			a.store.progress(id, f.ID, offset)
			body := &progressReader{r: io.TeeReader(io.LimitReader(fh, f.Size-offset), h), fn: func(n int64) { a.store.progress(id, f.ID, offset+n) }}
			if err := lan.Upload(ctx, peer, st.session, f.ID, token, offset, body, f.Size-offset); err != nil {
				return "", err
			}
			return hex.EncodeToString(h.Sum(nil)), nil
		}()
		if err == nil {
			if resuming {
				a.store.status(id, stTransferring, "")
			}
			return sha, nil
		}
		var pe *lan.PeerError
		if ctx.Err() != nil || (errors.As(err, &pe) && (pe.Status == 400 || pe.Status == 403 || pe.Status == 404)) || !peer.Ferry || attempt >= 12 ||
			strings.HasPrefix(err.Error(), "can't read") {
			return "", err
		}
		// The receiver keeps partial data for 10 minutes; wait and resume.
		resuming = true
		a.store.status(id, stInterrupted, fmt.Sprintf("Connection lost — reconnecting (attempt %d)…", attempt+1))
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(min(20, 1<<min(attempt, 5))) * time.Second):
		}
	}
}

type progressReader struct {
	r    io.Reader
	n    int64
	fn   func(int64)
	last time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if time.Since(p.last) > 120*time.Millisecond || err != nil {
		p.fn(p.n)
		p.last = time.Now()
	}
	return n, err
}

func (a *App) askPIN(ctx context.Context, id, alias string, wrong bool) (string, error) {
	ch := make(chan string, 1)
	a.mu.Lock()
	a.pinWait[id] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pinWait, id)
		a.mu.Unlock()
		wruntime.EventsEmit(a.ctx, "pin", nil)
	}()
	a.store.status(id, stWaiting, "PIN required by "+alias)
	wruntime.EventsEmit(a.ctx, "pin", map[string]any{"transferId": id, "peer": alias, "wrong": wrong})
	a.showWindow()
	select {
	case pin := <-ch:
		if pin == "" {
			return "", errCancelled
		}
		return pin, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Minute):
		return "", errCancelled
	}
}

func summary(s *store, id string) string {
	t, ok := s.find(id)
	if !ok {
		return ""
	}
	if len(t.Files) == 1 {
		return t.Files[0].Name
	}
	return fmt.Sprintf("%d files", len(t.Files))
}

// ---------- my devices (through my server, directly when nearby) ----------

// SendToDevice sends to one of my signed-in devices: directly when it is on this network, otherwise
// through my server, which keeps the files until the device is online.
func (a *App) SendToDevice(deviceID string, paths []string) (string, error) {
	files, err := expand(paths)
	if err != nil {
		return "", err
	}
	var dev *ferry.Device
	a.mu.Lock()
	for i := range a.devices {
		if a.devices[i].ID == deviceID {
			d := a.devices[i]
			dev = &d
		}
	}
	a.mu.Unlock()
	if dev == nil {
		return "", errors.New("that device is no longer linked to your account — refresh the list")
	}
	yes := true
	for _, ip := range dev.LanAddrs {
		if a.id == nil {
			break
		}
		port := dev.LanPort
		if port == 0 {
			port = lan.Port
		}
		ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
		p, err := lan.FetchInfo(ctx, ip, port, "", "server", &yes, 3*time.Second)
		cancel()
		if err == nil && (dev.Fingerprint == "" || strings.EqualFold(p.Fingerprint, dev.Fingerprint)) {
			p.Alias, p.AccountDeviceID = dev.Name, dev.ID
			return a.sendDirect(p, files, dev.ID), nil
		}
	}
	t := &Transfer{ID: newID(), Direction: "sent", Method: "server", Peer: dev.Name, Status: stCreated, Files: files, CanPause: true}
	a.store.add(t)
	a.runRelay(t.ID, dev.ID, dev.Name)
	return t.ID, nil
}

func (a *App) runRelay(id, deviceID, deviceName string) {
	c := a.store.control(id)
	t0, _ := a.store.get(id)
	orig := resetFiles(t0.Files)
	c.retry = func() {
		t := &Transfer{ID: newID(), Direction: "sent", Method: "server", Peer: deviceName, Status: stCreated, Files: resetFiles(orig), CanPause: true}
		a.store.add(t)
		a.runRelay(t.ID, deviceID, deviceName)
	}
	c.restart = func() { a.runRelay(id, deviceID, deviceName) }
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	a.store.update(id, func(t *Transfer) { t.CanPause = true })
	go func() {
		defer cancel()
		cl, acc, err := a.client()
		t, _ := a.store.get(id)
		serverID := t.ServerTransferID
		if err == nil && serverID == "" {
			serverID, err = cl.CreateTransfer(ctx, deviceID, len(t.Files), t.Total)
			a.store.update(id, func(t *Transfer) { t.ServerTransferID = serverID })
		}
		if err == nil {
			a.store.status(id, stTransferring, "Uploading to your server…")
			for _, f := range t.Files {
				if f.ServerFileID != "" {
					continue
				}
				fid, uerr := cl.Upload(ctx, f.Path, path.Base(f.Name), map[string]string{"transferId": serverID}, "tr:"+acc.ID+":"+serverID+":"+f.ID,
					resumeStore{a.settings}, func(n int64) { a.store.progress(id, f.ID, n) })
				if uerr != nil {
					err = uerr
					break
				}
				a.store.update(id, func(t *Transfer) { setServerFile(t, f.ID, fid) })
			}
		}
		if err == nil {
			err = cl.PatchTransfer(ctx, serverID, "waiting", -1, "")
		}
		switch {
		case err == nil:
			a.store.status(id, stWaiting, "On your server — "+deviceName+" gets it when it's online.")
			a.watchRelay(ctx, id, serverID)
		case c.paused:
			a.store.update(id, func(t *Transfer) {
				t.Status, t.Paused, t.Speed, t.Note = stInterrupted, true, 0, "Paused — resume any time"
			})
		case ctx.Err() != nil || errors.Is(err, ferry.ErrCancelled):
			if serverID != "" && cl != nil {
				bg, done := context.WithTimeout(context.Background(), 5*time.Second)
				cl.PatchTransfer(bg, serverID, stCancelled, -1, "")
				done()
			}
			a.store.finish(id, stCancelled, "Cancelled")
		default:
			a.store.finish(id, stFailed, err.Error()+" Your files are safe — you can retry.")
		}
	}()
}

func setServerFile(t *Transfer, fileID, serverID string) {
	for i := range t.Files {
		if t.Files[i].ID == fileID {
			t.Files[i].ServerFileID = serverID
		}
	}
}

// watchRelay follows a relayed transfer until the other device has it.
func (a *App) watchRelay(ctx context.Context, id, serverID string) {
	start := time.Now()
	for time.Since(start) < time.Hour {
		wait := 4 * time.Second
		if time.Since(start) > 5*time.Minute {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			a.store.finish(id, stCancelled, "Cancelled")
			return
		case <-time.After(wait):
		}
		cl, _, err := a.client()
		if err != nil {
			continue
		}
		tr, err := cl.GetTransfer(ctx, serverID)
		if err != nil {
			continue
		}
		if final(tr.Status) || tr.Status == "expired" {
			st := tr.Status
			if st == "expired" {
				st = stFailed
			}
			a.store.finish(id, st, tr.Error)
			return
		}
		if tr.Status == stTransferring {
			t, _ := a.store.get(id)
			a.store.status(id, stWaiting, t.Peer+" is downloading…")
		}
	}
	// Still undelivered after an hour: it stays on the server, which delivers it when the device comes online.
	a.store.finish(id, stCompleted, "")
}

// ---------- links ----------

type LinkOpts struct {
	ExpiresIn    int64  `json:"expiresIn"`
	MaxDownloads int    `json:"maxDownloads"`
	Password     string `json:"password"`
	Message      string `json:"message"`
}

// CreateLink uploads files to my server (into "Sent") and creates a share link anyone can open.
func (a *App) CreateLink(paths []string, o LinkOpts) (string, error) {
	if _, _, err := a.client(); err != nil {
		return "", err
	}
	if o.Password != "" && (len(o.Password) < 4 || len(o.Password) > 72) {
		return "", errors.New("link passwords must be 4–72 characters")
	}
	files, err := expand(paths)
	if err != nil {
		return "", err
	}
	name := "Link"
	if len(paths) == 1 {
		name = "Link · " + fileBase(paths[0])
	}
	t := &Transfer{ID: newID(), Direction: "sent", Method: "link", Peer: name, Status: stCreated, Files: files, CanPause: true}
	a.store.add(t)
	a.runLink(t.ID, o)
	return t.ID, nil
}

func (a *App) runLink(id string, o LinkOpts) {
	c := a.store.control(id)
	t0, _ := a.store.get(id)
	orig := resetFiles(t0.Files)
	c.retry = func() {
		t := &Transfer{ID: newID(), Direction: "sent", Method: "link", Peer: t0.Peer, Status: stCreated, Files: resetFiles(orig), CanPause: true}
		a.store.add(t)
		a.runLink(t.ID, o)
	}
	c.restart = func() { a.runLink(id, o) }
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		defer cancel()
		share, err := func() (*ferry.Share, error) {
			cl, acc, err := a.client()
			if err != nil {
				return nil, err
			}
			a.store.status(id, stTransferring, "Uploading to your server…")
			sent, err := cl.EnsureFolder(ctx, "Sent", "")
			if err != nil {
				return nil, err
			}
			t, _ := a.store.get(id)
			folders := map[string]string{"": sent} // relative folder → server folder ID
			var mu sync.Mutex
			folderFor := func(dir string) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				if f, ok := folders[dir]; ok {
					return f, nil
				}
				parent := sent
				cur := ""
				for _, part := range strings.Split(dir, "/") {
					cur = path.Join(cur, part)
					if f, ok := folders[cur]; ok {
						parent = f
						continue
					}
					f, err := cl.EnsureFolder(ctx, part, parent)
					if err != nil {
						return "", err
					}
					folders[cur], parent = f, f
				}
				return parent, nil
			}
			var fileIDs []string
			top := map[string]bool{}
			for _, f := range t.Files {
				dir := path.Dir(f.Name)
				if dir == "." {
					dir = ""
				}
				if dir != "" {
					top[strings.SplitN(dir, "/", 2)[0]] = true
				}
				if f.ServerFileID != "" {
					if dir == "" {
						fileIDs = append(fileIDs, f.ServerFileID)
					}
					continue
				}
				folder, err := folderFor(dir)
				if err != nil {
					return nil, err
				}
				fid, err := cl.Upload(ctx, f.Path, path.Base(f.Name), map[string]string{"folderId": folder, "conflict": "keep_both"},
					"link:"+acc.ID+":"+id+":"+f.ID, resumeStore{a.settings}, func(n int64) { a.store.progress(id, f.ID, n) })
				if err != nil {
					return nil, err
				}
				a.store.update(id, func(t *Transfer) { setServerFile(t, f.ID, fid) })
				if dir == "" {
					fileIDs = append(fileIDs, fid)
				}
			}
			var folderIDs []string
			for d := range top {
				folderIDs = append(folderIDs, folders[d])
			}
			a.store.status(id, stVerifying, "Creating the link…")
			return cl.CreateShare(ctx, fileIDs, folderIDs, ferry.ShareOpts{ExpiresIn: o.ExpiresIn, MaxDownloads: o.MaxDownloads, Password: o.Password, Message: o.Message})
		}()
		switch {
		case err == nil:
			a.store.update(id, func(t *Transfer) { t.ShareURL = share.Link() })
			a.store.finish(id, stCompleted, "")
			wruntime.EventsEmit(a.ctx, "link", map[string]string{"transferId": id, "url": share.Link()})
		case c.paused:
			a.store.update(id, func(t *Transfer) {
				t.Status, t.Paused, t.Speed, t.Note = stInterrupted, true, 0, "Paused — resume any time"
			})
		case ctx.Err() != nil || errors.Is(err, ferry.ErrCancelled):
			a.store.finish(id, stCancelled, "Cancelled")
		default:
			a.store.finish(id, stFailed, err.Error()+" Your files are safe — you can retry.")
		}
	}()
}

func fileBase(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}
