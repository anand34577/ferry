package server

// Public, account-free pages for share links (/s/) and upload links (/u/).
// Rendered on the server so the core action works without JavaScript.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"ferry/internal/db"
)

type pageData struct {
	SiteName    string
	Title       string
	Share       *Share
	Owner       string
	Entries     []pageEntry
	TotalBytes  int64
	Locked      bool
	NeedLogin   bool
	Error       string
	Heading     string
	Message     string
	Token       string
	OneTime     bool
	Remaining   int
	WindowHours int
	Uploaded    []pageEntry
	CanDelete   bool
	AcceptAttr  string
	Now         int64
	AssetVer    string
}

type pageEntry struct {
	ID, Path, Mime, SHA256 string
	Size                   int64
	Preview                bool
	Mine                   bool
}

func (s *Server) render(w http.ResponseWriter, status int, tmpl string, d *pageData) {
	d.SiteName = s.conf().SiteName
	d.Now = nowMs()
	d.AssetVer = assetVer
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, tmpl, d); err != nil {
		s.log.Error("render template", "tmpl", tmpl, "err", err)
	}
}

func (s *Server) messagePage(w http.ResponseWriter, status int, heading, msg string) {
	s.render(w, status, "message.html", &pageData{Title: heading, Heading: heading, Message: msg})
}

// publicShare loads a share by token and checks it is usable; on failure it renders the page.
func (s *Server) publicShare(w http.ResponseWriter, r *http.Request, kind string) (*Share, bool) {
	ctx := r.Context()
	if !s.limiter.allow("public:"+s.clientIP(r), 300, time.Minute) {
		s.messagePage(w, 429, "Slow down", "Too many requests from your network. Please wait a minute and try again.")
		return nil, false
	}
	var ownerName string
	var disabled int
	row := s.db.QueryRow(ctx, `SELECT `+shareCols+`, u.name, u.disabled FROM shares s JOIN users u ON u.id = s.user_id WHERE s.token = ?`, r.PathValue("token"))
	sh, err := scanShareExtra(row, &ownerName, &disabled)
	if db.IsNoRows(err) || (err == nil && sh.Kind != kind) {
		s.messagePage(w, 404, "Link not found", "This link doesn't exist or has been deleted. Check that you copied the whole link.")
		return nil, false
	}
	if err != nil {
		s.log.Error("load share", "err", err)
		s.messagePage(w, 500, "Something went wrong", "The server could not load this link. Please try again in a moment.")
		return nil, false
	}
	sh.OwnerName = ownerName
	switch {
	case sh.Revoked || disabled == 1:
		s.messagePage(w, 410, "Link disabled", "This link was disabled by its owner.")
		return nil, false
	case sh.Status == "expired":
		s.messagePage(w, 410, "Link expired", "This link has expired. Ask the sender for a new one.")
		return nil, false
	}
	return sh, true
}

func scanShareExtra(row interface{ Scan(...any) error }, owner *string, disabled *int) (*Share, error) {
	var sh *Share
	err := row.Scan(scanDest(&sh, owner, disabled)...)
	if err != nil {
		return nil, err
	}
	sh.HasPassword = sh.passwordHash != ""
	sh.Status = sh.status()
	return sh, nil
}

// scanDest returns Scan destinations for shareCols plus extras, filling *out.
func scanDest(out **Share, extra ...any) []any {
	sh := &Share{}
	*out = sh
	dest := []any{&sh.ID, &sh.Token, &sh.UserID, &sh.Kind, &sh.Name, &sh.Message, &sh.passwordHash, &sh.ExpiresAt, &sh.MaxDownloads,
		&sh.DownloadCount, boolScan{&sh.AllowDownload}, boolScan{&sh.AllowPreview}, boolScan{&sh.RequireAuth}, boolScan{&sh.AllowList},
		boolScan{&sh.AllowDelete}, &sh.MaxFileBytes, &sh.MaxFiles, &sh.AllowedTypes, &sh.FolderID, &sh.UploadCount, &sh.UploadedBytes,
		boolScan{&sh.Notify}, boolScan{&sh.Revoked}, &sh.LastAccess, &sh.CreatedAt, &sh.UpdatedAt, &sh.ShortURL, &sh.shortID}
	return append(dest, extra...)
}

// boolScan scans an INTEGER 0/1 column into a bool.
type boolScan struct{ b *bool }

