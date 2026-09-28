package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"ferry/internal/db"
)

// Transfer states — identical strings are used by the web app and Android.
const (
	stCreated      = "created"
	stWaiting      = "waiting"
	stNegotiating  = "negotiating"
	stConnecting   = "connecting"
	stTransferring = "transferring"
	stVerifying    = "verifying"
	stCompleted    = "completed"
	stFailed       = "failed"
	stCancelled    = "cancelled"
	stExpired      = "expired"
	stRejected     = "rejected"
	stInterrupted  = "interrupted"
)

var validStates = map[string]bool{stCreated: true, stWaiting: true, stNegotiating: true, stConnecting: true, stTransferring: true,
	stVerifying: true, stCompleted: true, stFailed: true, stCancelled: true, stExpired: true, stRejected: true, stInterrupted: true}

func isFinal(st string) bool {
	return st == stCompleted || st == stFailed || st == stCancelled || st == stExpired || st == stRejected
}

type Transfer struct {
	ID               string  `json:"id"`
	Direction        string  `json:"direction"` // sent | received, relative to the viewer
	Method           string  `json:"method"`    // direct | server | link
	Status           string  `json:"status"`
	Peer             string  `json:"peer"`
	SourceDeviceID   string  `json:"sourceDeviceId"`
	TargetDeviceID   string  `json:"targetDeviceId"`
	SourceDeviceName string  `json:"sourceDeviceName"`
	TargetDeviceName string  `json:"targetDeviceName"`
	ShareID          string  `json:"shareId,omitempty"`
	FileCount        int     `json:"fileCount"`
	TotalBytes       int64   `json:"totalBytes"`
	BytesDone        int64   `json:"bytesDone"`
	Error            string  `json:"error,omitempty"`
	CreatedAt        int64   `json:"createdAt"`
	UpdatedAt        int64   `json:"updatedAt"`
	Files            []*File `json:"files,omitempty"`
}

const transferSelect = `SELECT t.id, t.direction, t.method, t.status, t.peer, t.source_device_id, t.target_device_id,
	COALESCE(sd.name, ''), COALESCE(td.name, ''), t.share_id, t.file_count, t.total_bytes, t.bytes_done, t.error, t.created_at, t.updated_at
	FROM transfers t LEFT JOIN devices sd ON sd.id = t.source_device_id LEFT JOIN devices td ON td.id = t.target_device_id`

