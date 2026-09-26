package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"ferry/internal/db"
)

// RunCleanup runs Cleanup every interval until ctx ends. Every step is idempotent.
func (s *Server) RunCleanup(ctx context.Context) {
	s.Cleanup(ctx)
	t := time.NewTicker(s.cfg.CleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Cleanup(ctx)
		}
	}
}

// Cleanup removes expired sessions, stale uploads, expired transfers and orphaned blobs.
func (s *Server) Cleanup(ctx context.Context) map[string]int64 {
	now := nowMs()
	res := map[string]int64{}
	exec := func(name, q string, args ...any) {
		r, err := s.db.Exec(ctx, q, args...)
		if err != nil {
			s.log.Error("cleanup step failed", "step", name, "err", err)
			return
		}
		n, _ := r.RowsAffected()
		res[name] += n
	}
	s.limiter.sweep()
	s.sweepZipTickets()
	exec("sessions", `DELETE FROM sessions WHERE expires_at < ?`, now)
	exec("downloadSessions", `DELETE FROM download_sessions WHERE expires_at < ?`, now)
	exec("passwordResets", `DELETE FROM password_resets WHERE expires_at < ?`, now)

	// Stale in-progress uploads.
	stale := now - s.cfg.UploadExpiry.Milliseconds()
	if rows, err := s.db.Query(ctx, `SELECT id FROM uploads WHERE file_id = '' AND updated_at < ?`, stale); err == nil {
		var ids []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			if _, busyNow := busy.Load(id); busyNow {
				continue
			}
			s.store.Remove("uploads/" + id)
			exec("staleUploads", `DELETE FROM uploads WHERE id = ?`, id)
		}
	}
	// Completion tombstones of normal uploads are only needed briefly (lost final response).
	exec("uploadTombstones", `DELETE FROM uploads WHERE file_id <> '' AND share_id = '' AND updated_at < ?`, now-3600*1000)

	// Device inbox: waiting too long → expired; finished transfers' temporary files are removed.
	exec("expiredTransfers", `UPDATE transfers SET status = 'expired', updated_at = ? WHERE status IN ('created','waiting','interrupted') AND updated_at < ?`, now, now-7*24*3600*1000)
	exec("staleActiveTransfers", `UPDATE transfers SET status = 'interrupted', updated_at = ? WHERE status IN ('negotiating','connecting','transferring','verifying') AND updated_at < ?`, now, now-24*3600*1000)
	if rows, err := s.db.Query(ctx, `SELECT DISTINCT f.user_id, f.transfer_id FROM files f JOIN transfers t ON t.id = f.transfer_id
		WHERE f.transfer_id <> '' AND ((t.status = 'completed' AND t.updated_at < ?) OR t.status IN ('expired','rejected','cancelled','failed'))`, now-24*3600*1000); err == nil {
		var pairs [][2]string
		for rows.Next() {
			var u, t string
			rows.Scan(&u, &t)
			pairs = append(pairs, [2]string{u, t})
		}
		rows.Close()
		for _, p := range pairs {
			s.deleteTransferFiles(ctx, p[0], p[1])
			res["transferFiles"]++
		}
	}
	// Files whose transfer row vanished.
	if rows, err := s.db.Query(ctx, `SELECT f.user_id, f.id FROM files f LEFT JOIN transfers t ON t.id = f.transfer_id WHERE f.transfer_id <> '' AND t.id IS NULL`); err == nil {
		var pairs [][2]string
		for rows.Next() {
			var u, id string
			rows.Scan(&u, &id)
			pairs = append(pairs, [2]string{u, id})
		}
		rows.Close()
		for _, p := range pairs {
			s.deleteItems(ctx, p[0], []string{p[1]}, nil)
		}
	}

	// Expired/revoked shares are kept visible for a while, then removed.
	keep := now - s.cfg.ShareRetention.Milliseconds()
	if rows, err := s.db.Query(ctx, `SELECT `+shareCols+` FROM shares s WHERE (s.expires_at > 0 AND s.expires_at < ?) OR (s.revoked = 1 AND s.updated_at < ?)`, keep, keep); err == nil {
		var list []*Share
		for rows.Next() {
			if sh, err := scanShare(rows); err == nil {
				list = append(list, sh)
			}
		}
		rows.Close()
		for _, sh := range list {
			s.deleteShare(ctx, sh)
			res["oldShares"]++
		}
	}
	exec("audit", `DELETE FROM audit_log WHERE at < ?`, now-s.cfg.AuditRetention.Milliseconds())
	res["orphanBlobs"] = s.sweepOrphans(ctx)
	if total := sum(res); total > 0 {
		s.log.Info("cleanup finished", "result", res)
	}
	return res
}

