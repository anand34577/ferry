package server

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"ferry/internal/db"
)

type Share struct {
	ID            string      `json:"id"`
	Token         string      `json:"token"`
	URL           string      `json:"url"`
	Kind          string      `json:"kind"` // download | upload
	Name          string      `json:"name"`
	Message       string      `json:"message"`
	HasPassword   bool        `json:"hasPassword"`
	ExpiresAt     int64       `json:"expiresAt"`
	MaxDownloads  int         `json:"maxDownloads"`
	DownloadCount int         `json:"downloadCount"`
	AllowDownload bool        `json:"allowDownload"`
	AllowPreview  bool        `json:"allowPreview"`
	RequireAuth   bool        `json:"requireAuth"`
	AllowList     bool        `json:"allowList"`
	AllowDelete   bool        `json:"allowDelete"`
	MaxFileBytes  int64       `json:"maxFileBytes"`
	MaxFiles      int         `json:"maxFiles"`
	AllowedTypes  string      `json:"allowedTypes"`
	FolderID      string      `json:"folderId"`
	UploadCount   int         `json:"uploadCount"`
	UploadedBytes int64       `json:"uploadedBytes"`
	Notify        bool        `json:"notify"`
	Revoked       bool        `json:"revoked"`
	LastAccess    int64       `json:"lastAccess"`
	CreatedAt     int64       `json:"createdAt"`
	UpdatedAt     int64       `json:"updatedAt"`
	Status        string      `json:"status"` // active | expired | revoked | exhausted
	Items         []shareItem `json:"items,omitempty"`
	ItemCount     int         `json:"itemCount"`
	OwnerName     string      `json:"ownerName,omitempty"`
	ShortURL      string      `json:"shortUrl"` // from the optional URL shortener; "" when disabled or it failed
	shortID       string
	UserID        string `json:"-"`
	passwordHash  string
}

type shareItem struct {
	Type     string `json:"type"` // file | folder
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size,omitempty"`
	FolderID string `json:"folderId"` // where a shared file lives ("" = My files), to open it from the link
}

const shareCols = `s.id, s.token, s.user_id, s.kind, s.name, s.message, s.password_hash, s.expires_at, s.max_downloads, s.download_count,
	s.allow_download, s.allow_preview, s.require_auth, s.allow_list, s.allow_delete, s.max_file_bytes, s.max_files, s.allowed_types,
	s.folder_id, s.upload_count, s.uploaded_bytes, s.notify, s.revoked, s.last_access, s.created_at, s.updated_at, s.short_url, s.short_id`

func scanShare(row interface{ Scan(...any) error }) (*Share, error) {
	var sh *Share
	if err := row.Scan(scanDest(&sh)...); err != nil {
		return nil, err
	}
	sh.HasPassword = sh.passwordHash != ""
	sh.Status = sh.status()
	return sh, nil
}

func (sh *Share) status() string {
	switch {
	case sh.Revoked:
		return "revoked"
	case sh.ExpiresAt > 0 && sh.ExpiresAt <= nowMs():
		return "expired"
	case sh.Kind == "download" && sh.MaxDownloads > 0 && sh.DownloadCount >= sh.MaxDownloads:
		return "exhausted"
	case sh.Kind == "upload" && sh.MaxFiles > 0 && sh.UploadCount >= sh.MaxFiles:
		return "exhausted"
	}
	return "active"
}

func (s *Server) shareURL(r *http.Request, sh *Share) string {
	p := "/s/"
	if sh.Kind == "upload" {
		p = "/u/"
	}
	return s.baseURL(r) + p + sh.Token
}

func (s *Server) getShare(ctx context.Context, userID, id string) (*Share, error) {
	sh, err := scanShare(s.db.QueryRow(ctx, `SELECT `+shareCols+` FROM shares s WHERE s.id = ? AND s.user_id = ?`, id, userID))
	if db.IsNoRows(err) {
		return nil, errf(404, "share_not_found", "That link no longer exists.")
	}
	return sh, err
}