func scanTransfer(row interface{ Scan(...any) error }) (*Transfer, error) {
	t := &Transfer{}
	err := row.Scan(&t.ID, &t.Direction, &t.Method, &t.Status, &t.Peer, &t.SourceDeviceID, &t.TargetDeviceID, &t.SourceDeviceName,
		&t.TargetDeviceName, &t.ShareID, &t.FileCount, &t.TotalBytes, &t.BytesDone, &t.Error, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// relativize sets Direction from the viewing device's perspective.
func (t *Transfer) relativize(viewerDevice string) {
	if t.TargetDeviceID == "" {
		return
	}
	if viewerDevice != "" && t.TargetDeviceID == viewerDevice {
		t.Direction = "received"
	} else {
		t.Direction = "sent"
	}
	if t.SourceDeviceName == "" && t.SourceDeviceID == "" {
		t.SourceDeviceName = "Web browser"
	}
}

func (s *Server) getTransfer(ctx context.Context, userID, id string) (*Transfer, error) {
	t, err := scanTransfer(s.db.QueryRow(ctx, transferSelect+` WHERE t.id = ? AND t.user_id = ?`, id, userID))
	if db.IsNoRows(err) {
		return nil, errf(404, "transfer_not_found", "That transfer no longer exists.")
	}
	return t, err
}

func (s *Server) transferFiles(ctx context.Context, userID, id string) ([]*File, error) {
	return scanFiles(s.db.Query(ctx, `SELECT `+fileCols+` FROM files WHERE user_id = ? AND transfer_id = ? ORDER BY LOWER(name)`, userID, id))
}

func (s *Server) handleListTransfers(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	sqlq := transferSelect + ` WHERE t.user_id = ? AND t.hidden = 0`
	args := []any{a.user.ID}
	if b, err := strconv.ParseInt(q.Get("before"), 10, 64); err == nil && b > 0 {
		sqlq += ` AND t.created_at < ?`
		args = append(args, b)
	}
	// Direction is relative to the viewing device (see relativize), so it is expressed the same way in SQL.
	switch q.Get("direction") {
	case "received":
		sqlq += ` AND ((t.target_device_id <> '' AND t.target_device_id = ?) OR (t.target_device_id = '' AND t.direction = 'received'))`
		args = append(args, a.deviceID)
	case "sent":
		sqlq += ` AND ((t.target_device_id <> '' AND t.target_device_id <> ?) OR (t.target_device_id = '' AND t.direction = 'sent'))`
		args = append(args, a.deviceID)
	}
	var statuses []string
	for _, st := range splitIDs(q.Get("status")) {
		if validStates[st] {
			statuses = append(statuses, st)
		}
	}
	if len(statuses) > 0 {
		sqlq += ` AND t.status IN (?` + strings.Repeat(",?", len(statuses)-1) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	rows, err := s.db.Query(r.Context(), sqlq+` ORDER BY t.created_at DESC, t.id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	out := []*Transfer{}
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		t.relativize(a.deviceID)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"transfers": out})
}

type transferReq struct {
	Direction      *string `json:"direction"`
	Method         *string `json:"method"`
	Status         *string `json:"status"`
	Peer           *string `json:"peer"`
	TargetDeviceID *string `json:"targetDeviceId"`
	ShareID        *string `json:"shareId"`
	FileCount      *int    `json:"fileCount"`
	TotalBytes     *int64  `json:"totalBytes"`
	BytesDone      *int64  `json:"bytesDone"`
	Error          *string `json:"error"`
}

func str(p *string, def string) string {
	if p == nil {
		return def
	}
	return strings.TrimSpace(*p)
}

func (s *Server) handleCreateTransfer(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	var req transferReq
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := nowMs()
	t := &Transfer{ID: newID(), Direction: str(req.Direction, "sent"), Method: str(req.Method, "server"), Status: str(req.Status, stCreated),
		Peer: str(req.Peer, ""), SourceDeviceID: a.deviceID, TargetDeviceID: str(req.TargetDeviceID, ""), ShareID: str(req.ShareID, ""),
		CreatedAt: now, UpdatedAt: now, Error: str(req.Error, "")}
	if req.FileCount != nil {
		t.FileCount = *req.FileCount
	}
	if req.TotalBytes != nil {
		t.TotalBytes = *req.TotalBytes
	}
	if req.BytesDone != nil {
		t.BytesDone = *req.BytesDone
	}
	if t.Direction != "sent" && t.Direction != "received" || (t.Method != "direct" && t.Method != "server" && t.Method != "link") || !validStates[t.Status] {
		s.writeErr(w, r, errf(400, "bad_transfer", "Invalid transfer details."))
		return
	}
	if len(t.Peer) > 200 {
		t.Peer = clip(t.Peer, 200)
	}
	if len(t.Error) > 500 {
		t.Error = t.Error[:500]
	}
	if t.TargetDeviceID != "" {
		var owner, name string
		if err := s.db.QueryRow(r.Context(), `SELECT user_id, name FROM devices WHERE id = ?`, t.TargetDeviceID).Scan(&owner, &name); err != nil || owner != a.user.ID {
			s.writeErr(w, r, errf(404, "device_not_found", "That device is no longer linked to your account."))
			return
		}
		if t.TargetDeviceID == a.deviceID {
			s.writeErr(w, r, errf(400, "same_device", "You can't send files to the same device."))
			return
		}
		t.Method, t.Direction, t.Peer = "server", "sent", name
	}
	_, err := s.db.Exec(r.Context(), `INSERT INTO transfers (id, user_id, direction, method, status, peer, source_device_id, target_device_id, share_id,
		file_count, total_bytes, bytes_done, error, hidden, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		t.ID, a.user.ID, t.Direction, t.Method, t.Status, t.Peer, t.SourceDeviceID, t.TargetDeviceID, t.ShareID, t.FileCount, t.TotalBytes,
		t.BytesDone, t.Error, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	t, err = s.getTransfer(r.Context(), a.user.ID, t.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	t.relativize(a.deviceID)
	writeJSON(w, 201, t)
}

func (s *Server) handleGetTransfer(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	t, err := s.getTransfer(r.Context(), a.user.ID, r.PathValue("id"))
	if err == nil {
		t.Files, err = s.transferFiles(r.Context(), a.user.ID, t.ID)
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	t.relativize(a.deviceID)
	writeJSON(w, 200, t)
}

func (s *Server) handleUpdateTransfer(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	ctx := r.Context()
	t, err := s.getTransfer(ctx, a.user.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var req transferReq
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.Status != nil && *req.Status != t.Status {
		next := *req.Status
		if !validStates[next] {
			s.writeErr(w, r, errf(400, "bad_status", "Unknown transfer status."))
			return
		}
		if isFinal(t.Status) {
			s.writeErr(w, r, errf(409, "transfer_finished", "This transfer has already finished ("+t.Status+")."))
			return
		}
		t.Status = next
	}
	if req.BytesDone != nil {
		t.BytesDone = *req.BytesDone
	}
	if req.FileCount != nil {
		t.FileCount = *req.FileCount
	}
	if req.TotalBytes != nil {
		t.TotalBytes = *req.TotalBytes
	}
	if req.Error != nil {
		t.Error = str(req.Error, "")
		if len(t.Error) > 500 {
			t.Error = t.Error[:500]
		}
	}
	t.UpdatedAt = nowMs()
	if _, err := s.db.Exec(ctx, `UPDATE transfers SET status = ?, bytes_done = ?, file_count = ?, total_bytes = ?, error = ?, updated_at = ? WHERE id = ?`,
		t.Status, t.BytesDone, t.FileCount, t.TotalBytes, t.Error, t.UpdatedAt, t.ID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	// Rejected/cancelled inbox transfers free their temporary files immediately.
	if t.Status == stRejected || t.Status == stCancelled {
		s.deleteTransferFiles(ctx, a.user.ID, t.ID)
	}
	t.relativize(a.deviceID)
	writeJSON(w, 200, t)
}

func (s *Server) deleteTransferFiles(ctx context.Context, userID, transferID string) {
	files, err := s.transferFiles(ctx, userID, transferID)
	if err != nil {
		return
	}
	var ids []string
	for _, f := range files {
		ids = append(ids, f.ID)
	}
	if len(ids) > 0 {
		s.deleteItems(ctx, userID, ids, nil)
	}
	rows, err := s.db.Query(ctx, `SELECT id FROM uploads WHERE transfer_id = ? AND file_id = ''`, transferID)
	if err == nil {
		var up []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			up = append(up, id)
		}
		rows.Close()
		for _, id := range up {
			s.store.Remove("uploads/" + id)
			s.db.Exec(ctx, `DELETE FROM uploads WHERE id = ?`, id)
		}
	}
}

func (s *Server) handleHideTransfer(w http.ResponseWriter, r *http.Request) {
	res, err := s.db.Exec(r.Context(), `UPDATE transfers SET hidden = 1 WHERE id = ? AND user_id = ?`, r.PathValue("id"), userOf(r).ID)
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

// handleClearTransfers hides finished history entries; active transfers and files are untouched.
func (s *Server) handleClearTransfers(w http.ResponseWriter, r *http.Request) {
	_, err := s.db.Exec(r.Context(), `UPDATE transfers SET hidden = 1 WHERE user_id = ? AND status IN ('completed','failed','cancelled','expired','rejected')`, userOf(r).ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleTransferFiles(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if _, err := s.getTransfer(r.Context(), u.ID, r.PathValue("id")); err != nil {
		s.writeErr(w, r, err)
		return
	}
	files, err := s.transferFiles(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"files": files})
}

// handleInbox lists server-relayed transfers addressed to the calling device.
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	if a.deviceID == "" {
		writeJSON(w, 200, map[string]any{"transfers": []any{}})
		return
	}
	rows, err := s.db.Query(r.Context(), transferSelect+` WHERE t.user_id = ? AND t.target_device_id = ? AND t.status IN ('waiting','transferring','verifying','interrupted') ORDER BY t.created_at`, a.user.ID, a.deviceID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var list []*Transfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			rows.Close()
			s.writeErr(w, r, err)
			return
		}
		t.relativize(a.deviceID)
		list = append(list, t)
	}
	rows.Close()
	for _, t := range list {
		t.Files, _ = s.transferFiles(r.Context(), a.user.ID, t.ID)
	}
	if list == nil {
		list = []*Transfer{}
	}
	writeJSON(w, 200, map[string]any{"transfers": list})
}
