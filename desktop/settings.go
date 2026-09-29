package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Account is a signed-in Ferry server. The session token is stored encrypted for this Windows user.
type Account struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	SiteName string `json:"siteName"`
	Email    string `json:"email"`
	UserName string `json:"userName"`
	IsAdmin  bool   `json:"isAdmin"`
	DeviceID string `json:"deviceId"`
	TokenEnc string `json:"tokenEnc,omitempty"`
}

type TrustedDevice struct {
	Fingerprint string `json:"fingerprint"`
	Alias       string `json:"alias"`
	AutoAccept  bool   `json:"autoAccept"`
}

// Settings are saved as JSON in %APPDATA%\Ferry\settings.json.
type Settings struct {
	Alias            string            `json:"alias"`
	DownloadDir      string            `json:"downloadDir"`
	Receiving        bool              `json:"receiving"`
	RequirePIN       bool              `json:"requirePin"`
	PIN              string            `json:"pin"`
	AskWhereToSave   bool              `json:"askWhereToSave"`
	OpenWhenDone     bool              `json:"openWhenDone"`
	AutoAcceptOwn    bool              `json:"autoAcceptOwn"` // from my own devices through my server
	Trusted          []TrustedDevice   `json:"trusted"`
	Theme            string            `json:"theme"` // system | light | dark
	Notifications    bool              `json:"notifications"`
	CloseToTray      bool              `json:"closeToTray"`
	StartWithWindows bool              `json:"startWithWindows"`
	LinkExpiry       int64             `json:"linkExpiry"` // seconds; default for new links
	Accounts         []Account         `json:"accounts"`
	ActiveAccount    string            `json:"activeAccount"`
	Resume           map[string]string `json:"resume,omitempty"` // tus upload URLs by key
	Onboarded        bool              `json:"onboarded"`
}

type settingsStore struct {
	mu   sync.Mutex
	path string
	s    Settings
}

func defaultSettings() Settings {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	return Settings{Alias: firstNonEmpty(host, "Windows PC"), DownloadDir: filepath.Join(home, "Downloads", "Ferry"), Receiving: true,
		Theme: "system", Notifications: true, CloseToTray: true, LinkExpiry: 7 * 86400, AutoAcceptOwn: false, Resume: map[string]string{}}
}

func loadSettings(dir string) *settingsStore {
	st := &settingsStore{path: filepath.Join(dir, "settings.json"), s: defaultSettings()}
	if b, err := os.ReadFile(st.path); err == nil {
		if json.Unmarshal(b, &st.s) != nil {
			os.Rename(st.path, st.path+".broken") // keep it for support; start fresh rather than refuse to open
			st.s = defaultSettings()
		}
	}
	if st.s.Resume == nil {
		st.s.Resume = map[string]string{}
	}
	if strings.TrimSpace(st.s.Alias) == "" {
		st.s.Alias = defaultSettings().Alias
	}
	if st.s.DownloadDir == "" {
		st.s.DownloadDir = defaultSettings().DownloadDir
	}
	return st
}

func (st *settingsStore) get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.s
	s.Trusted = append([]TrustedDevice(nil), st.s.Trusted...)
	s.Accounts = append([]Account(nil), st.s.Accounts...)
	return s
}

// update changes the settings and saves them atomically (write + rename).
func (st *settingsStore) update(fn func(s *Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	b, _ := json.MarshalIndent(st.s, "", "  ")
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// resumeStore adapts the settings to ferry.ResumeStore.
type resumeStore struct{ st *settingsStore }

func (r resumeStore) Get(k string) string {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	return r.st.s.Resume[k]
}

func (r resumeStore) Set(k, v string) {
	r.st.update(func(s *Settings) {
		if v == "" {
			delete(s.Resume, k)
		} else {
			s.Resume[k] = v
		}
	})
}

func (st *settingsStore) account(id string) (Account, error) {
	for _, a := range st.get().Accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return Account{}, errors.New("that server is no longer in your list")
}

func (st *settingsStore) trusted(fp string) (TrustedDevice, bool) {
	for _, t := range st.get().Trusted {
		if strings.EqualFold(t.Fingerprint, fp) {
			return t, true
		}
	}
	return TrustedDevice{}, false
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