func (s *Server) shareItems(ctx context.Context, sh *Share) ([]shareItem, error) {
	rows, err := s.db.Query(ctx, `SELECT si.item_type, si.item_id, COALESCE(f.name, fo.name, ''), COALESCE(f.size, 0), COALESCE(f.folder_id, '')
		FROM share_items si
		LEFT JOIN files f ON si.item_type = 'file' AND f.id = si.item_id
		LEFT JOIN folders fo ON si.item_type = 'folder' AND fo.id = si.item_id
		WHERE si.share_id = ?`, sh.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []shareItem{}
	for rows.Next() {
		var it shareItem
		if err := rows.Scan(&it.Type, &it.ID, &it.Name, &it.Size, &it.FolderID); err != nil {
			return nil, err
		}
		if it.Name != "" {
			items = append(items, it)
		}
	}
	return items, rows.Err()
}

func (sh *Share) itemIDs() (files, folders []string) {
	for _, it := range sh.Items {
		if it.Type == "file" {
			files = append(files, it.ID)
		} else {
			folders = append(folders, it.ID)
		}
	}
	return
}

// ---------- API ----------

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	q := `SELECT ` + shareCols + ` FROM shares s WHERE s.user_id = ?`
	args := []any{u.ID}
	if k := r.URL.Query().Get("kind"); k == "download" || k == "upload" {
		q += ` AND s.kind = ?`
		args = append(args, k)
	}
	rows, err := s.db.Query(ctx, q+` ORDER BY s.created_at DESC`, args...)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	shares := []*Share{}
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			rows.Close()
			s.writeErr(w, r, err)
			return
		}
		sh.URL = s.shareURL(r, sh)
		shares = append(shares, sh)
	}
	rows.Close()
	items := map[string][]shareItem{}
	if rows, err := s.db.Query(ctx, `SELECT si.share_id, si.item_type, si.item_id, COALESCE(f.name, fo.name, ''), COALESCE(f.size, 0), COALESCE(f.folder_id, '')
		FROM share_items si JOIN shares s ON s.id = si.share_id
		LEFT JOIN files f ON si.item_type = 'file' AND f.id = si.item_id
		LEFT JOIN folders fo ON si.item_type = 'folder' AND fo.id = si.item_id
		WHERE s.user_id = ?`, u.ID); err == nil {
		for rows.Next() {
			var sid string
			var it shareItem
			if rows.Scan(&sid, &it.Type, &it.ID, &it.Name, &it.Size, &it.FolderID) == nil && it.Name != "" {
				items[sid] = append(items[sid], it)
			}
		}
		rows.Close()
	}
	for _, sh := range shares {
		sh.Items = items[sh.ID]
		sh.ItemCount = len(sh.Items)
	}
	writeJSON(w, 200, map[string]any{"shares": shares})
}

func (s *Server) handleGetShare(w http.ResponseWriter, r *http.Request) {
	sh, err := s.getShare(r.Context(), userOf(r).ID, r.PathValue("id"))
	if err == nil {
		sh.Items, err = s.shareItems(r.Context(), sh)
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sh.URL = s.shareURL(r, sh)
	sh.ItemCount = len(sh.Items)
	writeJSON(w, 200, sh)
}

type shareReq struct {
	Kind          *string  `json:"kind"`
	Name          *string  `json:"name"`
	Message       *string  `json:"message"`
	Password      *string  `json:"password"` // "" removes the password
	ExpiresAt     *int64   `json:"expiresAt"`
	ExpiresIn     *int64   `json:"expiresIn"` // seconds from now (server clock); 0 = never
	MaxDownloads  *int     `json:"maxDownloads"`
	AllowDownload *bool    `json:"allowDownload"`
	AllowPreview  *bool    `json:"allowPreview"`
	RequireAuth   *bool    `json:"requireAuth"`
	AllowList     *bool    `json:"allowList"`
	AllowDelete   *bool    `json:"allowDelete"`
	MaxFileBytes  *int64   `json:"maxFileBytes"`
	MaxFiles      *int     `json:"maxFiles"`
	AllowedTypes  *string  `json:"allowedTypes"`
	FolderID      *string  `json:"folderId"`
	Notify        *bool    `json:"notify"`
	Revoked       *bool    `json:"revoked"`
	FileIDs       []string `json:"fileIds"`
	FolderIDs     []string `json:"folderIds"`
}

// normalizeTypes turns "pdf, .JPG" into ".pdf,.jpg".
func normalizeTypes(s string) string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, ".") {
			p = "." + p
		}
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

