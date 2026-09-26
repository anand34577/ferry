package server

import (
	"net"
	"net/http"
	"strings"
)

type Device struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Platform    string   `json:"platform"`
	AppVersion  string   `json:"appVersion"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	LastSeen    int64    `json:"lastSeen"`
	CreatedAt   int64    `json:"createdAt"`
	Current     bool     `json:"current"`
	Online      bool     `json:"online"`
	LanAddrs    []string `json:"lanAddrs,omitempty"`
	LanPort     int      `json:"lanPort,omitempty"`
	LanProtocol string   `json:"lanProtocol,omitempty"`
	PresenceAt  int64    `json:"presenceAt,omitempty"`
	UserEmail   string   `json:"userEmail,omitempty"`
}

// presenceTTL: devices re-announce every 60 s while receiving; older presence is ignored.
const presenceTTL = 3 * 60 * 1000

func (s *Server) listDevices(r *http.Request, userID string) ([]*Device, error) {
	q := `SELECT d.id, d.name, d.platform, d.app_version, d.fingerprint, d.lan_addrs, d.lan_port, d.lan_protocol, d.presence_at, d.last_seen, d.created_at, u.email
		FROM devices d JOIN users u ON u.id = d.user_id`
	args := []any{}
	if userID != "" {
		q += ` WHERE d.user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.Query(r.Context(), q+` ORDER BY d.last_seen DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := nowMs()
	out := []*Device{}
	for rows.Next() {
		d := &Device{}
		var addrs string
		if err := rows.Scan(&d.ID, &d.Name, &d.Platform, &d.AppVersion, &d.Fingerprint, &addrs, &d.LanPort, &d.LanProtocol, &d.PresenceAt, &d.LastSeen, &d.CreatedAt, &d.UserEmail); err != nil {
			return nil, err
		}
		if now-d.PresenceAt < presenceTTL && addrs != "" {
			d.Online = true
			d.LanAddrs = strings.Split(addrs, ",")
		} else {
			d.LanPort, d.LanProtocol, d.PresenceAt = 0, "", 0
		}
		if userID != "" {
			d.UserEmail = ""
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	devs, err := s.listDevices(r, a.user.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	for _, d := range devs {
		d.Current = d.ID == a.deviceID
	}
	writeJSON(w, 200, map[string]any{"devices": devs})
}

func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 80 {
		s.writeErr(w, r, errf(400, "invalid_name", "Device name must be 1–80 characters."))
		return
	}
	res, err := s.db.Exec(r.Context(), `UPDATE devices SET name = ? WHERE id = ? AND user_id = ?`, name, r.PathValue("id"), userOf(r).ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeErr(w, r, errNotFound)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// revokeDevice deletes the device and signs it out everywhere.
func (s *Server) revokeDevice(r *http.Request, id, userID string) error {
	q := `DELETE FROM devices WHERE id = ?`
	args := []any{id}
	if userID != "" {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	res, err := s.db.Exec(r.Context(), q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	_, err = s.db.Exec(r.Context(), `DELETE FROM sessions WHERE device_id = ?`, id)
	return err
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if err := s.revokeDevice(r, r.PathValue("id"), u.ID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "device_revoked", r.PathValue("id"), "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handlePresence records where a device can be reached on its LAN (server-assisted discovery).
// Only other devices of the same account can read it.
func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	if a.deviceID == "" {
		s.writeErr(w, r, errf(400, "not_a_device", "Only app devices can publish presence."))
		return
	}
	var req struct {
		Addrs       []string `json:"addrs"`
		Port        int      `json:"port"`
		Protocol    string   `json:"protocol"`
		Fingerprint string   `json:"fingerprint"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var addrs []string
	for _, a := range req.Addrs {
		if ip := net.ParseIP(strings.TrimSpace(a)); ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast()) && len(addrs) < 8 {
			addrs = append(addrs, ip.String())
		}
	}
	if req.Port <= 0 || req.Port > 65535 || (req.Protocol != "http" && req.Protocol != "https") || len(req.Fingerprint) > 128 {
		s.writeErr(w, r, errf(400, "bad_presence", "Invalid presence data."))
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE devices SET lan_addrs = ?, lan_port = ?, lan_protocol = ?, fingerprint = ?, presence_at = ?, last_seen = ? WHERE id = ?`,
		strings.Join(addrs, ","), req.Port, req.Protocol, req.Fingerprint, nowMs(), nowMs(), a.deviceID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "serverTime": nowMs(), "yourPublicIp": s.clientIP(r)})
}

func (s *Server) handleClearPresence(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	if a.deviceID != "" {
		s.db.Exec(r.Context(), `UPDATE devices SET lan_addrs = '', presence_at = 0 WHERE id = ?`, a.deviceID)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
