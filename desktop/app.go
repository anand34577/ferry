package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"ferrydesktop/internal/ferry"
	"ferrydesktop/internal/lan"
)

// App is the bridge between the window (React UI) and the transfer engine. Every exported method is
// callable from the UI; results and errors are shown to people, so errors are written for them.
type App struct {
	ctx      context.Context
	version  string
	log      *slog.Logger
	dataDir  string
	settings *settingsStore
	id       *lan.Identity
	recv     *lan.Server
	disc     *lan.Discovery
	store    *store
	quitting bool

	mu        sync.Mutex
	incoming  map[string]*pendingIncoming
	pinWait   map[string]chan string
	pending   []string // files handed to Ferry (Send to, command line) before the UI was ready
	uiReady   bool
	devices   []ferry.Device
	online    bool
	inboxSeen map[string]bool
	serverErr string
}

type pendingIncoming struct {
	Incoming lan.Incoming `json:"request"`
	Method   string       `json:"method"` // direct | server
	answer   chan decision
}

type decision struct {
	accept, trust bool
	dir           string
}

func NewApp(version, dataDir string, log *slog.Logger) *App {
	return &App{version: version, dataDir: dataDir, log: log, settings: loadSettings(dataDir), incoming: map[string]*pendingIncoming{},
		pinWait: map[string]chan string{}, inboxSeen: map[string]bool{}}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.log.Info("starting", "version", a.version)
	a.store = newStore(a.dataDir, func(l []Transfer) { wruntime.EventsEmit(ctx, "transfers", l) })
	var err error
	if a.id, err = lan.LoadIdentity(a.dataDir); err != nil {
		a.log.Error("lan identity", "err", err)
	}
	host := &lanHost{a}
	if a.id != nil {
		if a.recv, err = lan.Listen(a.id, host, a.log); err != nil {
			a.log.Error("receiver", "err", err)
		}
		a.disc = lan.NewDiscovery(a.id, host.Self, host.Receiving, func() { wruntime.EventsEmit(ctx, "peers", a.disc.Peers()) }, a.log)
		a.disc.Start()
	}
	a.log.Info("network ready", "port", a.port())
	wruntime.OnFileDrop(ctx, func(_, _ int, paths []string) { a.handOver(paths) })
	// Slow or blocking setup (COM, tray message loop, server sync) runs off the UI thread.
	go func() {
		if err := wruntime.InitializeNotifications(ctx); err != nil {
			a.log.Warn("notifications unavailable", "err", err)
		}
		a.log.Info("notifications ready")
	}()
	go a.serverLoop()
	startTray(a, trayIcon)
	a.log.Info("startup done")
}

func (a *App) shutdown(context.Context) {
	if a.disc != nil {
		a.disc.Stop()
	}
	if a.recv != nil {
		a.recv.Close()
	}
	if c, _, err := a.client(); err == nil && a.online {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		c.ClearPresence(ctx)
		cancel()
	}
}

// beforeClose hides the window instead of quitting when "keep running in the tray" is on.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting || !a.settings.get().CloseToTray {
		return false
	}
	wruntime.WindowHide(ctx)
	return true
}

// showWindow brings the window forward. It runs in the background: window calls wait for the UI
// thread, and a transfer must never wait on the UI.
func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	go func() {
		wruntime.WindowShow(a.ctx)
		wruntime.WindowUnminimise(a.ctx)
		wruntime.WindowSetAlwaysOnTop(a.ctx, true) // brings it in front of other windows without keeping it there
		wruntime.WindowSetAlwaysOnTop(a.ctx, false)
	}()
}

// handOver receives files from Explorer's "Send to → Ferry", the command line or drag and drop.
func (a *App) handOver(paths []string) {
	var ok []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			ok = append(ok, p)
		}
	}
	if len(ok) == 0 {
		return
	}
	a.mu.Lock()
	ready := a.uiReady
	if !ready {
		a.pending = append(a.pending, ok...)
	}
	a.mu.Unlock()
	if ready {
		wruntime.EventsEmit(a.ctx, "files", ok)
		a.showWindow()
	}
}