// apply copies request fields onto sh and validates the result.
func (s *Server) applyShareReq(ctx context.Context, u *User, sh *Share, req *shareReq, creating bool) error {
	now := nowMs()
	if req.Name != nil {
		sh.Name = strings.TrimSpace(*req.Name)
		if len(sh.Name) > 200 {
			sh.Name = sh.Name[:200]
		}
	}
	if req.Message != nil {
		sh.Message = strings.TrimSpace(*req.Message)
		if len(sh.Message) > 2000 {
			return errf(400, "message_too_long", "The message can be at most 2000 characters.")
		}
	}
	if req.Password != nil {
		if *req.Password == "" {
			sh.passwordHash = ""
		} else {
			if len(*req.Password) < 4 || len(*req.Password) > 72 {
				return errf(400, "bad_password", "Link passwords must be 4–72 characters.")
			}
			h, err := hashPassword(*req.Password)
			if err != nil {
				return err
			}
			sh.passwordHash = h
		}
	}
	const maxExpiry = 100 * 365 * 24 * 3600 // seconds; larger values would overflow into "never expires"
	if (req.ExpiresIn != nil && *req.ExpiresIn > maxExpiry) || (req.ExpiresAt != nil && *req.ExpiresAt > now+maxExpiry*1000) {
		return errf(400, "bad_expiry", "The expiry time is too far in the future. Choose \"Never\" instead.")
	}
	if req.ExpiresIn != nil {
		if *req.ExpiresIn <= 0 {
			sh.ExpiresAt = 0
		} else {
			sh.ExpiresAt = now + *req.ExpiresIn*1000
		}
	} else if req.ExpiresAt != nil {
		if *req.ExpiresAt != 0 && *req.ExpiresAt <= now {
			return errf(400, "bad_expiry", "The expiry time must be in the future.")
		}
		sh.ExpiresAt = *req.ExpiresAt
	}
	setB := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	setB(&sh.AllowDownload, req.AllowDownload)
	setB(&sh.AllowPreview, req.AllowPreview)
	setB(&sh.RequireAuth, req.RequireAuth)
	setB(&sh.AllowList, req.AllowList)
	setB(&sh.AllowDelete, req.AllowDelete)
	setB(&sh.Notify, req.Notify)
	setB(&sh.Revoked, req.Revoked)
	if req.MaxDownloads != nil {
		if *req.MaxDownloads < 0 {
			return errf(400, "bad_limit", "Download limit can't be negative.")
		}
		sh.MaxDownloads = *req.MaxDownloads
	}
	if req.MaxFileBytes != nil && *req.MaxFileBytes >= 0 {
		sh.MaxFileBytes = *req.MaxFileBytes
	}
	if req.MaxFiles != nil && *req.MaxFiles >= 0 {
		sh.MaxFiles = *req.MaxFiles
	}
	if req.AllowedTypes != nil {
		sh.AllowedTypes = normalizeTypes(*req.AllowedTypes)
	}
	if sh.MaxDownloads > 0 {
		sh.AllowPreview = false // a preview would consume a limited use
	}
	if !s.conf().PublicSharing {
		sh.RequireAuth = true
	}
	if s.conf().MaxShareFiles > 0 && (sh.MaxFiles == 0 || sh.MaxFiles > s.conf().MaxShareFiles) && sh.Kind == "upload" {
		sh.MaxFiles = s.conf().MaxShareFiles
	}
	if sh.Kind == "upload" {
		if req.FolderID != nil && *req.FolderID != "" {
			if _, err := s.getFolder(ctx, u.ID, *req.FolderID); err != nil {
				return err
			}
			sh.FolderID = *req.FolderID
		}
		if sh.FolderID == "" && creating {
			// Default destination: a new folder named after the link.
			base := sh.Name
			if base == "" {
				base = "Received files"
			}
			base = cleanName(base)
			for i := 0; i < 1000; i++ {
				name := base
				if i > 0 {
					name = numberedName(base, i)
				}
				id := newID()
				_, err := s.db.Exec(ctx, `INSERT INTO folders (id, user_id, parent_id, name, created_at, updated_at) VALUES (?, ?, '', ?, ?, ?)`, id, u.ID, name, now, now)
				if db.IsUnique(err) {
					continue
				}
				if err != nil {
					return err
				}
				sh.FolderID = id
				break
			}
		}
	}
	return nil
}

