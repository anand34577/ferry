package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"ferry/internal/db"
)

// RunCleanup runs Cleanup every interval until ctx ends. Every step is idempotent.
func (s *Server) RunCleanup(ctx context.Context) {
	s.Cleanup(ctx)
	t := time.NewTicker(max(s.conf().CleanupInterval, time.Minute)) // 0 would panic

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
	stale := now - s.conf().UploadExpiry.Milliseconds()
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
	keep := now - s.conf().ShareRetention.Milliseconds()
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
	// Share links whose files were all deleted (older versions kept them).
	if rows, err := s.db.Query(ctx, `SELECT `+shareCols+` FROM shares s WHERE s.kind = 'download' AND NOT EXISTS (SELECT 1 FROM share_items si WHERE si.share_id = s.id)`); err == nil {
		var list []*Share
		for rows.Next() {
			if sh, err := scanShare(rows); err == nil {
				list = append(list, sh)
			}
		}
		rows.Close()
		for _, sh := range list {
			s.deleteShare(ctx, sh)
			res["emptyShares"]++
		}
	}
	exec("audit", `DELETE FROM audit_log WHERE at < ?`, now-s.conf().AuditRetention.Milliseconds())
	exec("linkEvents", `DELETE FROM share_events WHERE at < ?`, now-s.conf().AuditRetention.Milliseconds())
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
	c := s.conf()
	if c.SMTPHost == "" {
		return fmt.Errorf("smtp not configured")
	}
	from := c.SMTPFrom
	if from == "" {
		from = c.SMTPUser
	}
	// "Name <addr>" is accepted for the sender; the envelope needs the bare address.
	if a, err := mail.ParseAddress(from); err == nil {
		from = a.Address
	}
	if from == "" || strings.ContainsAny(from, "\r\n<>") {
		return fmt.Errorf("no sender address: set the email sender address (FERRY_SMTP_FROM), e.g. ferry@example.com")
	}
	if strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("invalid recipient")
	}
	helo := heloName(c.PublicURL)
	domain := helo
	if _, d, ok := strings.Cut(from, "@"); ok && d != "" {
		domain = d
	}
	// Names and subjects are user-controlled: fold all whitespace (incl. CR/LF) and RFC 2047-encode them.
	subject = mime.QEncoding.Encode("utf-8", strings.Join(strings.Fields(subject), " "))
	sender := (&mail.Address{Name: strings.Join(strings.Fields(c.SiteName), " "), Address: from}).String()
	var qp strings.Builder
	qw := quotedprintable.NewWriter(&qp) // safe for any relay: 7-bit, short lines
	qw.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	qw.Close()
	msg := "From: " + sender + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMessage-ID: <" + newID() + "@" + domain + ">\r\nDate: " + time.Now().Format(time.RFC1123Z) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" + qp.String()
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	tlsCfg := &tls.Config{ServerName: c.SMTPHost, InsecureSkipVerify: c.SMTPSkipVerify} //nolint:gosec // opt-in via FERRY_SMTP_SKIP_VERIFY
	implicitTLS := c.SMTPSecurity == "tls" || (c.SMTPSecurity == "auto" && c.SMTPPort == 465)
	// Explicit dial + deadline: smtp.SendMail has no timeout and would hang forever on a stuck server.
	d := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if implicitTLS {
		conn, err = tls.DialWithDialer(d, "tcp", addr, tlsCfg)
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return smtpHint("connect to "+addr, err, implicitTLS)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Minute))
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		conn.Close()
		if !implicitTLS && c.SMTPPort == 465 {
			return fmt.Errorf("no answer from %s: port 465 needs connection security \"tls\"", addr)
		}
		return smtpHint("greeting from "+addr, err, implicitTLS)
	}
	defer cl.Close()
	// Some servers reject "localhost" in EHLO; announce the public host name when there is one.
	if err := cl.Hello(helo); err != nil {
		return fmt.Errorf("EHLO rejected: %w", err)
	}
	if !implicitTLS && c.SMTPSecurity != "none" {
		ok, _ := cl.Extension("STARTTLS")
		if !ok && c.SMTPSecurity == "starttls" {
			return fmt.Errorf("the mail server does not offer STARTTLS (use connection security \"none\" for a plain relay, or \"tls\" for port 465)")
		}
		if ok {
			if err := cl.StartTLS(tlsCfg); err != nil {
				return smtpHint("STARTTLS", err, true)
			}
		}
	}
	hint := ""
	if c.SMTPUser != "" {
		if ok, _ := cl.Extension("AUTH"); ok {
			if err := cl.Auth(smtpAuth(cl, c.SMTPUser, c.SMTPPass, c.SMTPHost)); err != nil {
				return fmt.Errorf("sign-in rejected (check username and password; Gmail and Outlook need an app password): %w", err)
			}
		} else if _, isTLS := cl.TLSConnectionState(); !isTLS {
			// Relays that trust the sender's IP don't offer AUTH; others only offer it after STARTTLS.
			hint = " (the server didn't offer sign-in on this unencrypted connection; try connection security \"auto\" or \"starttls\")"
		} else {
			hint = " (the server didn't offer sign-in, so none was attempted)"
		}
	}
	if err := cl.Mail(from); err != nil {
		return fmt.Errorf("sender %s rejected%s: %w", from, hint, err)
	}
	if err := cl.Rcpt(to); err != nil {
		return fmt.Errorf("recipient %s rejected%s: %w", to, hint, err)
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

// heloName is the host name announced to the mail server.
func heloName(publicURL string) string {
	if u, err := url.Parse(publicURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	if h, err := os.Hostname(); err == nil && h != "" && !strings.ContainsAny(h, " _") {
		return h
	}
	return "localhost"
}

// smtpHint adds the likely fix to common connection and TLS failures.
func smtpHint(step string, err error, tlsUsed bool) error {
	var cv *tls.CertificateVerificationError
	var he x509.HostnameError
	var ua x509.UnknownAuthorityError
	var rh tls.RecordHeaderError
	switch {
	case errors.As(err, &cv) || errors.As(err, &he) || errors.As(err, &ua):
		return fmt.Errorf("%s: the mail server's certificate isn't trusted (turn on \"Accept self-signed certificates\" if it is your own server): %w", step, err)
	case errors.As(err, &rh) && tlsUsed:
		return fmt.Errorf("%s: the server doesn't speak TLS on this port (use connection security \"starttls\" or \"auto\" for port 587/25): %w", step, err)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("%s: timed out (check host, port and firewall; many hosting providers block outgoing port 25): %w", step, err)
	}
	return fmt.Errorf("%s: %w", step, err)
}

// smtpAuth picks PLAIN or LOGIN from what the server advertises. Go's PlainAuth refuses to send
// credentials over an unencrypted connection to anything but localhost; with FERRY_SMTP_SECURITY=none
// the admin explicitly chose a plain relay (e.g. a local SMTP proxy), so that check is waived.
func smtpAuth(cl *smtp.Client, user, pass, host string) smtp.Auth {
	_, mechs := cl.Extension("AUTH")
	if !strings.Contains(strings.ToUpper(mechs), "PLAIN") && strings.Contains(strings.ToUpper(mechs), "LOGIN") {
		return &loginAuth{user, pass}
	}
	return plainAuth{smtp.PlainAuth("", user, pass, host)}
}

type plainAuth struct{ smtp.Auth }

func (a plainAuth) Start(si *smtp.ServerInfo) (string, []byte, error) {
	si.TLS = true // see smtpAuth: the connection security is the admin's choice
	return a.Auth.Start(si)
}

type loginAuth struct{ user, pass string }

func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }
func (a *loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if strings.Contains(strings.ToLower(string(from)), "user") {
		return []byte(a.user), nil
	}
	return []byte(a.pass), nil
}

func (s *Server) notifyUpload(owner *User, sh *Share, f *File, uploader string) {
	who := "Someone"
	if uploader != "" {
		who = uploader
	}
	body := who + " uploaded \"" + f.Name + "\" (" + humanSize(f.Size) + ") to your upload link \"" + sh.Name + "\" on " + s.conf().SiteName + "."
	s.notifyOwner(owner, sh, "New files received: "+sh.Name, body)
}

// notifyOwner emails and/or pushes (Gotify) a link event to its owner.
// Bursts are coalesced: at most one notification per link per 10 minutes.
func (s *Server) notifyOwner(owner *User, sh *Share, title, body string) {
	if !s.limiter.allow("notify:"+sh.ID, 1, 10*time.Minute) {
		return
	}
	if s.conf().SMTPHost != "" {
		mailBody := body + "\n"
		if s.conf().PublicURL != "" {
			mailBody += "\nOpen " + s.conf().SiteName + ": " + s.conf().PublicURL + "/links\n"
		}
		if err := s.mail(owner.Email, title, mailBody); err != nil {
			s.log.Warn("link notification email failed", "err", err)
		}
	}
	if err := s.gotify(context.Background(), owner.ID, title, body); err != nil {
		s.log.Warn("gotify notification failed", "err", err)
	}
}