func sum(m map[string]int64) (t int64) {
	for _, v := range m {
		t += v
	}
	return
}

// sweepOrphans removes blobs/upload files with no DB row (crash leftovers, deferred deletes).
// Only files older than an hour are touched, so in-flight commits are never raced.
func (s *Server) sweepOrphans(ctx context.Context) int64 {
	cutoff := time.Now().Add(-time.Hour)
	var n int64
	check := func(prefix, q string) {
		var keys []string
		s.store.Walk(prefix, func(key string, mod time.Time) {
			if mod.Before(cutoff) {
				keys = append(keys, key)
			}
		})
		for _, key := range keys {
			id := key[strings.LastIndex(key, "/")+1:]
			var x string
			if err := s.db.QueryRow(ctx, q, id).Scan(&x); db.IsNoRows(err) {
				if prefix == "blobs" {
					s.store.Remove("blobs/" + id)
				} else {
					s.store.Remove(key)
				}
				n++
			}
		}
	}
	check("blobs", `SELECT id FROM files WHERE blob = ?`)
	check("uploads", `SELECT id FROM uploads WHERE id = ?`)
	return n
}

// ---------- email ----------

func (s *Server) sendMail(to, subject, body string) error {
	c := s.cfg
	if c.SMTPHost == "" {
		return fmt.Errorf("smtp not configured")
	}
	from := c.SMTPFrom
	if from == "" {
		from = c.SMTPUser
	}
	// Names and subjects are user-controlled: fold all whitespace (incl. CR/LF) and RFC 2047-encode them.
	subject = mime.QEncoding.Encode("utf-8", strings.Join(strings.Fields(subject), " "))
	sender := (&mail.Address{Name: strings.Join(strings.Fields(c.SiteName), " "), Address: from}).String()
	msg := "From: " + sender + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\n\r\n" + body
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	var auth smtp.Auth
	if c.SMTPUser != "" {
		auth = smtp.PlainAuth("", c.SMTPUser, c.SMTPPass, c.SMTPHost)
	}
	// Explicit dial + deadline: smtp.SendMail has no timeout and would hang forever on a stuck server.
	d := &net.Dialer{Timeout: 30 * time.Second}
	var conn net.Conn
	var err error
	if c.SMTPPort == 465 {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: c.SMTPHost})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(2 * time.Minute))
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if ok, _ := cl.Extension("STARTTLS"); ok && c.SMTPPort != 465 {
		if err := cl.StartTLS(&tls.Config{ServerName: c.SMTPHost}); err != nil {
			return err
		}
	}
	if auth != nil {
		if err := cl.Auth(auth); err != nil {
			return err
		}
	}
	if err := cl.Mail(from); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	wc, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write([]byte(msg)); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func (s *Server) notifyUpload(owner *User, sh *Share, f *File, uploader string) {
	if s.cfg.SMTPHost == "" {
		return
	}
	// Coalesce bursts: at most one notification per link per 10 minutes.
	if !s.limiter.allow("notify:"+sh.ID, 1, 10*time.Minute) {
		return
	}
	who := "Someone"
	if uploader != "" {
		who = uploader
	}
	body := who + " uploaded \"" + f.Name + "\" (" + humanSize(f.Size) + ") to your upload link \"" + sh.Name + "\" on " + s.cfg.SiteName + ".\n"
	if s.cfg.PublicURL != "" {
		body += "\nOpen your files: " + s.cfg.PublicURL + "/files\n"
	}
	if err := s.mail(owner.Email, "New files received: "+sh.Name, body); err != nil {
		s.log.Warn("upload notification failed", "err", err)
	}
}