func (bs boolScan) Scan(v any) error {
	switch x := v.(type) {
	case int64:
		*bs.b = x != 0
	case int32:
		*bs.b = x != 0
	case bool:
		*bs.b = x
	case nil:
		*bs.b = false
	default:
		return errors.New("boolScan: unexpected type")
	}
	return nil
}

// ---------- unlock (password) and auth gates ----------

func (s *Server) unlockCookieName(sh *Share) string { return "fsu_" + sh.ID[:12] }

func (s *Server) unlockSig(sh *Share, exp string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte("unlock|" + sh.ID + "|" + exp + "|" + sh.passwordHash))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) isUnlocked(r *http.Request, sh *Share) bool {
	if !sh.HasPassword {
		return true
	}
	c, err := r.Cookie(s.unlockCookieName(sh))
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || n < nowMs() {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.unlockSig(sh, exp)))
}

func (s *Server) sharePrefix(sh *Share) string {
	if sh.Kind == "upload" {
		return "/u/" + sh.Token
	}
	return "/s/" + sh.Token
}

// gate enforces require-auth and password. Returns false when it rendered a page instead.
func (s *Server) gate(w http.ResponseWriter, r *http.Request, sh *Share, tmpl string, render bool) bool {
	if sh.RequireAuth {
		if _, err := s.authenticate(r); err != nil {
			if render {
				s.render(w, 401, tmpl, &pageData{Title: "Sign in required", Share: sh, NeedLogin: true, Token: sh.Token})
			} else {
				s.writeErr(w, r, errf(401, "login_required", "Sign in to access this link."))
			}
			return false
		}
	}
	if !s.isUnlocked(r, sh) {
		if render {
			s.render(w, 401, tmpl, &pageData{Title: "Password required", Share: sh, Locked: true, Token: sh.Token})
		} else {
			s.writeErr(w, r, errf(401, "password_required", "This link is password protected."))
		}
		return false
	}
	return true
}

// handleSharePassword verifies a link password (form POST) and sets the unlock cookie.
func (s *Server) handleSharePassword(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.publicShare(w, r, "download")
	if !ok {
		return
	}
	s.unlock(w, r, sh, "share.html")
}

func (s *Server) unlock(w http.ResponseWriter, r *http.Request, sh *Share, tmpl string) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	pw := r.PostFormValue("password")
	if !s.limiter.allow("sharepw:"+s.clientIP(r)+":"+sh.ID, 10, time.Minute) {
		s.render(w, 429, tmpl, &pageData{Title: "Password required", Share: sh, Locked: true, Token: sh.Token,
			Error: "Too many attempts. Please wait a minute and try again."})
		return
	}
	if !checkPassword(sh.passwordHash, pw) {
		s.audit(r.Context(), r, sh.UserID, "share_password_failed", sh.ID, "")
		s.linkEvent(r.Context(), r, sh, "password_failed", "")
		s.render(w, 401, tmpl, &pageData{Title: "Password required", Share: sh, Locked: true, Token: sh.Token, Error: "Incorrect password. Please try again."})
		return
	}
	exp := strconv.FormatInt(nowMs()+12*3600*1000, 10)
	http.SetCookie(w, &http.Cookie{Name: s.unlockCookieName(sh), Value: exp + "." + s.unlockSig(sh, exp), Path: s.sharePrefix(sh),
		HttpOnly: true, Secure: s.secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	http.Redirect(w, r, s.sharePrefix(sh), http.StatusSeeOther)
}

// ---------- download links ----------

func (s *Server) shareEntries(ctx context.Context, sh *Share) ([]entry, error) {
	items, err := s.shareItems(ctx, sh)
	if err != nil {
		return nil, err
	}
	sh.Items = items
	files, folders := sh.itemIDs()
	return s.collectEntries(ctx, sh.UserID, files, folders)
}

func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.publicShare(w, r, "download")
	if !ok || !s.gate(w, r, sh, "share.html", true) {
		return
	}
	d := &pageData{Title: sh.Name, Share: sh, Token: sh.Token, Owner: sh.OwnerName, WindowHours: int(s.conf().DownloadWindow.Hours())}
	hasSession := s.validDownloadSession(r, sh) != ""
	if sh.Status == "exhausted" && !hasSession {
		heading := "Download limit reached"
		msg := "This link has reached its download limit and can't be used again."
		if sh.MaxDownloads == 1 {
			heading, msg = "Link already used", "This was a one-time link and it has already been downloaded."
		}
		s.messagePage(w, 410, heading, msg)
		return
	}
	entries, err := s.shareEntries(r.Context(), sh)
	if err != nil {
		s.log.Error("share entries", "err", err)
		s.messagePage(w, 500, "Something went wrong", "The server could not load this link. Please try again.")
		return
	}
	for _, e := range entries {
		d.Entries = append(d.Entries, pageEntry{ID: e.File.ID, Path: e.Path, Size: e.File.Size, Mime: e.File.Mime, SHA256: e.File.SHA256,
			Preview: sh.AllowPreview && sh.MaxDownloads == 0 && isPreviewable(e.File.Mime)})
		d.TotalBytes += e.File.Size
	}
	d.OneTime = sh.MaxDownloads == 1
	if sh.MaxDownloads > 1 {
		d.Remaining = sh.MaxDownloads - sh.DownloadCount
	}
	s.db.Exec(r.Context(), `UPDATE shares SET last_access = ? WHERE id = ?`, nowMs(), sh.ID)
	s.linkEvent(r.Context(), r, sh, "view", "")
	s.render(w, 200, "share.html", d)
}