func (a *App) notify(title, body string) {
	if a.ctx == nil || !a.settings.get().Notifications {
		return
	}
	go func() { // toasts go through COM and can be slow; never hold up a transfer for them
		if err := wruntime.SendNotification(a.ctx, wruntime.NotificationOptions{ID: newID(), Title: title, Body: body}); err != nil {
			a.log.Warn("notification", "err", err)
		}
	}()
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- state for the UI ----------

type Identity struct {
	Fingerprint string          `json:"fingerprint"`
	Port        int             `json:"port"`
	Addrs       []lan.LocalAddr `json:"addrs"`
	PairCode    string          `json:"pairCode"`
	QR          string          `json:"qr"`
	Listening   bool            `json:"listening"`
	Error       string          `json:"error,omitempty"`
}

type AccountView struct {
	Account
	Active bool `json:"active"`
}

type State struct {
	Version   string            `json:"version"`
	Settings  Settings          `json:"settings"`
	Identity  Identity          `json:"identity"`
	Peers     []lan.Peer        `json:"peers"`
	Scanning  bool              `json:"scanning"`
	Transfers []Transfer        `json:"transfers"`
	Incoming  []pendingIncoming `json:"incoming"`
	Accounts  []AccountView     `json:"accounts"`
	Devices   []ferry.Device    `json:"devices"`
	Online    bool              `json:"online"`
	ServerErr string            `json:"serverError"`
	Pending   []string          `json:"pending"` // files to send that arrived before the UI loaded
}

// GetState returns everything the UI shows; later changes arrive as events.
func (a *App) GetState() State {
	s := a.settings.get()
	s.Resume = nil
	for i := range s.Accounts {
		s.Accounts[i].TokenEnc = ""
	}
	st := State{Version: a.version, Settings: s, Identity: a.identity(), Transfers: a.store.list()}
	if a.disc != nil {
		st.Peers, st.Scanning = a.disc.Peers(), a.disc.Scanning()
	}
	for _, acc := range s.Accounts {
		st.Accounts = append(st.Accounts, AccountView{acc, acc.ID == s.ActiveAccount})
	}
	a.mu.Lock()
	for _, p := range a.incoming {
		st.Incoming = append(st.Incoming, *p)
	}
	st.Devices, st.Online, st.ServerErr = a.devices, a.online, a.serverErr
	st.Pending, a.pending, a.uiReady = a.pending, nil, true
	a.mu.Unlock()
	return st
}

func (a *App) identity() Identity {
	id := Identity{Addrs: lan.LocalAddrs()}
	if a.id == nil {
		id.Error = "Ferry couldn't create its network identity."
		return id
	}
	id.Fingerprint = a.id.Fingerprint
	if a.recv == nil {
		id.Error = "Ferry couldn't open a network port for receiving (53317–53329 are all in use)."
		return id
	}
	id.Port, id.Listening = a.recv.Port, true
	var ips []string
	for _, ad := range id.Addrs {
		if !ad.Virtual {
			ips = append(ips, ad.IP)
		}
	}
	if len(ips) > 0 {
		id.PairCode = lan.EncodePair(ips[0], id.Port)
		id.QR = lan.QRLink(ips, id.Port, a.id.Fingerprint, a.settings.get().Alias)
	}
	return id
}

func (a *App) emitIncoming() {
	a.mu.Lock()
	list := []pendingIncoming{}
	for _, p := range a.incoming {
		list = append(list, *p)
	}
	a.mu.Unlock()
	wruntime.EventsEmit(a.ctx, "incoming", list)
}

// ---------- settings ----------

type SettingsPatch struct {
	Alias            *string `json:"alias"`
	DownloadDir      *string `json:"downloadDir"`
	Receiving        *bool   `json:"receiving"`
	RequirePIN       *bool   `json:"requirePin"`
	PIN              *string `json:"pin"`
	AskWhereToSave   *bool   `json:"askWhereToSave"`
	OpenWhenDone     *bool   `json:"openWhenDone"`
	AutoAcceptOwn    *bool   `json:"autoAcceptOwn"`
	Theme            *string `json:"theme"`
	Notifications    *bool   `json:"notifications"`
	CloseToTray      *bool   `json:"closeToTray"`
	StartWithWindows *bool   `json:"startWithWindows"`
	LinkExpiry       *int64  `json:"linkExpiry"`
	Onboarded        *bool   `json:"onboarded"`
}

func (a *App) SaveSettings(p SettingsPatch) (State, error) {
	if p.Alias != nil {
		v := strings.TrimSpace(*p.Alias)
		if v == "" || len([]rune(v)) > 40 {
			return a.GetState(), errors.New("the device name must be 1–40 characters")
		}
		p.Alias = &v
	}
	if p.PIN != nil {
		v := strings.TrimSpace(*p.PIN)
		if v != "" && (len(v) < 4 || len(v) > 12) {
			return a.GetState(), errors.New("the PIN must be 4–12 characters")
		}
		p.PIN = &v
	}
	if p.DownloadDir != nil {
		if err := os.MkdirAll(*p.DownloadDir, 0o755); err != nil {
			return a.GetState(), fmt.Errorf("can't use that folder: %w", err)
		}
	}
	if p.Theme != nil && *p.Theme != "system" && *p.Theme != "light" && *p.Theme != "dark" {
		return a.GetState(), errors.New("unknown theme")
	}
	if p.StartWithWindows != nil {
		if err := setAutostart(*p.StartWithWindows); err != nil {
			return a.GetState(), fmt.Errorf("couldn't change the startup setting: %w", err)
		}
	}
	err := a.settings.update(func(s *Settings) {
		set := func(dst *bool, v *bool) {
			if v != nil {
				*dst = *v
			}
		}
		if p.Alias != nil {
			s.Alias = *p.Alias
		}
		if p.DownloadDir != nil {
			s.DownloadDir = *p.DownloadDir
		}
		if p.PIN != nil {
			s.PIN = *p.PIN
		}
		if p.Theme != nil {
			s.Theme = *p.Theme
		}
		if p.LinkExpiry != nil && *p.LinkExpiry >= 0 {
			s.LinkExpiry = *p.LinkExpiry
		}
		set(&s.Receiving, p.Receiving)
		set(&s.RequirePIN, p.RequirePIN)
		set(&s.AskWhereToSave, p.AskWhereToSave)
		set(&s.OpenWhenDone, p.OpenWhenDone)
		set(&s.AutoAcceptOwn, p.AutoAcceptOwn)
		set(&s.Notifications, p.Notifications)
		set(&s.CloseToTray, p.CloseToTray)
		set(&s.StartWithWindows, p.StartWithWindows)
		set(&s.Onboarded, p.Onboarded)
		if s.RequirePIN && s.PIN == "" {
			s.RequirePIN = false
		}
	})
	if err != nil {
		return a.GetState(), fmt.Errorf("couldn't save settings: %w", err)
	}
	if p.Alias != nil || p.Receiving != nil {
		if a.disc != nil {
			a.disc.Announce()
		}
		updateTray(a)
	}
	return a.GetState(), nil
}

func (a *App) SetTrusted(fingerprint, alias string, autoAccept bool) error {
	return a.settings.update(func(s *Settings) {
		for i := range s.Trusted {
			if strings.EqualFold(s.Trusted[i].Fingerprint, fingerprint) {
				s.Trusted[i].AutoAccept = autoAccept
				return
			}
		}
		s.Trusted = append(s.Trusted, TrustedDevice{Fingerprint: fingerprint, Alias: alias, AutoAccept: autoAccept})
	})
}

func (a *App) Untrust(fingerprint string) error {
	return a.settings.update(func(s *Settings) {
		out := s.Trusted[:0]
		for _, t := range s.Trusted {
			if !strings.EqualFold(t.Fingerprint, fingerprint) {
				out = append(out, t)
			}
		}
		s.Trusted = out
	})
}

// ---------- files ----------

func (a *App) ChooseFiles() ([]string, error) {
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Choose files to send"})
}

func (a *App) ChooseFolder(title string) (string, error) {
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: firstNonEmpty(title, "Choose a folder")})
}

