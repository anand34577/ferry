package server

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"ferry/internal/db"
	"ferry/internal/storage"
)

type File struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FolderID   string `json:"folderId"`
	TransferID string `json:"transferId,omitempty"`
	Size       int64  `json:"size"`
	Mime       string `json:"mime"`
	SHA256     string `json:"sha256"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	UserID     string `json:"-"`
	Blob       string `json:"-"`
}

type Folder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ParentID  string `json:"parentId"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

const fileCols = `id, name, folder_id, transfer_id, size, mime, sha256, created_at, updated_at, user_id, blob`

func scanFile(row interface{ Scan(...any) error }) (*File, error) {
	f := &File{}
	err := row.Scan(&f.ID, &f.Name, &f.FolderID, &f.TransferID, &f.Size, &f.Mime, &f.SHA256, &f.CreatedAt, &f.UpdatedAt, &f.UserID, &f.Blob)
	return f, err
}

func scanFiles(rows interface {
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}, err error) ([]*File, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*File{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Server) getFile(ctx context.Context, userID, id string) (*File, error) {
	f, err := scanFile(s.db.QueryRow(ctx, `SELECT `+fileCols+` FROM files WHERE id = ? AND user_id = ?`, id, userID))
	if db.IsNoRows(err) {
		return nil, errNotFound
	}
	return f, err
}

func (s *Server) getFolder(ctx context.Context, userID, id string) (*Folder, error) {
	f := &Folder{}
	err := s.db.QueryRow(ctx, `SELECT id, name, parent_id, created_at, updated_at FROM folders WHERE id = ? AND user_id = ?`, id, userID).
		Scan(&f.ID, &f.Name, &f.ParentID, &f.CreatedAt, &f.UpdatedAt)
	if db.IsNoRows(err) {
		return nil, errf(404, "folder_not_found", "That folder no longer exists.")
	}
	return f, err
}

// folderIndex loads every folder of a user; cheap and keeps tree logic out of SQL (no recursive CTEs).
type folderIndex struct {
	byID     map[string]*Folder
	children map[string][]string
}

func (s *Server) loadFolders(ctx context.Context, userID string) (*folderIndex, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, parent_id, created_at, updated_at FROM folders WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := &folderIndex{byID: map[string]*Folder{}, children: map[string][]string{}}
	for rows.Next() {
		f := &Folder{}
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		idx.byID[f.ID] = f
		idx.children[f.ParentID] = append(idx.children[f.ParentID], f.ID)
	}
	return idx, rows.Err()
}

// descendants returns id and every folder below it.
func (idx *folderIndex) descendants(id string) []string {
	out := []string{id}
	for i := 0; i < len(out); i++ {
		out = append(out, idx.children[out[i]]...)
	}
	return out
}

func (idx *folderIndex) path(id string) []*Folder {
	var p []*Folder
	for seen := 0; id != "" && seen < 1000; seen++ {
		f := idx.byID[id]
		if f == nil {
			break
		}
		p = append([]*Folder{f}, p...)
		id = f.ParentID
	}
	return p
}

// filesInFolders loads files of the given folders in batches.
func (s *Server) filesInFolders(ctx context.Context, userID string, folderIDs []string) ([]*File, error) {
	var out []*File
	for len(folderIDs) > 0 {
		n := min(len(folderIDs), 400)
		batch := folderIDs[:n]
		folderIDs = folderIDs[n:]
		args := []any{userID}
		for _, id := range batch {
			args = append(args, id)
		}
		fs, err := scanFiles(s.db.Query(ctx, `SELECT `+fileCols+` FROM files WHERE user_id = ? AND transfer_id = '' AND folder_id IN (?`+strings.Repeat(",?", n-1)+`)`, args...))
		if err != nil {
			return nil, err
		}
		out = append(out, fs...)
	}
	return out, nil
}

func (s *Server) filesByIDs(ctx context.Context, userID string, ids []string) ([]*File, error) {
	var out []*File
	for len(ids) > 0 {
		n := min(len(ids), 400)
		args := []any{userID}
		for _, id := range ids[:n] {
			args = append(args, id)
		}
		ids = ids[n:]
		fs, err := scanFiles(s.db.Query(ctx, `SELECT `+fileCols+` FROM files WHERE user_id = ? AND id IN (?`+strings.Repeat(",?", n-1)+`)`, args...))
		if err != nil {
			return nil, err
		}
		out = append(out, fs...)
	}
	return out, nil
}

// entry is a file with its path inside an archive or share listing.
type entry struct {
	File *File
	Path string
}

// collectEntries expands files and folders into a flat, de-duplicated list with relative paths.
func (s *Server) collectEntries(ctx context.Context, userID string, fileIDs, folderIDs []string) ([]entry, error) {
	var out []entry
	used := map[string]bool{}
	add := func(f *File, p string) {
		orig := p
		for n := 1; used[strings.ToLower(p)]; n++ {
			p = path.Join(path.Dir(orig), numberedName(path.Base(orig), n))
		}
		used[strings.ToLower(p)] = true
		out = append(out, entry{f, strings.TrimPrefix(p, "./")})
	}
	files, err := s.filesByIDs(ctx, userID, fileIDs)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	for _, f := range files {
		add(f, f.Name)
	}
	if len(folderIDs) == 0 {
		return out, nil
	}
	idx, err := s.loadFolders(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, root := range folderIDs {
		if idx.byID[root] == nil {
			continue
		}
		ids := idx.descendants(root)
		rel := map[string]string{}
		for _, id := range ids {
			var parts []string
			for cur := id; ; cur = idx.byID[cur].ParentID {
				parts = append([]string{idx.byID[cur].Name}, parts...)
				if cur == root {
					break
				}
			}
			rel[id] = path.Join(parts...)
		}
		files, err := s.filesInFolders(ctx, userID, ids)
		if err != nil {
			return nil, err
		}
		sort.Slice(files, func(i, j int) bool {
			a, b := rel[files[i].FolderID]+"/"+files[i].Name, rel[files[j].FolderID]+"/"+files[j].Name
			return strings.ToLower(a) < strings.ToLower(b)
		})
		for _, f := range files {
			add(f, path.Join(rel[f.FolderID], f.Name))
		}
	}
	return out, nil
}

// ---------- quota ----------

func (s *Server) userQuota(u *User) int64 {
	if u.QuotaBytes >= 0 {
		return u.QuotaBytes
	}
	return s.conf().DefaultUserQuota
}

func (s *Server) usedBytes(ctx context.Context, userID string) (used, pending int64, err error) {
	if err = s.db.QueryRow(ctx, `SELECT COALESCE(SUM(size), 0) FROM files WHERE user_id = ?`, userID).Scan(&used); err != nil {
		return
	}
	err = s.db.QueryRow(ctx, `SELECT COALESCE(SUM(size), 0) FROM uploads WHERE user_id = ? AND file_id = ''`, userID).Scan(&pending)
	return
}

// checkQuota rejects an incoming upload of n bytes that would exceed any configured limit.
// In-progress uploads count as reserved so parallel uploads cannot overshoot.
func (s *Server) checkQuota(ctx context.Context, u *User, n int64) error {
	if s.conf().MaxUploadBytes > 0 && n > s.conf().MaxUploadBytes {
		return errf(413, "file_too_large", "This file is larger than the server's upload limit of "+humanSize(s.conf().MaxUploadBytes)+".")
	}
	if q := s.userQuota(u); q > 0 {
		used, pending, err := s.usedBytes(ctx, u.ID)
		if err != nil {
			return err
		}
		if used+pending+n > q {
			return errf(413, "quota_exceeded", "Not enough storage space: "+humanSize(used)+" of "+humanSize(q)+" used. Delete some files or ask your administrator for more space.")
		}
	}
	room, err := s.serverRoom(ctx)
	if err != nil {
		return err
	}
	if n > room {
		return errf(507, "server_full", "The server is out of storage space. Please contact the administrator.")
	}
	return nil
}

// serverRoom is how many more bytes the server accepts under FERRY_GLOBAL_QUOTA and FERRY_MIN_FREE_SPACE,
// counting bytes still owed to in-progress uploads.
func (s *Server) serverRoom(ctx context.Context) (int64, error) {
	room := int64(math.MaxInt64)
	if s.conf().GlobalQuota <= 0 && s.conf().MinFreeSpace <= 0 {
		return room, nil
	}
	var used, pending, owed int64
	if err := s.db.QueryRow(ctx, `SELECT COALESCE(SUM(size), 0) FROM files`).Scan(&used); err != nil {
		return 0, err
	}
	if err := s.db.QueryRow(ctx, `SELECT COALESCE(SUM(size), 0), COALESCE(SUM(size - received), 0) FROM uploads WHERE file_id = ''`).Scan(&pending, &owed); err != nil {
		return 0, err
	}
	if s.conf().GlobalQuota > 0 {
		room = min(room, s.conf().GlobalQuota-used-pending)
	}
	if s.conf().MinFreeSpace > 0 {
		if _, free, ok := s.store.Usage(); ok {
			room = min(room, int64(free)-owed-s.conf().MinFreeSpace)
		}
	}
	return max(room, 0), nil
}

// quotaRoom is how many more bytes u can upload right now under every limit (for streams of unknown size).
func (s *Server) quotaRoom(ctx context.Context, u *User) (int64, error) {
	room, err := s.serverRoom(ctx)
	if err != nil {
		return 0, err
	}
	if q := s.userQuota(u); q > 0 {
		used, pending, err := s.usedBytes(ctx, u.ID)
		if err != nil {
			return 0, err
		}
		room = min(room, max(q-used-pending, 0))
	}
	return room, nil
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	used, pending, err := s.usedBytes(r.Context(), u.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var count int
	s.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM files WHERE user_id = ? AND transfer_id = ''`, u.ID).Scan(&count)
	writeJSON(w, 200, map[string]any{"usedBytes": used, "pendingBytes": pending, "quotaBytes": s.userQuota(u), "fileCount": count,
		"maxUploadBytes": s.conf().MaxUploadBytes})
}

// ---------- listing ----------

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	q := r.URL.Query()
	folderID := q.Get("folder")
	order := "LOWER(name) ASC"
	desc := q.Get("order") == "desc"
	switch q.Get("sort") {
	case "size":
		order = "size"
	case "date":
		order = "updated_at"
	default:
		order = "LOWER(name)"
	}
	if desc {
		order += " DESC"
	} else {
		order += " ASC"
	}
	idx, err := s.loadFolders(ctx, u.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	resp := map[string]any{}
	var files []*File
	folders := []*Folder{}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		files, err = scanFiles(s.db.Query(ctx, `SELECT `+fileCols+` FROM files WHERE user_id = ? AND transfer_id = '' AND LOWER(name) LIKE ? ESCAPE '\' ORDER BY `+order+` LIMIT 500`, u.ID, likeEscape(search)))
		ls := strings.ToLower(search)
		for _, f := range idx.byID {
			if strings.Contains(strings.ToLower(f.Name), ls) {
				folders = append(folders, f)
			}
		}
		resp["search"] = search
	} else {
		if folderID != "" && idx.byID[folderID] == nil {
			s.writeErr(w, r, errf(404, "folder_not_found", "That folder no longer exists."))
			return
		}
		files, err = scanFiles(s.db.Query(ctx, `SELECT `+fileCols+` FROM files WHERE user_id = ? AND transfer_id = '' AND folder_id = ? ORDER BY `+order, u.ID, folderID))
		for _, id := range idx.children[folderID] {
			folders = append(folders, idx.byID[id])
		}
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sort.Slice(folders, func(i, j int) bool { return strings.ToLower(folders[i].Name) < strings.ToLower(folders[j].Name) })
	// Shared-state badges for the file manager.
	shared := map[string]bool{}
	if rows, err := s.db.Query(ctx, `SELECT si.item_id FROM share_items si JOIN shares sh ON sh.id = si.share_id WHERE sh.user_id = ? AND sh.revoked = 0 AND (sh.expires_at = 0 OR sh.expires_at > ?)`, u.ID, nowMs()); err == nil {
		for rows.Next() {
			var id string
			rows.Scan(&id)
			shared[id] = true
		}
		rows.Close()
	}
	sharedIDs := []string{}
	for id := range shared {
		sharedIDs = append(sharedIDs, id)
	}
	crumbs := idx.path(folderID)
	if crumbs == nil {
		crumbs = []*Folder{}
	}
	resp["folderId"] = folderID
	resp["breadcrumbs"] = crumbs
	resp["folders"] = folders
	resp["files"] = files
	resp["shared"] = sharedIDs
	writeJSON(w, 200, resp)
}

func (s *Server) handleCheckNames(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FolderID string   `json:"folderId"`
		Names    []string `json:"names"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u := userOf(r)
	existing := map[string]bool{}
	files, err := scanFiles(s.db.Query(r.Context(), `SELECT `+fileCols+` FROM files WHERE user_id = ? AND folder_id = ? AND transfer_id = ''`, u.ID, req.FolderID))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	for _, f := range files {
		existing[f.Name] = true
	}
	conflicts := []string{}
	for _, n := range req.Names {
		if existing[cleanName(n)] {
			conflicts = append(conflicts, n)
		}
	}
	writeJSON(w, 200, map[string]any{"conflicts": conflicts})
}

func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	f, err := s.getFile(r.Context(), userOf(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, f)
}

// ---------- folder & file mutations ----------

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		ParentID string `json:"parentId"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u := userOf(r)
	if req.ParentID != "" {
		if _, err := s.getFolder(r.Context(), u.ID, req.ParentID); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	f := &Folder{ID: newID(), Name: cleanName(req.Name), ParentID: req.ParentID, CreatedAt: nowMs()}
	f.UpdatedAt = f.CreatedAt
	_, err := s.db.Exec(r.Context(), `INSERT INTO folders (id, user_id, parent_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		f.ID, u.ID, f.ParentID, f.Name, f.CreatedAt, f.UpdatedAt)
	if db.IsUnique(err) {
		err = errf(409, "name_taken", "A folder named \""+f.Name+"\" already exists here.")
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "folder_created", f.ID, f.Name)
	writeJSON(w, 201, f)
}

func (s *Server) handleUpdateFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     *string `json:"name"`
		ParentID *string `json:"parentId"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u := userOf(r)
	f, err := s.getFolder(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.Name != nil {
		f.Name = cleanName(*req.Name)
	}
	if req.ParentID != nil && *req.ParentID != f.ParentID {
		if err := s.checkMoveTarget(r.Context(), u.ID, []string{f.ID}, *req.ParentID); err != nil {
			s.writeErr(w, r, err)
			return
		}
		f.ParentID = *req.ParentID
	}
	f.UpdatedAt = nowMs()
	_, err = s.db.Exec(r.Context(), `UPDATE folders SET name = ?, parent_id = ?, updated_at = ? WHERE id = ? AND user_id = ?`, f.Name, f.ParentID, f.UpdatedAt, f.ID, u.ID)
	if db.IsUnique(err) {
		err = errf(409, "name_taken", "A folder named \""+f.Name+"\" already exists there.")
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "folder_updated", f.ID, f.Name)
	writeJSON(w, 200, f)
}

// checkMoveTarget validates target exists and is not one of the moved folders or inside them.
func (s *Server) checkMoveTarget(ctx context.Context, userID string, movedFolders []string, target string) error {
	if target == "" {
		return nil
	}
	idx, err := s.loadFolders(ctx, userID)
	if err != nil {
		return err
	}
	if idx.byID[target] == nil {
		return errf(404, "folder_not_found", "The destination folder no longer exists.")
	}
	moved := map[string]bool{}
	for _, id := range movedFolders {
		moved[id] = true
	}
	for _, f := range idx.path(target) {
		if moved[f.ID] {
			return errf(400, "invalid_move", "A folder can't be moved into itself.")
		}
	}
	return nil
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if err := s.deleteItems(r.Context(), u.ID, nil, []string{r.PathValue("id")}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleUpdateFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     *string `json:"name"`
		FolderID *string `json:"folderId"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u := userOf(r)
	f, err := s.getFile(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.Name != nil {
		f.Name = cleanName(*req.Name)
	}
	if req.FolderID != nil {
		if err := s.checkMoveTarget(r.Context(), u.ID, nil, *req.FolderID); err != nil {
			s.writeErr(w, r, err)
			return
		}
		f.FolderID = *req.FolderID
	}
	f.UpdatedAt = nowMs()
	_, err = s.db.Exec(r.Context(), `UPDATE files SET name = ?, folder_id = ?, updated_at = ? WHERE id = ? AND user_id = ?`, f.Name, f.FolderID, f.UpdatedAt, f.ID, u.ID)
	if db.IsUnique(err) {
		err = errf(409, "name_taken", "A file named \""+f.Name+"\" already exists there.")
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "file_updated", f.ID, f.Name)
	writeJSON(w, 200, f)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	f, err := s.getFile(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.deleteItems(r.Context(), u.ID, []string{f.ID}, nil); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.audit(r.Context(), r, u.ID, "file_deleted", f.ID, f.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// deleteItems removes files and folders (recursively): DB rows first, then blobs.
// A blob that cannot be removed (e.g. open on Windows) is swept later by the orphan cleanup.
func (s *Server) deleteItems(ctx context.Context, userID string, fileIDs, folderIDs []string) error {
	var allFolders []string
	if len(folderIDs) > 0 {
		idx, err := s.loadFolders(ctx, userID)
		if err != nil {
			return err
		}
		for _, id := range folderIDs {
			if idx.byID[id] != nil {
				allFolders = append(allFolders, idx.descendants(id)...)
			}
		}
		inFolders, err := s.filesInFolders(ctx, userID, allFolders)
		if err != nil {
			return err
		}
		for _, f := range inFolders {
			fileIDs = append(fileIDs, f.ID)
		}
	}
	files, err := s.filesByIDs(ctx, userID, fileIDs)
	if err != nil {
		return err
	}
	err = s.db.InTx(ctx, func(tx *db.Tx) error {
		for _, f := range files {
			if _, err := tx.Exec(ctx, `DELETE FROM files WHERE id = ? AND user_id = ?`, f.ID, userID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM share_items WHERE item_id = ?`, f.ID); err != nil {
				return err
			}
		}
		for _, id := range allFolders {
			if _, err := tx.Exec(ctx, `DELETE FROM folders WHERE id = ? AND user_id = ?`, id, userID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM share_items WHERE item_id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, f := range files {
		s.streams.cancel(f.ID)
		if err := s.store.Remove("blobs/" + f.Blob); err != nil {
			s.log.Warn("blob removal deferred to cleanup", "blob", f.Blob, "err", err)
		}
	}
	return nil
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action         string   `json:"action"`
		FileIDs        []string `json:"fileIds"`
		FolderIDs      []string `json:"folderIds"`
		TargetFolderID string   `json:"targetFolderId"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	ctx := r.Context()
	u := userOf(r)
	switch req.Action {
	case "delete":
		if err := s.deleteItems(ctx, u.ID, req.FileIDs, req.FolderIDs); err != nil {
			s.writeErr(w, r, err)
			return
		}
		s.audit(ctx, r, u.ID, "items_deleted", "", itemCount(req.FileIDs, req.FolderIDs))
		writeJSON(w, 200, map[string]any{"ok": true})
	case "move":
		if err := s.checkMoveTarget(ctx, u.ID, req.FolderIDs, req.TargetFolderID); err != nil {
			s.writeErr(w, r, err)
			return
		}
		var failed []string
		now := nowMs()
		for _, id := range req.FileIDs {
			if _, err := s.db.Exec(ctx, `UPDATE files SET folder_id = ?, updated_at = ? WHERE id = ? AND user_id = ?`, req.TargetFolderID, now, id, u.ID); err != nil {
				if !db.IsUnique(err) {
					s.writeErr(w, r, err)
					return
				}
				failed = append(failed, id)
			}
		}
		for _, id := range req.FolderIDs {
			if _, err := s.db.Exec(ctx, `UPDATE folders SET parent_id = ?, updated_at = ? WHERE id = ? AND user_id = ?`, req.TargetFolderID, now, id, u.ID); err != nil {
				if !db.IsUnique(err) {
					s.writeErr(w, r, err)
					return
				}
				failed = append(failed, id)
			}
		}
		s.audit(ctx, r, u.ID, "items_moved", req.TargetFolderID, itemCount(req.FileIDs, req.FolderIDs))
		resp := map[string]any{"ok": len(failed) == 0, "failed": failed}
		if len(failed) > 0 {
			resp["message"] = "Some items were not moved because an item with the same name already exists in the destination."
		}
		writeJSON(w, 200, resp)
	default:
		s.writeErr(w, r, errf(400, "bad_action", "Unknown action."))
	}
}

// ---------- content delivery ----------

var previewable = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true, "image/avif": true,
	"video/mp4": true, "video/webm": true, "video/ogg": true, "audio/mpeg": true, "audio/wav": true, "audio/wave": true,
	"audio/ogg": true, "audio/mp4": true, "audio/aac": true, "audio/flac": true, "application/pdf": true,
	"text/plain": true, "text/csv": true, "text/markdown": true,
}

func baseMime(m string) string { return strings.TrimSpace(strings.SplitN(m, ";", 2)[0]) }

func isPreviewable(m string) bool { return previewable[baseMime(m)] }

// dangerousMime types can execute script if rendered by a browser; never trust them from extensions.
func dangerousMime(m string) bool {
	m = baseMime(m)
	return strings.Contains(m, "html") || strings.Contains(m, "svg") || strings.Contains(m, "javascript") ||
		strings.Contains(m, "xml") || m == "application/x-shockwave-flash"
}

// detectMime sniffs content. Extension-derived types refine generic results (e.g. .docx is a zip)
// but can never introduce a script-capable type.
func detectMime(head []byte, name string) string {
	sniffed := http.DetectContentType(head)
	b := baseMime(sniffed)
	if dangerousMime(sniffed) {
		return "application/octet-stream"
	}
	if b == "application/octet-stream" || b == "application/zip" || b == "text/plain" {
		if ext := mime.TypeByExtension(strings.ToLower(path.Ext(name))); ext != "" && !dangerousMime(ext) {
			eb := baseMime(ext)
			// Only accept a text/* refinement if content sniffed as text, and binary refinements if it did not.
			if strings.HasPrefix(eb, "text/") == (b == "text/plain") {
				return eb
			}
		}
	}
	return b
}

type ctxReadSeeker struct {
	ctx context.Context
	rs  io.ReadSeeker
}

func (c ctxReadSeeker) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.rs.Read(p)
}
func (c ctxReadSeeker) Seek(off int64, whence int) (int64, error) { return c.rs.Seek(off, whence) }

func contentDisposition(kind, name string) string {
	if v := mime.FormatMediaType(kind, map[string]string{"filename": name}); v != "" {
		return v
	}
	return kind
}

// serveFile streams a blob with Range support. streamKeys identify cancellation groups
// (share ID, file ID, user ID) so revocation or deletion stops the stream immediately.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, f *File, inline bool, streamKeys ...string) {
	rc, _, _, err := s.store.OpenRead("blobs/" + f.Blob)
	if err != nil {
		s.storageErr(w, r, f, err)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.streams.add(append(streamKeys, f.ID), cancel)()
	// Closing the file is what stops a revoked download: it also interrupts a zero-copy sendfile.
	go func() { <-ctx.Done(); rc.Close() }()
	s.metrics.downloadsActive.Add(1)
	defer s.metrics.downloadsActive.Add(-1)

	h := w.Header()
	if inline && isPreviewable(f.Mime) {
		ct := baseMime(f.Mime)
		if strings.HasPrefix(ct, "text/") {
			ct = "text/plain; charset=utf-8"
		}
		h.Set("Content-Type", ct)
		h.Set("Content-Disposition", contentDisposition("inline", f.Name))
		h.Set("X-Frame-Options", "SAMEORIGIN")
		if ct == "application/pdf" {
			h.Set("Content-Security-Policy", "frame-ancestors 'self'")
		} else {
			h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'")
		}
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", contentDisposition("attachment", f.Name))
	}
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+f.SHA256+`"`)
	h.Set("X-Content-SHA256", f.SHA256)
	http.ServeContent(w, r.WithContext(ctx), "", time.UnixMilli(f.UpdatedAt), rc)
}

func (s *Server) storageErr(w http.ResponseWriter, r *http.Request, f *File, err error) {
	s.writeErr(w, r, s.storageAPIErr(f, err))
}

// storageAPIErr turns a storage failure into a user-facing error (outage vs. missing data).
func (s *Server) storageAPIErr(f *File, err error) error {
	switch {
	case errors.Is(err, storage.ErrUnavailable):
		s.log.Error("storage unavailable", "file", f.ID, "err", err)
		return errf(503, "storage_unavailable", "The server's storage is temporarily unavailable. Your file is not lost — please try again later.")
	case errors.Is(err, storage.ErrNotFound):
		s.log.Error("blob missing from storage", "file", f.ID, "blob", f.Blob)
		return errf(404, "file_missing", "\""+f.Name+"\" is missing on the server, so the archive can't be created. The administrator can check the storage logs.")
	}
	return err
}

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	f, err := s.getFile(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.serveFile(w, r, f, r.URL.Query().Get("inline") == "1", "user:"+u.ID)
}

func splitIDs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// zipTicket is a selection too large for a URL, stored briefly for the GET that downloads it.
type zipTicket struct {
	userID, name   string
	files, folders []string
	expires        time.Time
}

// handleCreateZip stores a selection and returns a short-lived download URL for it.
// Tickets are in memory (5 min); with several replicas they would need the database.
func (s *Server) handleCreateZip(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FileIDs   []string `json:"fileIds"`
		FolderIDs []string `json:"folderIds"`
		Name      string   `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	tok := newToken(16)
	s.zips.Store(tok, &zipTicket{userID: userOf(r).ID, name: req.Name, files: req.FileIDs, folders: req.FolderIDs, expires: time.Now().Add(5 * time.Minute)})
	writeJSON(w, 200, map[string]string{"url": "/api/v1/files/zip?ticket=" + tok})
}

func (s *Server) sweepZipTickets() {
	s.zips.Range(func(k, v any) bool {
		if time.Now().After(v.(*zipTicket).expires) {
			s.zips.Delete(k)
		}
		return true
	})
}

func (s *Server) handleOwnerZip(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	q := r.URL.Query()
	files, folders, rawName := splitIDs(q.Get("files")), splitIDs(q.Get("folders")), q.Get("name")
	if t := q.Get("ticket"); t != "" {
		v, ok := s.zips.Load(t)
		zt, _ := v.(*zipTicket)
		if !ok || zt.userID != u.ID || time.Now().After(zt.expires) {
			s.writeErr(w, r, errf(410, "ticket_expired", "This download link has expired. Please start the download again."))
			return
		}
		files, folders, rawName = zt.files, zt.folders, zt.name
	}
	entries, err := s.collectEntries(r.Context(), u.ID, files, folders)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if len(entries) == 0 {
		s.writeErr(w, r, errf(404, "empty", "There are no files to download."))
		return
	}
	name := cleanName(rawName)
	if rawName == "" {
		name = "ferry-files"
	}
	if err := s.streamZip(w, r, name+".zip", entries, "user:"+u.ID); err != nil {
		s.writeErr(w, r, err)
	}
}

// checkBlobs verifies every entry's data is readable.
func (s *Server) checkBlobs(entries []entry) error {
	for _, e := range entries {
		rc, _, _, err := s.store.OpenRead("blobs/" + e.File.Blob)
		if err != nil {
			return s.storageAPIErr(e.File, err)
		}
		rc.Close()
	}
	return nil
}

// streamZip writes an uncompressed (store) ZIP directly to the response; memory use is constant.
// It returns an error (and writes nothing) if any file is unreadable, so recipients never get a
// silently incomplete archive. A failure after streaming started aborts without a central directory.
func (s *Server) streamZip(w http.ResponseWriter, r *http.Request, name string, entries []entry, streamKeys ...string) error {
	if err := s.checkBlobs(entries); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	keys := append([]string{}, streamKeys...)
	for _, e := range entries {
		keys = append(keys, e.File.ID)
	}
	defer s.streams.add(keys, cancel)()
	s.metrics.downloadsActive.Add(1)
	defer s.metrics.downloadsActive.Add(-1)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", name))
	w.Header().Set("Cache-Control", "private, no-store")
	zw := zip.NewWriter(w)
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil
		}
		rc, _, _, err := s.store.OpenRead("blobs/" + e.File.Blob)
		if err != nil {
			s.log.Error("zip aborted: file became unreadable", "file", e.File.ID, "err", err)
			return nil // headers are sent; dropping the central directory marks the archive as broken
		}
		hdr := &zip.FileHeader{Name: e.Path, Method: zip.Store, Modified: time.UnixMilli(e.File.UpdatedAt)}
		hdr.SetMode(0o644)
		fw, err := zw.CreateHeader(hdr)
		if err == nil {
			_, err = io.Copy(fw, ctxReadSeeker{ctx, rc})
		}
		rc.Close()
		if err != nil {
			s.log.Warn("zip stream aborted", "err", err)
			return nil // client gone or revoked; do not write the central directory
		}
	}
	zw.Close()
	return nil
}

func itemCount(files, folders []string) string {
	return strconv.Itoa(len(files)) + " file(s), " + strconv.Itoa(len(folders)) + " folder(s)"
}