func (s *Server) dsCookieName(sh *Share) string { return "fds_" + sh.ID[:12] }

// validDownloadSession returns the session ID if the caller already holds one for this share.
func (s *Server) validDownloadSession(r *http.Request, sh *Share) string {
	v := r.Header.Get("X-Download-Session")
	if c, err := r.Cookie(s.dsCookieName(sh)); err == nil && v == "" {
		v = c.Value
	}
	if v == "" {
		return ""
	}
	var exp int64
	if err := s.db.QueryRow(r.Context(), `SELECT expires_at FROM download_sessions WHERE id = ? AND share_id = ?`, hashToken(v), sh.ID).Scan(&exp); err != nil || exp < nowMs() {
		return ""
	}
	return v
}

// claimDownload returns an existing session or atomically consumes one use of the share.
// This is what makes one-time links safe under concurrent requests.
func (s *Server) claimDownload(w http.ResponseWriter, r *http.Request, sh *Share) error {
	if s.validDownloadSession(r, sh) != "" {
		return nil
	}
	ctx := r.Context()
	now := nowMs()
	res, err := s.db.Exec(ctx, `UPDATE shares SET download_count = download_count + 1, last_access = ?
		WHERE id = ? AND revoked = 0 AND (max_downloads = 0 OR download_count < max_downloads)`, now, sh.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if sh.MaxDownloads == 1 {
			return errf(410, "already_used", "This one-time link has already been downloaded.")
		}
		return errf(410, "limit_reached", "This link has reached its download limit.")
	}
	tok := newToken(24)
	exp := now + s.conf().DownloadWindow.Milliseconds()
	if _, err := s.db.Exec(ctx, `INSERT INTO download_sessions (id, share_id, created_at, expires_at, ip) VALUES (?, ?, ?, ?, ?)`,
		hashToken(tok), sh.ID, now, exp, s.clientIP(r)); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: s.dsCookieName(sh), Value: tok, Path: "/s/" + sh.Token, HttpOnly: true,
		Secure: s.secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: int(s.conf().DownloadWindow.Seconds())})
	w.Header().Set("Ferry-Download-Session", tok)
	r.Header.Set("X-Download-Session", tok)
	s.audit(ctx, r, sh.UserID, "share_downloaded", sh.ID, sh.Name)
	if sh.Notify {
		if owner, err := s.uploadOwner(ctx, sh); err == nil {
			go s.notifyOwner(owner, sh, "Link downloaded: "+sh.Name, "Someone downloaded \""+sh.Name+"\" from your link on "+s.conf().SiteName+".")
		}
	}
	return nil
}

func (s *Server) publicErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiErr
	if errors.As(err, &ae) {
		title := map[int]string{404: "Not found", 410: "Link unavailable", 401: "Access denied", 403: "Access denied", 503: "Temporarily unavailable"}[ae.Status]
		if title == "" {
			title = "Something went wrong"
		}
		s.messagePage(w, ae.Status, title, ae.Message)
		return
	}
	s.log.Error("public request failed", "route", r.Pattern, "err", err)
	s.messagePage(w, 500, "Something went wrong", "The server could not complete this request. Please try again.")
}