// Item summarizes a chosen file or folder for the UI.
type Item struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Files int    `json:"files"`
	IsDir bool   `json:"isDir"`
	Error string `json:"error,omitempty"`
}

func (a *App) Describe(paths []string) []Item {
	var out []Item
	for _, p := range paths {
		it := Item{Path: p, Name: filepath.Base(p)}
		files, err := expand([]string{p})
		if err != nil {
			it.Error = err.Error()
		}
		st, _ := os.Stat(p)
		it.IsDir = st != nil && st.IsDir()
		for _, f := range files {
			it.Size += f.Size
		}
		it.Files = len(files)
		out = append(out, it)
	}
	return out
}

// skipName leaves out files Windows and macOS create by themselves.
var skipName = map[string]bool{"desktop.ini": true, "thumbs.db": true, ".ds_store": true}

// expand turns chosen files and folders into files with relative names ("Folder/sub/a.jpg").
func expand(paths []string) ([]TFile, error) {
	var out []TFile
	seen := map[string]bool{}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("%q can't be read: it may have been moved or deleted", filepath.Base(p))
		}
		if !st.IsDir() {
			if !seen[p] {
				seen[p] = true
				out = append(out, TFile{ID: newID()[:12], Name: filepath.Base(p), Path: p, Size: st.Size(), Mime: lan.MimeOf(p)})
			}
			continue
		}
		root := filepath.Dir(p)
		err = filepath.WalkDir(p, func(fp string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() || skipName[strings.ToLower(d.Name())] || seen[fp] {
				return nil // unreadable entries and links are skipped, not fatal
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, fp)
			seen[fp] = true
			out = append(out, TFile{ID: newID()[:12], Name: filepath.ToSlash(rel), Path: fp, Size: info.Size(), Mime: lan.MimeOf(fp)})
			if len(out) > 20000 {
				return errors.New("too many files (over 20,000) — send them as a ZIP or in smaller parts")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, errors.New("there are no files to send in what you chose")
	}
	return out, nil
}

func (a *App) ShowInFolder(transferID string) error {
	t, ok := a.store.find(transferID)
	if !ok {
		return errors.New("that transfer is no longer listed")
	}
	for _, f := range t.Files {
		if f.Path != "" {
			if _, err := os.Stat(f.Path); err == nil {
				return revealInExplorer(f.Path)
			}
		}
	}
	if t.SaveDir != "" {
		return revealInExplorer(t.SaveDir)
	}
	return errors.New("the files are no longer there")
}

func (a *App) OpenFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return errors.New("that file is no longer there")
	}
	return openPath(path)
}

