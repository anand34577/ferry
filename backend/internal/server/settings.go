package server

// Settings administrators change in the web UI (Admin → Settings). They are stored in the settings
// table as "cfg.<key>" and applied immediately, without a restart. Environment variables still win:
// a setting given as FERRY_* is shown locked.

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"ferry/internal/config"
	"ferry/internal/db"
)

const settingPrefix = "cfg."

func (s *Server) savedSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT key, value FROM settings WHERE key LIKE 'cfg.%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(k, settingPrefix)] = v
	}
	return out, rows.Err()
}

// buildConfig applies saved values on top of the environment and validates the result.
func (s *Server) buildConfig(saved map[string]string) (*config.Config, error) {
	c := *s.base
	for k, v := range saved {
		f, ok := config.FieldByKey(k)
		if !ok || s.base.FromEnv[f.Env] {
			continue
		}
		if err := f.Set(&c, v); err != nil {
			return nil, err
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Server) loadSettings(ctx context.Context) error {
	saved, err := s.savedSettings(ctx)
	if err != nil {
		return err
	}
	c, err := s.buildConfig(saved)
	if err != nil {
		return err
	}
	s.cfgp.Store(c)
	return nil
}

type settingView struct {
	config.Field
	Value      string `json:"value"`
	HasValue   bool   `json:"hasValue"`
	Locked     bool   `json:"locked"`     // set in the environment
	Overridden bool   `json:"overridden"` // saved in the UI (can be reset to the default)
}

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	saved, err := s.savedSettings(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	c := s.conf()
	out := make([]settingView, 0, len(config.Fields))
	for _, f := range config.Fields {
		v := f.Get(c)
		sv := settingView{Field: f, Value: v, HasValue: v != "", Locked: s.base.FromEnv[f.Env]}
		_, sv.Overridden = saved[f.Key]
		if f.Kind == "secret" {
			sv.Value = "" // never sent back to the browser
		}
		out = append(out, sv)
	}
	writeJSON(w, 200, map[string]any{"settings": out, "oidcCallbackUrl": s.baseURL(r) + "/api/v1/auth/oidc/callback"})
}

// handleAdminSaveSettings takes {"key": "value"}; null resets a setting to its default.
func (s *Server) handleAdminSaveSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]*string
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	ctx := r.Context()
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	saved, err := s.savedSettings(ctx)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var changed []string
	for k, v := range req {
		f, ok := config.FieldByKey(k)
		if !ok {
			s.writeErr(w, r, errf(400, "unknown_setting", "Unknown setting "+k+"."))
			return
		}
		if s.base.FromEnv[f.Env] {
			s.writeErr(w, r, errf(400, "setting_locked", f.Label+" is set by "+f.Env+" in the server environment. Remove it there to manage it here."))
			return
		}
		if v == nil {
			delete(saved, k)
		} else {
			tmp := *s.base
			if err := f.Set(&tmp, *v); err != nil { // canonical form, e.g. "10 gb" → "10GB"
				s.writeErr(w, r, errf(400, "invalid_setting", err.Error()+"."))
				return
			}
			saved[k] = f.Get(&tmp)
		}
		changed = append(changed, k)
	}
	c, err := s.buildConfig(saved)
	if err != nil {
		s.writeErr(w, r, errf(400, "invalid_setting", "Not saved: "+err.Error()+"."))
		return
	}
	err = s.db.InTx(ctx, func(tx *db.Tx) error {
		for _, k := range changed {
			if v, ok := saved[k]; ok {
				_, err = tx.Exec(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, settingPrefix+k, v)
			} else {
				_, err = tx.Exec(ctx, `DELETE FROM settings WHERE key = ?`, settingPrefix+k)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.cfgp.Store(c)
	for _, k := range changed {
		if strings.HasPrefix(k, "oidc_") {
			s.oidc.reset()
			break
		}
	}
	sort.Strings(changed)
	s.audit(ctx, r, userOf(r).ID, "admin_settings_changed", "", strings.Join(changed, ", "))
	s.handleAdminSettings(w, r)
}