func (s *Server) handleShareDownload(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.publicShare(w, r, "download")
	if !ok || !s.gate(w, r, sh, "share.html", true) {
		return
	}
	entries, err := s.shareEntries(r.Context(), sh)
	if err != nil {
		s.publicErr(w, r, err)
		return
	}
	var f *File
	for _, e := range entries {
		if e.File.ID == r.PathValue("fileId") {
			f = e.File
			break
		}
	}
	if f == nil {
		s.publicErr(w, r, errf(404, "not_found", "This file is no longer part of the link."))
		return
	}
	inline := r.URL.Query().Get("inline") == "1"
	if inline {
		if !sh.AllowPreview || sh.MaxDownloads > 0 || !isPreviewable(f.Mime) {
			s.publicErr(w, r, errf(403, "no_preview", "Preview isn't available for this file."))
			return
		}
	} else {
		if !sh.AllowDownload {
			s.publicErr(w, r, errf(403, "no_download", "The owner has disabled downloads for this link."))
			return
		}
		if err := s.checkBlobs([]entry{{File: f}}); err != nil {
			s.publicErr(w, r, err)
			return
		}
		if err := s.claimDownload(w, r, sh); err != nil {
			s.publicErr(w, r, err)
			return
		}
	}
	if firstRange(r) {
		kind := "download"
		if inline {
			kind = "preview"
		}
		s.linkEvent(r.Context(), r, sh, kind, f.Name)
	}
	s.serveFile(w, r, f, inline, "share:"+sh.ID, "user:"+sh.UserID)
}

func (s *Server) handleShareZip(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.publicShare(w, r, "download")
	if !ok || !s.gate(w, r, sh, "share.html", true) {
		return
	}
	if !sh.AllowDownload {
		s.publicErr(w, r, errf(403, "no_download", "The owner has disabled downloads for this link."))
		return
	}
	entries, err := s.shareEntries(r.Context(), sh)
	if err != nil {
		s.publicErr(w, r, err)
		return
	}
	if len(entries) == 0 {
		s.publicErr(w, r, errf(404, "empty", "There are no files in this link anymore."))
		return
	}
	// Check before consuming a limited download, so a missing file doesn't burn a one-time link.
	if err := s.checkBlobs(entries); err != nil {
		s.publicErr(w, r, err)
		return
	}
	if err := s.claimDownload(w, r, sh); err != nil {
		s.publicErr(w, r, err)
		return
	}
	s.linkEvent(r.Context(), r, sh, "download", "All files (zip)")
	if err := s.streamZip(w, r, cleanName(sh.Name)+".zip", entries, "share:"+sh.ID, "user:"+sh.UserID); err != nil {
		s.publicErr(w, r, err)
	}
}

// ---------- upload links ----------

func (s *Server) uploaderKey(w http.ResponseWriter, r *http.Request, sh *Share) string {
	name := "fuk_" + sh.ID[:12]
	if c, err := r.Cookie(name); err == nil && len(c.Value) >= 20 {
		return c.Value
	}
	k := newToken(16)
	http.SetCookie(w, &http.Cookie{Name: name, Value: k, Path: "/u/" + sh.Token, HttpOnly: true, Secure: s.secureCookie(r),
		SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	r.AddCookie(&http.Cookie{Name: name, Value: k})
	return k
}

func (s *Server) uploadOwner(ctx context.Context, sh *Share) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, sh.UserID))
}

func (s *Server) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.publicShare(w, r, "upload")
	if !ok || !s.gate(w, r, sh, "upload.html", true) {
		return
	}
	s.linkEvent(r.Context(), r, sh, "view", "")
	s.renderUploadPage(w, r, sh, 200, "", "")
}

func (s *Server) renderUploadPage(w http.ResponseWriter, r *http.Request, sh *Share, status int, errMsg, okMsg string) {
	key := s.uploaderKey(w, r, sh)
	d := &pageData{Title: sh.Name, Share: sh, Token: sh.Token, Owner: sh.OwnerName, Error: errMsg, Message: okMsg, CanDelete: sh.AllowDelete}
	if sh.AllowedTypes != "" {
		d.AcceptAttr = sh.AllowedTypes
	}
	if sh.Status == "exhausted" && errMsg == "" {
		d.Error = "This link has received the maximum number of files."
	}
	rows, err := s.db.Query(r.Context(), `SELECT f.id, f.name, f.size, u.uploader_key FROM uploads u JOIN files f ON f.id = u.file_id
		WHERE u.share_id = ? ORDER BY u.updated_at DESC LIMIT 500`, sh.ID)
	if err == nil {
		for rows.Next() {
			var e pageEntry
			var k string
			rows.Scan(&e.ID, &e.Path, &e.Size, &k)
			e.Mine = k == key
			if sh.AllowList || e.Mine {
				d.Uploaded = append(d.Uploaded, e)
			}
		}
		rows.Close()
	}
	s.render(w, status, "upload.html", d)
}