func (a *App) OpenDownloads() error {
	d := a.settings.get().DownloadDir
	os.MkdirAll(d, 0o755)
	return revealInExplorer(d)
}

func (a *App) CopyText(s string) error { return wruntime.ClipboardSetText(a.ctx, s) }

func (a *App) OpenURL(u string) {
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		wruntime.BrowserOpenURL(a.ctx, u)
	}
}

func (a *App) Quit() {
	a.quitting = true
	wruntime.Quit(a.ctx)
}

// ---------- incoming decisions ----------

// RespondIncoming answers a request to receive files. dir overrides the download folder ("" = default).
func (a *App) RespondIncoming(id string, accept, trust bool, dir string) {
	a.log.Info("incoming decision from the window", "accept", accept)
	a.mu.Lock()
	p := a.incoming[id]
	a.mu.Unlock()
	if p != nil {
		select {
		case p.answer <- decision{accept, trust, dir}:
		default:
		}
	}
}

// AnswerPIN gives the PIN a receiver asked for ("" cancels).
func (a *App) AnswerPIN(transferID, pin string) {
	a.mu.Lock()
	ch := a.pinWait[transferID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- pin:
		default:
		}
	}
}

// ---------- transfer controls ----------

func (a *App) Cancel(id string) {
	c := a.store.control(id)
	if c.cancel != nil {
		c.paused = false
		c.cancel()
	}
	if t, ok := a.store.get(id); ok && t.Direction == "received" && t.Method == "direct" && a.recv != nil {
		a.recv.CancelActive(id)
		a.store.finish(id, stCancelled, "Cancelled")
	}
	if t, ok := a.store.get(id); ok && (t.Paused || final(t.Status)) {
		a.store.finish(id, stCancelled, "Cancelled")
	}
}

func (a *App) Pause(id string) {
	c := a.store.control(id)
	if t, ok := a.store.get(id); ok && t.CanPause && c.cancel != nil && !final(t.Status) {
		c.paused = true
		c.cancel()
	}
}

func (a *App) Resume(id string) {
	c := a.store.control(id)
	if t, ok := a.store.get(id); ok && t.Paused && c.restart != nil {
		c.paused = false
		a.store.update(id, func(t *Transfer) { t.Paused, t.Note = false, "" })
		c.restart()
	}
}

func (a *App) Retry(id string) {
	c := a.store.control(id)
	if c.retry != nil {
		retry := c.retry
		a.store.dismiss(id)
		retry()
	}
}

func (a *App) Dismiss(id string) { a.store.dismiss(id) }
func (a *App) ClearHistory()     { a.store.clearHistory() }
