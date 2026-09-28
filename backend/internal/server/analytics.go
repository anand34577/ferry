package server

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- link analytics ----------

// linkEvent records one public access to a share (view, download, preview, upload, password_failed).
func (s *Server) linkEvent(ctx context.Context, r *http.Request, sh *Share, kind, detail string) {
	var ip, ua, ref string
	if r != nil {
		ip, ua, ref = s.clientIP(r), r.UserAgent(), r.Referer()
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO share_events (id, share_id, at, kind, ip, user_agent, referrer, detail) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), sh.ID, nowMs(), kind, ip, clip(ua, 250), clip(ref, 250), clip(detail, 250)); err != nil {
		s.log.Warn("link event write failed", "err", err)
	}
}

// clip shortens s to at most n bytes without splitting a UTF-8 character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// firstRange is true for a request that starts reading a file; players and download managers send
// many follow-up Range requests that must not count as separate downloads.
func firstRange(r *http.Request) bool {
	h := r.Header.Get("Range")
	return h == "" || strings.HasPrefix(h, "bytes=0-")
}

type linkEventRow struct {
	At        int64  `json:"at"`
	Kind      string `json:"kind"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
	Referrer  string `json:"referrer"`
	Detail    string `json:"detail"`
}

// handleShareAnalytics returns all-time totals plus the raw events of the last `days` days
// (capped); the client derives the daily chart and browser breakdown from them.
func (s *Server) handleShareAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	q, args := `SELECT `+shareCols+` FROM shares s WHERE s.id = ? AND s.user_id = ?`, []any{r.PathValue("id"), u.ID}
	if u.Role == "admin" {
		q, args = `SELECT `+shareCols+` FROM shares s WHERE s.id = ?`, args[:1]
	}
	sh, err := scanShare(s.db.QueryRow(ctx, q, args...))
	if err != nil {
		s.writeErr(w, r, errf(404, "share_not_found", "That link no longer exists."))
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	totals := map[string]int{"view": 0, "download": 0, "preview": 0, "upload": 0, "password_failed": 0}
	if rows, err := s.db.Query(ctx, `SELECT kind, COUNT(*) FROM share_events WHERE share_id = ? GROUP BY kind`, sh.ID); err == nil {
		for rows.Next() {
			var k string
			var n int
			rows.Scan(&k, &n)
			totals[k] = n
		}
		rows.Close()
	}
	var visitors int
	s.db.QueryRow(ctx, `SELECT COUNT(DISTINCT ip) FROM share_events WHERE share_id = ?`, sh.ID).Scan(&visitors)
	since := nowMs() - int64(days)*24*3600*1000
	// At most 5000 raw events per window. If links ever get that busy, aggregate per day in SQL instead.
	rows, err := s.db.Query(ctx, `SELECT at, kind, ip, user_agent, referrer, detail FROM share_events WHERE share_id = ? AND at >= ? ORDER BY at DESC LIMIT 5000`, sh.ID, since)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	events := []linkEventRow{}
	for rows.Next() {
		var e linkEventRow
		if err := rows.Scan(&e.At, &e.Kind, &e.IP, &e.UserAgent, &e.Referrer, &e.Detail); err != nil {
			s.writeErr(w, r, err)
			return
		}
		events = append(events, e)
	}
	writeJSON(w, 200, map[string]any{"share": map[string]any{"id": sh.ID, "name": sh.Name, "kind": sh.Kind, "createdAt": sh.CreatedAt},
		"totals": totals, "visitors": visitors, "days": days, "events": events})
}

// ---------- audit of every API change ----------

type auditFlag struct{ done bool }

const auditKey ctxKey = 2

// Routes that change nothing worth recording, or would flood the log (tus chunks, heartbeats, progress).
var auditSkip = map[string]bool{
	"POST /api/v1/files/check":                true,
	"POST /api/v1/uploads":                    true, // the finished upload is audited as file_uploaded
	"PATCH /api/v1/uploads/{id}":              true,
	"PUT /api/v1/devices/current/presence":    true,
	"DELETE /api/v1/devices/current/presence": true,
	"PATCH /api/v1/transfers/{id}":            true, // progress updates
	"POST /api/v1/me/totp/setup":              true,
}

var auditNames = map[string]string{
	"POST /api/v1/folders":             "folder_created",
	"PATCH /api/v1/folders/{id}":       "folder_updated",
	"DELETE /api/v1/folders/{id}":      "folder_deleted",
	"PATCH /api/v1/files/{id}":         "file_updated",
	"DELETE /api/v1/files/{id}":        "file_deleted",
	"POST /api/v1/files/zip":           "zip_downloaded",
	"PATCH /api/v1/shares/{id}":        "share_updated",
	"PATCH /api/v1/me":                 "profile_updated",
	"POST /api/v1/transfers":           "transfer_created",
	"DELETE /api/v1/transfers/{id}":    "transfer_hidden",
	"POST /api/v1/transfers/clear":     "transfers_cleared",
	"PATCH /api/v1/devices/{id}":       "device_updated",
	"DELETE /api/v1/uploads/{id}":      "upload_cancelled",
	"POST /api/v1/shares/{id}/shorten": "share_shortened",
	"POST /api/v1/me/gotify/test":      "gotify_test",
}

// auditAPI records every successful mutating API call that its handler did not already audit
// with a more specific action.
func (s *Server) auditAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || auditSkip[r.Pattern] {
			next(w, r)
			return
		}
		flag := &auditFlag{}
		sw := &statusWriter{ResponseWriter: w}
		r = r.WithContext(context.WithValue(r.Context(), auditKey, flag))
		next(sw, r)
		if flag.done || sw.status >= 400 || sw.status == 0 {
			return
		}
		action := auditNames[r.Pattern]
		if action == "" {
			action = strings.Replace(r.Pattern, "/api/v1", "", 1)
		}
		s.audit(r.Context(), r, userOf(r).ID, action, r.PathValue("id"), "")
	}
}

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	limit, _ := strconv.Atoi(qv.Get("limit"))
	csvOut := qv.Get("format") == "csv"
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if csvOut {
		limit = 100000
	}
	where, args := []string{"1 = 1"}, []any{}
	if v := strings.TrimSpace(qv.Get("q")); v != "" {
		where = append(where, `(LOWER(a.action) LIKE ? ESCAPE '\' OR LOWER(a.detail) LIKE ? ESCAPE '\' OR LOWER(a.target) LIKE ? ESCAPE '\' OR LOWER(COALESCE(u.email, '')) LIKE ? ESCAPE '\' OR a.ip LIKE ? ESCAPE '\')`)
		p := likeEscape(v)
		args = append(args, p, p, p, p, p)
	}
	if v := qv.Get("user"); v != "" {
		where = append(where, "a.user_id = ?")
		args = append(args, v)
	}
	if v := qv.Get("action"); v != "" {
		where = append(where, "a.action LIKE ? ESCAPE '\\'")
		args = append(args, likeEscape(v))
	}
	if v, _ := strconv.ParseInt(qv.Get("before"), 10, 64); v > 0 {
		where = append(where, "a.at < ?")
		args = append(args, v)
	}
	if v, _ := strconv.ParseInt(qv.Get("since"), 10, 64); v > 0 {
		where = append(where, "a.at >= ?")
		args = append(args, v)
	}
	rows, err := s.db.Query(r.Context(), `SELECT a.id, a.at, a.user_id, COALESCE(u.email, ''), a.action, a.target, a.ip, a.detail
		FROM audit_log a LEFT JOIN users u ON u.id = a.user_id WHERE `+strings.Join(where, " AND ")+` ORDER BY a.at DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	type ev struct {
		ID     string `json:"id"`
		At     int64  `json:"at"`
		UserID string `json:"userId"`
		Email  string `json:"email"`
		Action string `json:"action"`
		Target string `json:"target"`
		IP     string `json:"ip"`
		Detail string `json:"detail"`
	}
	out := []ev{}
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.ID, &e.At, &e.UserID, &e.Email, &e.Action, &e.Target, &e.IP, &e.Detail); err != nil {
			s.writeErr(w, r, err)
			return
		}
		out = append(out, e)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	if csvOut {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="ferry-audit-`+time.Now().UTC().Format("20060102")+`.csv"`)
		cw := csv.NewWriter(w)
		cw.Write([]string{"time_utc", "user", "action", "target", "detail", "ip"})
		for _, e := range out {
			cw.Write([]string{time.UnixMilli(e.At).UTC().Format(time.RFC3339), e.Email, e.Action, e.Target, csvSafe(e.Detail), e.IP})
		}
		cw.Flush()
		s.audit(r.Context(), r, userOf(r).ID, "admin_audit_exported", "", strconv.Itoa(len(out))+" events")
		return
	}
	writeJSON(w, 200, map[string]any{"events": out, "more": more})
}

// csvSafe defuses spreadsheet formula injection in user-controlled cells.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