// handleUploadPost handles both the password form and the no-JavaScript multipart upload.
func (s *Server) handleUploadPost(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.publicShare(w, r, "upload")
	if !ok {
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		s.unlock(w, r, sh, "upload.html")
		return
	}
	if !s.gate(w, r, sh, "upload.html", true) {
		return
	}
	if sh.Status != "active" {
		s.renderUploadPage(w, r, sh, 403, "This link is not accepting more files.", "")
		return
	}
	owner, err := s.uploadOwner(r.Context(), sh)
	if err != nil {
		s.publicErr(w, r, err)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		s.renderUploadPage(w, r, sh, 400, "The upload could not be read. Please try again.", "")
		return
	}
	key := s.uploaderKey(w, r, sh)
	uploader := ""
	var names []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.renderUploadPage(w, r, sh, 400, "The upload was interrupted. Please try again.", "")
			return
		}
		if part.FormName() == "uploader" { // the form puts this field before the files
			b, _ := io.ReadAll(io.LimitReader(part, 100))
			uploader = strings.TrimSpace(string(b))
			continue
		}
		if part.FormName() != "files" || part.FileName() == "" {
			continue
		}
		f, err := s.receivePart(r.Context(), sh, owner, part, uploader, key)
		part.Close()
		if err != nil {
			var ae *apiErr
			msg := "The upload failed. Please try again."
			if errors.As(err, &ae) {
				msg = ae.Message
			} else {
				s.log.Error("form upload", "err", err)
			}
			s.renderUploadPage(w, r, sh, 400, msg, "")
			return
		}
		names = append(names, f.Name)
		sh.UploadCount++
	}
	if len(names) == 0 {
		s.renderUploadPage(w, r, sh, 400, "Please choose at least one file.", "")
		return
	}
	s.renderUploadPage(w, r, sh, 200, "", strconv.Itoa(len(names))+" file(s) uploaded successfully.")
}