func (s *Server) validateShareItems(ctx context.Context, u *User, fileIDs, folderIDs []string) ([]shareItem, error) {
	var items []shareItem
	files, err := s.filesByIDs(ctx, u.ID, fileIDs)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.TransferID == "" {
			items = append(items, shareItem{Type: "file", ID: f.ID, Name: f.Name, Size: f.Size})
		}
	}
	if len(folderIDs) > 0 {
		idx, err := s.loadFolders(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range folderIDs {
			if f := idx.byID[id]; f != nil {
				items = append(items, shareItem{Type: "folder", ID: id, Name: f.Name})
			}
		}
	}
	if len(items) == 0 {
		return nil, errf(400, "no_items", "Choose at least one file or folder to share.")
	}
	if s.conf().MaxShareBytes > 0 || s.conf().MaxShareFiles > 0 {
		entries, err := s.collectEntries(ctx, u.ID, fileIDs, folderIDs)
		if err != nil {
			return nil, err
		}
		var total int64
		for _, e := range entries {
			total += e.File.Size
		}
		if s.conf().MaxShareFiles > 0 && len(entries) > s.conf().MaxShareFiles {
			return nil, errf(413, "share_too_many", "A link can contain at most "+strconv.Itoa(s.conf().MaxShareFiles)+" files.")
		}
		if s.conf().MaxShareBytes > 0 && total > s.conf().MaxShareBytes {
			return nil, errf(413, "share_too_large", "A link can contain at most "+humanSize(s.conf().MaxShareBytes)+".")
		}
	}
	return items, nil
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	var req shareReq
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if !s.limiter.allow("share:"+u.ID, 60, time.Minute) {
		s.writeErr(w, r, errf(429, "rate_limited", "You're creating links too quickly. Please wait a moment."))
		return
	}
	now := nowMs()
	sh := &Share{ID: newID(), Token: newToken(12), Kind: "download", AllowDownload: true, AllowPreview: true, CreatedAt: now, UpdatedAt: now, UserID: u.ID}
	if req.Kind != nil {
		sh.Kind = *req.Kind
	}
	if sh.Kind != "download" && sh.Kind != "upload" {
		s.writeErr(w, r, errf(400, "bad_kind", "Link type must be download or upload."))
		return
	}
	var items []shareItem
	if sh.Kind == "download" {
		var err error
		if items, err = s.validateShareItems(ctx, u, req.FileIDs, req.FolderIDs); err != nil {
			s.writeErr(w, r, err)
			return
		}
		if sh.Name == "" && len(items) == 1 {
			sh.Name = items[0].Name
		} else if len(items) > 1 {
			sh.Name = strconv.Itoa(len(items)) + " items"
		}
	} else {
		sh.Name = "Upload link"
	}
	if err := s.applyShareReq(ctx, u, sh, &req, true); err != nil {
		s.writeErr(w, r, err)
		return
	}
	err := s.db.InTx(ctx, func(tx *db.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO shares (id, token, user_id, kind, name, message, password_hash, expires_at, max_downloads, download_count,
			allow_download, allow_preview, require_auth, allow_list, allow_delete, max_file_bytes, max_files, allowed_types, folder_id,
			upload_count, uploaded_bytes, notify, revoked, last_access, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, 0, 0, ?, ?)`,
			sh.ID, sh.Token, u.ID, sh.Kind, sh.Name, sh.Message, sh.passwordHash, sh.ExpiresAt, sh.MaxDownloads,
			boolInt(sh.AllowDownload), boolInt(sh.AllowPreview), boolInt(sh.RequireAuth), boolInt(sh.AllowList), boolInt(sh.AllowDelete),
			sh.MaxFileBytes, sh.MaxFiles, sh.AllowedTypes, sh.FolderID, boolInt(sh.Notify), sh.CreatedAt, sh.UpdatedAt)
		if err != nil {
			return err
		}
		for _, it := range items {
			if _, err := tx.Exec(ctx, `INSERT INTO share_items (share_id, item_type, item_id) VALUES (?, ?, ?)`, sh.ID, it.Type, it.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sh.Items, sh.ItemCount = items, len(items)
	sh.HasPassword = sh.passwordHash != ""
	sh.Status = sh.status()
	sh.URL = s.shareURL(r, sh)
	s.shorten(ctx, sh, sh.URL)
	s.audit(ctx, r, u.ID, "share_created", sh.ID, sh.Kind+": "+sh.Name)
	writeJSON(w, 201, sh)
}

func (s *Server) handleUpdateShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	sh, err := s.getShare(ctx, u.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var req shareReq
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	req.Kind = nil
	oldHash := sh.passwordHash
	wasRevoked := sh.Revoked
	if err := s.applyShareReq(ctx, u, sh, &req, false); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var items []shareItem
	if sh.Kind == "download" && (req.FileIDs != nil || req.FolderIDs != nil) {
		if items, err = s.validateShareItems(ctx, u, req.FileIDs, req.FolderIDs); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	sh.UpdatedAt = nowMs()
	err = s.db.InTx(ctx, func(tx *db.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE shares SET name = ?, message = ?, password_hash = ?, expires_at = ?, max_downloads = ?, allow_download = ?,
			allow_preview = ?, require_auth = ?, allow_list = ?, allow_delete = ?, max_file_bytes = ?, max_files = ?, allowed_types = ?,
			folder_id = ?, notify = ?, revoked = ?, updated_at = ? WHERE id = ?`,
			sh.Name, sh.Message, sh.passwordHash, sh.ExpiresAt, sh.MaxDownloads, boolInt(sh.AllowDownload), boolInt(sh.AllowPreview),
			boolInt(sh.RequireAuth), boolInt(sh.AllowList), boolInt(sh.AllowDelete), sh.MaxFileBytes, sh.MaxFiles, sh.AllowedTypes,
			sh.FolderID, boolInt(sh.Notify), boolInt(sh.Revoked), sh.UpdatedAt, sh.ID)
		if err != nil || items == nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM share_items WHERE share_id = ?`, sh.ID); err != nil {
			return err
		}
		for _, it := range items {
			if _, err := tx.Exec(ctx, `INSERT INTO share_items (share_id, item_type, item_id) VALUES (?, ?, ?)`, sh.ID, it.Type, it.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if sh.Revoked && !wasRevoked {
		s.streams.cancel(sh.ID)
		s.audit(ctx, r, u.ID, "share_revoked", sh.ID, sh.Name)
	}
	if sh.passwordHash != oldHash {
		s.streams.cancel(sh.ID) // unlocked sessions are invalid now
	}
	sh.HasPassword = sh.passwordHash != ""
	sh.Status = sh.status()
	sh.URL = s.shareURL(r, sh)
	sh.Items, _ = s.shareItems(ctx, sh)
	sh.ItemCount = len(sh.Items)
	writeJSON(w, 200, sh)
}

func (s *Server) handleRegenerateShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	sh, err := s.getShare(ctx, u.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sh.Token = newToken(12)
	if _, err := s.db.Exec(ctx, `UPDATE shares SET token = ?, short_url = '', short_id = '', updated_at = ? WHERE id = ?`, sh.Token, nowMs(), sh.ID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.unshorten(sh.shortID) // the old short link must stop working too
	sh.ShortURL, sh.shortID = "", ""
	s.shorten(ctx, sh, s.shareURL(r, sh))
	s.db.Exec(ctx, `DELETE FROM download_sessions WHERE share_id = ?`, sh.ID)
	s.streams.cancel(sh.ID)
	s.audit(ctx, r, u.ID, "share_regenerated", sh.ID, sh.Name)
	sh.URL = s.shareURL(r, sh)
	writeJSON(w, 200, sh)
}

func (s *Server) deleteShare(ctx context.Context, sh *Share) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM shares WHERE id = ?`, sh.ID); err != nil {
		return err
	}
	s.db.Exec(ctx, `DELETE FROM share_items WHERE share_id = ?`, sh.ID)
	s.db.Exec(ctx, `DELETE FROM download_sessions WHERE share_id = ?`, sh.ID)
	s.db.Exec(ctx, `DELETE FROM share_events WHERE share_id = ?`, sh.ID)
	s.unshorten(sh.shortID)
	// Unfinished anonymous uploads die with the link; received files stay in the owner's folder.
	rows, err := s.db.Query(ctx, `SELECT id FROM uploads WHERE share_id = ? AND file_id = ''`, sh.ID)
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			s.store.Remove("uploads/" + id)
		}
	}
	s.db.Exec(ctx, `DELETE FROM uploads WHERE share_id = ?`, sh.ID)
	s.streams.cancel(sh.ID)
	return nil
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	sh, err := s.getShare(r.Context(), u.ID, r.PathValue("id"))
	if err == nil {
		err = s.deleteShare(r.Context(), sh)
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "share_deleted", sh.ID, sh.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleEmailShare(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if s.conf().SMTPHost == "" {
		s.writeErr(w, r, errf(400, "email_disabled", "Email is not configured on this server."))
		return
	}
	var req struct {
		To      string `json:"to"`
		Message string `json:"message"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	to, err := normalizeEmail(req.To)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if !s.limiter.allow("email:"+u.ID, 20, time.Hour) {
		s.writeErr(w, r, errf(429, "rate_limited", "You've sent too many emails. Please try again later."))
		return
	}
	sh, err := s.getShare(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	link := s.shareURL(r, sh)
	if sh.ShortURL != "" {
		link = sh.ShortURL
	}
	body := u.Name + " shared \"" + sh.Name + "\" with you via " + s.conf().SiteName + ".\n\n"
	if sh.Kind == "upload" {
		body = u.Name + " asked you to upload files via " + s.conf().SiteName + ".\n\n"
	}
	if m := strings.TrimSpace(req.Message); m != "" {
		body += m + "\n\n"
	}
	body += link + "\n"
	if sh.HasPassword {
		body += "\nThis link is password protected; the sender will give you the password separately.\n"
	}
	if err := s.mail(to, u.Name+" shared files with you", body); err != nil {
		s.log.Error("send share email", "err", err)
		s.writeErr(w, r, errf(502, "email_failed", "The email could not be sent. Check the server's email settings."))
		return
	}
	s.audit(r.Context(), r, u.ID, "share_emailed", sh.ID, to)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// checkLinkLimits enforces upload-link restrictions before any bytes are accepted.
func (s *Server) checkLinkLimits(ctx context.Context, sh *Share, name string, size int64) error {
	if sh.MaxFileBytes > 0 && size > sh.MaxFileBytes {
		return errf(413, "file_too_large", "\""+name+"\" is too large. Files must be at most "+humanSize(sh.MaxFileBytes)+".")
	}
	if sh.AllowedTypes != "" {
		ext := strings.ToLower(path.Ext(name))
		ok := false
		for _, t := range strings.Split(sh.AllowedTypes, ",") {
			if t == ext {
				ok = true
			}
		}
		if !ok {
			return errf(415, "type_not_allowed", "\""+name+"\" can't be uploaded. Accepted file types: "+strings.ReplaceAll(sh.AllowedTypes, ",", ", ")+".")
		}
	}
	if sh.MaxFiles > 0 {
		var pending int
		s.db.QueryRow(ctx, `SELECT COUNT(*) FROM uploads WHERE share_id = ? AND file_id = ''`, sh.ID).Scan(&pending)
		if sh.UploadCount+pending >= sh.MaxFiles {
			return errf(403, "limit_reached", "This link has reached its limit of "+strconv.Itoa(sh.MaxFiles)+" files.")
		}
	}
	return nil
}