// receivePart streams one multipart file into storage with hashing, then finalizes it.
func (s *Server) receivePart(ctx context.Context, sh *Share, owner *User, part *multipart.Part, uploader, key string) (*File, error) {
	name := cleanName(path.Base(strings.ReplaceAll(part.FileName(), `\`, "/")))
	if err := s.checkLinkLimits(ctx, sh, name, 0); err != nil {
		return nil, err
	}
	up := &upload{ID: newID(), UserID: owner.ID, ShareID: sh.ID, FolderID: sh.FolderID, Name: name, Conflict: "keep_both",
		Uploader: uploader, UploaderKey: key, CreatedAt: nowMs()}
	up.UpdatedAt = up.CreatedAt
	wc, err := s.store.OpenAppend("uploads/"+up.ID, 0)
	if err != nil {
		return nil, err
	}
	limit := int64(1 << 62)
	if sh.MaxFileBytes > 0 {
		limit = sh.MaxFileBytes
	}
	if s.conf().MaxUploadBytes > 0 && s.conf().MaxUploadBytes < limit {
		limit = s.conf().MaxUploadBytes
	}
	// The size is unknown until the part ends, so stop reading once the quota would be exceeded
	// instead of writing an arbitrarily large file to disk first.
	room, err := s.quotaRoom(ctx, owner)
	if err != nil {
		wc.Close()
		s.store.Remove("uploads/" + up.ID)
		return nil, err
	}
	quotaLimit := min(limit, room)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(wc, h), countingReader{io.LimitReader(part, quotaLimit+1), &s.metrics.bytesIn})
	wc.Close()
	if err == nil && n > quotaLimit && quotaLimit == limit {
		err = errf(413, "file_too_large", "\""+name+"\" is too large. Files must be at most "+humanSize(limit)+".")
	}
	if err == nil {
		err = s.checkQuota(ctx, owner, n) // also reports the quota case (n > quotaLimit)
	}
	if err != nil {
		s.store.Remove("uploads/" + up.ID)
		return nil, err
	}
	up.Size, up.Received = n, n
	if _, err := s.db.Exec(ctx, `INSERT INTO uploads (`+uploadCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		up.ID, up.UserID, up.ShareID, up.FolderID, "", up.Name, up.Size, up.Received, up.Conflict, "", "", up.Uploader, up.UploaderKey, "", up.CreatedAt, up.UpdatedAt); err != nil {
		s.store.Remove("uploads/" + up.ID)
		return nil, err
	}
	f, _, err := s.finalizeUpload(ctx, up, owner, sh, hex.EncodeToString(h.Sum(nil)))
	return f, err
}

func (s *Server) linkUpload(w http.ResponseWriter, r *http.Request) (*Share, *User, *upload, bool) {
	sh, ok := s.publicShare(w, r, "upload")
	if !ok || !s.gate(w, r, sh, "", false) {
		return nil, nil, nil, false
	}
	up, err := scanUpload(s.db.QueryRow(r.Context(), `SELECT `+uploadCols+` FROM uploads WHERE id = ? AND share_id = ?`, r.PathValue("id"), sh.ID))
	if err != nil || up.UploaderKey != s.uploaderKey(w, r, sh) {
		tusHeaders(w)
		s.writeErr(w, r, errf(404, "upload_not_found", "This upload has expired. Please start it again."))
		return nil, nil, nil, false
	}
	owner, err := s.uploadOwner(r.Context(), sh)
	if err != nil {
		s.writeErr(w, r, err)
		return nil, nil, nil, false
	}
	return sh, owner, up, true
}

func (s *Server) tusCreateLink(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.publicShare(w, r, "upload")
	if !ok || !s.gate(w, r, sh, "", false) {
		return
	}
	if sh.Status != "active" {
		tusHeaders(w)
		s.writeErr(w, r, errf(403, "limit_reached", "This link is not accepting more files."))
		return
	}
	if !s.limiter.allow("linkupload:"+s.clientIP(r), 600, time.Hour) {
		tusHeaders(w)
		s.writeErr(w, r, errf(429, "rate_limited", "Too many uploads from your network. Please try again later."))
		return
	}
	owner, err := s.uploadOwner(r.Context(), sh)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.tusCreate(w, r, owner, sh, "/u/"+sh.Token+"/tus/")
}

func (s *Server) tusHeadLink(w http.ResponseWriter, r *http.Request) {
	if _, _, up, ok := s.linkUpload(w, r); ok {
		s.tusHead(w, r, up, nil)
	}
}

func (s *Server) tusPatchLink(w http.ResponseWriter, r *http.Request) {
	if sh, owner, up, ok := s.linkUpload(w, r); ok {
		s.tusPatch(w, r, up, owner, sh)
	}
}

func (s *Server) tusDeleteLink(w http.ResponseWriter, r *http.Request) {
	if _, _, up, ok := s.linkUpload(w, r); ok {
		s.tusDelete(w, r, up, nil)
	}
}

// handleUploadDelete lets an anonymous uploader remove a file they uploaded (if the owner allows).
func (s *Server) handleUploadDelete(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.publicShare(w, r, "upload")
	if !ok || !s.gate(w, r, sh, "upload.html", true) {
		return
	}
	if !sh.AllowDelete {
		s.renderUploadPage(w, r, sh, 403, "The owner doesn't allow deleting uploaded files.", "")
		return
	}
	key := s.uploaderKey(w, r, sh)
	var fileID string
	err := s.db.QueryRow(r.Context(), `SELECT file_id FROM uploads WHERE share_id = ? AND file_id = ? AND uploader_key = ?`, sh.ID, r.PathValue("fileId"), key).Scan(&fileID)
	if err != nil {
		s.renderUploadPage(w, r, sh, 404, "You can only delete files you uploaded from this browser.", "")
		return
	}
	if err := s.deleteItems(r.Context(), sh.UserID, []string{fileID}, nil); err != nil {
		s.publicErr(w, r, err)
		return
	}
	s.db.Exec(r.Context(), `DELETE FROM uploads WHERE share_id = ? AND file_id = ?`, sh.ID, fileID)
	s.db.Exec(r.Context(), `UPDATE shares SET upload_count = upload_count - 1 WHERE id = ? AND upload_count > 0`, sh.ID)
	http.Redirect(w, r, "/u/"+sh.Token, http.StatusSeeOther)
}

// fileExt returns a short uppercase extension label for file badges.
func fileExt(name string) string {
	e := strings.ToUpper(strings.TrimPrefix(path.Ext(name), "."))
	if e == "" || len(e) > 4 {
		return "FILE"
	}
	return e
}

// fileKind groups a MIME type into a badge colour class.
func fileKind(mime string) string {
	m := baseMime(mime)
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "video/"):
		return "video"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	case m == "application/pdf":
		return "pdf"
	case strings.Contains(m, "zip") || strings.Contains(m, "compressed") || strings.Contains(m, "tar"):
		return "archive"
	case strings.HasPrefix(m, "text/"):
		return "text"
	}
	return "other"
}
