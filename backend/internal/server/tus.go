package server

// Resumable uploads via the tus 1.0 protocol (core + creation + termination).
// Upload progress and the SHA-256 state are persisted every 8 MiB (after fsync), so after a
// crash, disconnect or restart the client resumes from the last saved offset.

import (
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ferry/internal/db"
)

const tusVersion = "1.0.0"

type upload struct {
	ID, UserID, ShareID, FolderID, TransferID, Name string
	Size, Received                                  int64
	Conflict, HashState, ClientSHA                  string
	Uploader, UploaderKey, FileID                   string
	CreatedAt, UpdatedAt                            int64
	Concat                                          string // "partial": one part of a parallel upload (tus concatenation)
	CRC                                             int64  // running CRC-32, stored with the file for resumable ZIPs
}

const uploadCols = `id, user_id, share_id, folder_id, transfer_id, name, size, received, conflict, hash_state, client_sha256, uploader, uploader_key, file_id, created_at, updated_at, concat, crc32`
const uploadVals = `?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`

func scanUpload(row interface{ Scan(...any) error }) (*upload, error) {
	u := &upload{}
	err := row.Scan(&u.ID, &u.UserID, &u.ShareID, &u.FolderID, &u.TransferID, &u.Name, &u.Size, &u.Received, &u.Conflict,
		&u.HashState, &u.ClientSHA, &u.Uploader, &u.UploaderKey, &u.FileID, &u.CreatedAt, &u.UpdatedAt, &u.Concat, &u.CRC)
	return u, err
}

// busy prevents two PATCH requests from writing the same upload concurrently
// (a client may retry while the server has not yet noticed the old connection dropped).
var busy sync.Map

func tusHeaders(w http.ResponseWriter) {
	w.Header().Set("Tus-Resumable", tusVersion)
	w.Header().Set("Cache-Control", "no-store")
}

func (s *Server) tusOptions(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w)
	w.Header().Set("Tus-Version", tusVersion)
	w.Header().Set("Tus-Extension", "creation,termination,concatenation")
	if s.conf().MaxUploadBytes > 0 {
		w.Header().Set("Tus-Max-Size", strconv.FormatInt(s.conf().MaxUploadBytes, 10))
	}
	w.WriteHeader(204)
}

func parseMetadata(h string) map[string]string {
	m := map[string]string{}
	for _, kv := range strings.Split(h, ",") {
		parts := strings.Fields(kv)
		if len(parts) == 0 {
			continue
		}
		v := ""
		if len(parts) > 1 {
			if b, err := base64.StdEncoding.DecodeString(parts[1]); err == nil {
				v = string(b)
			}
		}
		m[parts[0]] = v
	}
	return m
}

var hexSHA = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ---------- principals ----------

func (s *Server) tusCreateUser(w http.ResponseWriter, r *http.Request) {
	s.tusCreate(w, r, userOf(r), nil, "/api/v1/uploads/")
}

func (s *Server) userUpload(r *http.Request) (*upload, error) {
	up, err := scanUpload(s.db.QueryRow(r.Context(), `SELECT `+uploadCols+` FROM uploads WHERE id = ? AND user_id = ? AND share_id = ''`, r.PathValue("id"), userOf(r).ID))
	if db.IsNoRows(err) {
		return nil, errf(404, "upload_not_found", "This upload has expired. Please start it again.")
	}
	return up, err
}

func (s *Server) tusHeadUser(w http.ResponseWriter, r *http.Request) {
	up, err := s.userUpload(r)
	s.tusHead(w, r, up, err)
}

func (s *Server) tusPatchUser(w http.ResponseWriter, r *http.Request) {
	up, err := s.userUpload(r)
	if err != nil {
		tusHeaders(w)
		s.writeErr(w, r, err)
		return
	}
	s.tusPatch(w, r, up, userOf(r), nil)
}

func (s *Server) tusDeleteUser(w http.ResponseWriter, r *http.Request) {
	up, err := s.userUpload(r)
	s.tusDelete(w, r, up, err)
}

func (s *Server) handleListUploads(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT `+uploadCols+` FROM uploads WHERE user_id = ? AND share_id = '' AND file_id = '' ORDER BY updated_at DESC`, userOf(r).ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		up, err := scanUpload(rows)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		out = append(out, map[string]any{"id": up.ID, "name": up.Name, "size": up.Size, "received": up.Received,
			"folderId": up.FolderID, "transferId": up.TransferID, "updatedAt": up.UpdatedAt})
	}
	writeJSON(w, 200, map[string]any{"uploads": out})
}

// ---------- protocol ----------

// tusCreate handles POST. owner receives the file; share is set for anonymous upload links.
func (s *Server) tusCreate(w http.ResponseWriter, r *http.Request, owner *User, share *Share, locationPrefix string) {
	tusHeaders(w)
	ctx := r.Context()
	if r.Header.Get("Tus-Resumable") != tusVersion {
		w.Header().Set("Tus-Version", tusVersion)
		s.writeErr(w, r, errf(412, "tus_version", "Unsupported upload protocol version."))
		return
	}
	// Parallel uploads (tus concatenation): the client uploads parts side by side, then creates the
	// final upload, which joins them. Only for signed-in users; upload links use one connection.
	concat := r.Header.Get("Upload-Concat")
	var parts []*upload
	var size int64
	var err error
	switch {
	case concat != "" && share != nil:
		s.writeErr(w, r, errf(400, "concat_unsupported", "Parallel uploads aren't available for upload links."))
		return
	case strings.HasPrefix(concat, "final;"):
		if parts, size, err = s.concatParts(r, owner, strings.TrimPrefix(concat, "final;")); err != nil {
			s.writeErr(w, r, err)
			return
		}
	case concat != "" && concat != "partial":
		s.writeErr(w, r, errf(400, "bad_concat", "Unsupported Upload-Concat value."))
		return
	default:
		size, err = strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
		if err != nil || size < 0 {
			s.writeErr(w, r, errf(400, "bad_length", "Upload-Length header is required."))
			return
		}
	}
	if concat == "partial" {
		s.tusCreatePart(w, r, owner, size, locationPrefix)
		return
	}
	meta := parseMetadata(r.Header.Get("Upload-Metadata"))
	name := cleanName(meta["filename"])
	if meta["filename"] == "" {
		s.writeErr(w, r, errf(400, "missing_name", "The file name is missing."))
		return
	}
	up := &upload{ID: newID(), UserID: owner.ID, Name: name, Size: size, Conflict: "keep_both", CreatedAt: nowMs()}
	up.UpdatedAt = up.CreatedAt
	if sha := strings.ToLower(meta["sha256"]); sha != "" {
		if !hexSHA.MatchString(sha) {
			s.writeErr(w, r, errf(400, "bad_checksum", "Invalid sha256 metadata."))
			return
		}
		up.ClientSHA = sha
	}
	if share != nil {
		up.ShareID = share.ID
		up.FolderID = share.FolderID
		up.Uploader = strings.TrimSpace(meta["uploader"])
		if len(up.Uploader) > 100 {
			up.Uploader = up.Uploader[:100]
		}
		up.UploaderKey = s.uploaderKey(w, r, share)
		if err := s.checkLinkLimits(ctx, share, name, size); err != nil {
			s.writeErr(w, r, err)
			return
		}
	} else {
		switch c := meta["conflict"]; c {
		case "", "keep_both", "rename":
		case "replace", "skip":
			up.Conflict = c
		default:
			s.writeErr(w, r, errf(400, "bad_conflict", "Unknown conflict policy."))
			return
		}
		up.FolderID = meta["folderId"]
		if up.FolderID != "" {
			if _, err := s.getFolder(ctx, owner.ID, up.FolderID); err != nil {
				s.writeErr(w, r, err)
				return
			}
		}
		if tid := meta["transferId"]; tid != "" {
			var status string
			err := s.db.QueryRow(ctx, `SELECT status FROM transfers WHERE id = ? AND user_id = ?`, tid, owner.ID).Scan(&status)
			if err != nil || isFinal(status) {
				s.writeErr(w, r, errf(409, "transfer_closed", "This transfer is no longer accepting files."))
				return
			}
			up.TransferID = tid
			up.FolderID = ""
		}
	}
	if parts == nil { // the parts already reserved their space
		if err := s.checkQuota(ctx, owner, size); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	wc, err := s.store.OpenAppend("uploads/"+up.ID, 0)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	wc.Close()
	_, err = s.db.Exec(ctx, `INSERT INTO uploads (`+uploadCols+`) VALUES (`+uploadVals+`)`,
		up.ID, up.UserID, up.ShareID, up.FolderID, up.TransferID, up.Name, up.Size, 0, up.Conflict, "", up.ClientSHA,
		up.Uploader, up.UploaderKey, "", up.CreatedAt, up.UpdatedAt, up.Concat, 0)
	if err != nil {
		s.store.Remove("uploads/" + up.ID)
		s.writeErr(w, r, err)
		return
	}
	w.Header().Set("Location", locationPrefix+up.ID)
	if parts != nil {
		s.joinParts(w, r, up, owner, parts)
		return
	}
	w.Header().Set("Upload-Offset", "0")
	if size == 0 { // empty files complete immediately
		s.tusFinish(w, r, up, owner, share, sha256.New())
		if w.Header().Get("Ferry-Error") != "" {
			return
		}
		w.WriteHeader(201)
		return
	}
	w.WriteHeader(201)
}

// tusCreatePart creates one part of a parallel upload. It carries no name or destination: those come
// with the final upload that joins the parts.
func (s *Server) tusCreatePart(w http.ResponseWriter, r *http.Request, owner *User, size int64, locationPrefix string) {
	ctx := r.Context()
	if err := s.checkQuota(ctx, owner, size); err != nil {
		s.writeErr(w, r, err)
		return
	}
	up := &upload{ID: newID(), UserID: owner.ID, Name: "part", Size: size, Conflict: "keep_both", Concat: "partial", CreatedAt: nowMs()}
	up.UpdatedAt = up.CreatedAt
	wc, err := s.store.OpenAppend("uploads/"+up.ID, 0)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	wc.Close()
	if _, err := s.db.Exec(ctx, `INSERT INTO uploads (`+uploadCols+`) VALUES (`+uploadVals+`)`,
		up.ID, up.UserID, "", "", "", up.Name, up.Size, 0, up.Conflict, "", "", "", "", "", up.CreatedAt, up.UpdatedAt, up.Concat, 0); err != nil {
		s.store.Remove("uploads/" + up.ID)
		s.writeErr(w, r, err)
		return
	}
	w.Header().Set("Location", locationPrefix+up.ID)
	w.Header().Set("Upload-Offset", "0")
	w.WriteHeader(201)
}

// concatParts resolves "final;<url> <url> …" to the caller's complete parts, in order.
func (s *Server) concatParts(r *http.Request, owner *User, list string) ([]*upload, int64, error) {
	urls := strings.Fields(list)
	if len(urls) == 0 || len(urls) > 64 {
		return nil, 0, errf(400, "bad_concat", "A parallel upload needs between 1 and 64 parts.")
	}
	var parts []*upload
	var total int64
	for _, u := range urls {
		id := path.Base(strings.SplitN(u, "?", 2)[0])
		p, err := scanUpload(s.db.QueryRow(r.Context(), `SELECT `+uploadCols+` FROM uploads WHERE id = ? AND user_id = ? AND share_id = '' AND concat = 'partial'`, id, owner.ID))
		if err != nil {
			return nil, 0, errf(404, "upload_not_found", "Part of this upload has expired. Please start it again.")
		}
		if p.Received != p.Size {
			return nil, 0, errf(409, "part_incomplete", "Part of this upload isn't finished yet.")
		}
		parts = append(parts, p)
		total += p.Size
	}
	return parts, total, nil
}

// joinParts writes the parts into the final upload (hashing as it goes), finishes it and removes the
// parts. On failure the parts are kept, so the client can simply retry creating the final upload.
func (s *Server) joinParts(w http.ResponseWriter, r *http.Request, up *upload, owner *User, parts []*upload) {
	ctx := context.WithoutCancel(r.Context()) // finish even if the client stops waiting
	fail := func(err error) {
		s.db.Exec(ctx, `DELETE FROM uploads WHERE id = ?`, up.ID)
		s.store.Remove("uploads/" + up.ID)
		if err != nil {
			s.writeErr(w, r, err)
		}
	}
	for _, p := range parts {
		if _, loaded := busy.LoadOrStore(p.ID, true); loaded {
			fail(errf(423, "upload_busy", "This upload is already being finished."))
			return
		}
		defer busy.Delete(p.ID)
	}
	wc, err := s.store.OpenAppend("uploads/"+up.ID, 0)
	if err != nil {
		fail(err)
		return
	}
	h, crc := sha256.New(), crc32.NewIEEE()
	out := io.MultiWriter(wc, h, crc)
	buf := make([]byte, 1<<20)
	for _, p := range parts {
		rc, _, _, err := s.store.OpenRead("uploads/" + p.ID)
		if err == nil {
			var n int64
			n, err = io.CopyBuffer(out, rc, buf)
			rc.Close()
			if err == nil && n != p.Size {
				err = fmt.Errorf("part %s has %d bytes, expected %d", p.ID, n, p.Size)
			}
		}
		if err != nil {
			wc.Close()
			fail(err)
			return
		}
	}
	if sy, ok := wc.(interface{ Sync() error }); ok {
		err = sy.Sync()
	}
	wc.Close()
	if err != nil {
		fail(err)
		return
	}
	up.Received, up.CRC = up.Size, int64(crc.Sum32())
	s.db.Exec(ctx, `UPDATE uploads SET received = size, crc32 = ?, updated_at = ? WHERE id = ?`, up.CRC, nowMs(), up.ID)
	s.tusFinish(w, r.WithContext(ctx), up, owner, nil, h)
	if w.Header().Get("Ferry-Error") != "" {
		fail(nil)
		return
	}
	for _, p := range parts {
		s.db.Exec(ctx, `DELETE FROM uploads WHERE id = ?`, p.ID)
		s.store.Remove("uploads/" + p.ID)
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(up.Size, 10))
	w.WriteHeader(201)
}

func (s *Server) tusHead(w http.ResponseWriter, r *http.Request, up *upload, err error) {
	tusHeaders(w)
	if err != nil {
		var ae *apiErr
		if errors.As(err, &ae) {
			w.WriteHeader(ae.Status)
		} else {
			w.WriteHeader(500)
		}
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(up.Received, 10))
	w.Header().Set("Upload-Length", strconv.FormatInt(up.Size, 10))
	if up.Concat == "partial" {
		w.Header().Set("Upload-Concat", "partial")
	}
	if up.FileID != "" {
		w.Header().Set("Ferry-File-Id", up.FileID)
	}
	w.WriteHeader(200)
}

func (s *Server) tusDelete(w http.ResponseWriter, r *http.Request, up *upload, err error) {
	tusHeaders(w)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if up.FileID == "" {
		s.db.Exec(r.Context(), `DELETE FROM uploads WHERE id = ?`, up.ID)
		s.store.Remove("uploads/" + up.ID)
	}
	w.WriteHeader(204)
}

const progressEvery = 32 << 20 // progress (fsync + DB write) is saved at least this often, and on every disconnect

func (s *Server) tusPatch(w http.ResponseWriter, r *http.Request, up *upload, owner *User, share *Share) {
	tusHeaders(w)
	ctx := r.Context()
	if r.Header.Get("Content-Type") != "application/offset+octet-stream" {
		s.writeErr(w, r, errf(415, "bad_content_type", "Upload chunks must use application/offset+octet-stream."))
		return
	}
	offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil {
		s.writeErr(w, r, errf(400, "bad_offset", "Upload-Offset header is required."))
		return
	}
	if up.FileID != "" { // already complete (response to the final PATCH may have been lost)
		w.Header().Set("Upload-Offset", strconv.FormatInt(up.Size, 10))
		w.Header().Set("Ferry-File-Id", up.FileID)
		w.WriteHeader(204)
		return
	}
	if offset != up.Received {
		w.Header().Set("Upload-Offset", strconv.FormatInt(up.Received, 10))
		s.writeErr(w, r, errf(409, "offset_mismatch", "Upload offset mismatch; resume from the server's offset."))
		return
	}
	if _, loaded := busy.LoadOrStore(up.ID, true); loaded {
		s.writeErr(w, r, errf(423, "upload_busy", "This upload is already in progress in another request."))
		return
	}
	defer busy.Delete(up.ID)

	h := sha256.New()
	crc := uint32(up.CRC)
	if up.HashState != "" {
		st, err := base64.StdEncoding.DecodeString(up.HashState)
		if err == nil {
			err = h.(encoding.BinaryUnmarshaler).UnmarshalBinary(st)
		}
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
	} else if up.Received > 0 {
		s.writeErr(w, r, errors.New("upload hash state missing"))
		return
	}
	wc, err := s.store.OpenAppend("uploads/"+up.ID, up.Received)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.metrics.uploadsActive.Add(1)
	defer s.metrics.uploadsActive.Add(-1)

	remaining := up.Size - up.Received
	body := countingReader{io.LimitReader(r.Body, remaining+1), &s.metrics.bytesIn}
	buf := make([]byte, 1<<20)
	sinceSave := int64(0)
	save := func() error {
		if sy, ok := wc.(interface{ Sync() error }); ok {
			if err := sy.Sync(); err != nil {
				return err
			}
		}
		st, err := h.(encoding.BinaryMarshaler).MarshalBinary()
		if err != nil {
			return err
		}
		up.HashState = base64.StdEncoding.EncodeToString(st)
		up.CRC = int64(crc)
		up.UpdatedAt = nowMs()
		// Background context: progress must be saved even when the client disconnected.
		_, err = s.db.Exec(context.Background(), `UPDATE uploads SET received = ?, hash_state = ?, crc32 = ?, updated_at = ? WHERE id = ?`,
			up.Received, up.HashState, up.CRC, up.UpdatedAt, up.ID)
		sinceSave = 0
		return err
	}
	var copyErr error
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if up.Received+int64(n) > up.Size {
				copyErr = errf(413, "too_much_data", "More data was sent than the declared file size.")
				n = int(up.Size - up.Received)
			}
			if _, werr := wc.Write(buf[:n]); werr != nil {
				copyErr = werr
				break
			}
			h.Write(buf[:n])
			crc = crc32.Update(crc, crc32.IEEETable, buf[:n])
			up.Received += int64(n)
			sinceSave += int64(n)
			if sinceSave >= progressEvery {
				if err := save(); err != nil {
					copyErr = err
					break
				}
			}
		}
		if copyErr != nil || rerr != nil {
			if rerr != io.EOF && copyErr == nil {
				copyErr = rerr
			}
			break
		}
	}
	saveErr := save()
	wc.Close()
	if saveErr != nil {
		s.writeErr(w, r, saveErr)
		return
	}
	if copyErr != nil {
		var ae *apiErr
		if errors.As(copyErr, &ae) {
			s.writeErr(w, r, ae)
		}
		// Otherwise the client disconnected; progress is saved and it can resume.
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(up.Received, 10))
	if up.Received == up.Size && up.Concat != "partial" { // parts are joined when the client creates the final upload
		s.tusFinish(w, r.WithContext(context.WithoutCancel(ctx)), up, owner, share, h)
		if w.Header().Get("Ferry-Error") != "" {
			return
		}
	}
	w.WriteHeader(204)
}

// tusFinish turns a complete upload into a file. On failure it writes the error response and
// sets the Ferry-Error header so the caller does not write a success status.
func (s *Server) tusFinish(w http.ResponseWriter, r *http.Request, up *upload, owner *User, share *Share, h hash.Hash) {
	fail := func(err error) {
		w.Header().Set("Ferry-Error", "1")
		s.writeErr(w, r, err)
	}
	f, skipped, err := s.finalizeUpload(r.Context(), up, owner, share, hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		fail(err)
		return
	}
	w.Header().Set("Ferry-File-Id", f.ID)
	w.Header().Set("Ferry-Sha256", f.SHA256)
	w.Header().Set("Ferry-File-Name", url.PathEscape(f.Name))
	if skipped {
		w.Header().Set("Ferry-Skipped", "1")
	}
}

// magic lists the sniffed types an extension must match when an upload link restricts types.
var magic = map[string][]string{
	".pdf": {"application/pdf"}, ".png": {"image/png"}, ".jpg": {"image/jpeg"}, ".jpeg": {"image/jpeg"},
	".gif": {"image/gif"}, ".webp": {"image/webp"}, ".bmp": {"image/bmp"}, ".zip": {"application/zip"},
	".docx": {"application/zip"}, ".xlsx": {"application/zip"}, ".pptx": {"application/zip"},
	".odt": {"application/zip"}, ".ods": {"application/zip"}, ".mp3": {"audio/mpeg"}, ".mp4": {"video/mp4"},
	".webm": {"video/webm"}, ".wav": {"audio/wave"}, ".ogg": {"application/ogg", "audio/ogg", "video/ogg"},
	".txt": {"text/plain"}, ".csv": {"text/plain"}, ".md": {"text/plain"}, ".gz": {"application/x-gzip"},
	".rar": {"application/x-rar-compressed"}, ".7z": {"application/octet-stream"},
}

func (s *Server) finalizeUpload(ctx context.Context, up *upload, owner *User, share *Share, sum string) (*File, bool, error) {
	key := "uploads/" + up.ID
	discard := func() {
		s.db.Exec(ctx, `DELETE FROM uploads WHERE id = ?`, up.ID)
		s.store.Remove(key)
	}
	if up.ClientSHA != "" && up.ClientSHA != sum {
		discard()
		return nil, false, errf(460, "checksum_mismatch", "The file was corrupted during upload (checksum mismatch). Please upload it again.")
	}
	rc, _, _, err := s.store.OpenRead(key)
	if err != nil {
		return nil, false, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(rc, head)
	rc.Close()
	head = head[:n]
	mimeType := detectMime(head, up.Name)
	if share != nil && share.AllowedTypes != "" {
		ext := strings.ToLower(path.Ext(up.Name))
		if want, ok := magic[ext]; ok && n > 0 {
			sniffed := baseMime(http.DetectContentType(head))
			okType := false
			for _, m := range want {
				if sniffed == m {
					okType = true
				}
			}
			if !okType {
				discard()
				return nil, false, errf(415, "content_mismatch", "\""+up.Name+"\" is not a valid "+strings.TrimPrefix(ext, ".")+" file.")
			}
		}
	}
	if s.conf().ScanCommand != "" {
		if err := s.scan(ctx, key); err != nil {
			discard()
			s.log.Warn("upload rejected by scanner", "upload", up.ID, "err", err)
			return nil, false, errf(422, "scan_failed", "\""+up.Name+"\" was rejected by the server's security scan.")
		}
	}
	now := nowMs()
	f := &File{ID: newID(), Name: up.Name, FolderID: up.FolderID, TransferID: up.TransferID, Size: up.Size, Mime: mimeType,
		SHA256: sum, CreatedAt: now, UpdatedAt: now, UserID: owner.ID, Blob: up.ID}
	if err := s.store.Commit(key, "blobs/"+up.ID); err != nil {
		return nil, false, err
	}
	var oldBlob string
	skipped := false
	err = s.db.InTx(ctx, func(tx *db.Tx) error {
		existing, err := scanFile(tx.QueryRow(ctx, `SELECT `+fileCols+` FROM files WHERE user_id = ? AND folder_id = ? AND transfer_id = ? AND name = ?`,
			owner.ID, f.FolderID, f.TransferID, f.Name))
		if err != nil && !db.IsNoRows(err) {
			return err
		}
		exists := err == nil
		switch {
		case exists && up.Conflict == "skip":
			skipped = true
			f = existing
		case exists && up.Conflict == "replace":
			oldBlob = existing.Blob
			f.ID, f.CreatedAt = existing.ID, existing.CreatedAt
			_, err = tx.Exec(ctx, `UPDATE files SET size = ?, mime = ?, sha256 = ?, blob = ?, updated_at = ? WHERE id = ?`, f.Size, f.Mime, f.SHA256, f.Blob, now, f.ID)
			if err != nil {
				return err
			}
		default:
			base := f.Name
			for i := 0; ; i++ {
				if i > 0 {
					f.Name = numberedName(base, i)
				}
				var dummy string
				if tx.QueryRow(ctx, `SELECT id FROM files WHERE user_id = ? AND folder_id = ? AND transfer_id = ? AND name = ?`, owner.ID, f.FolderID, f.TransferID, f.Name).Scan(&dummy) == nil {
					continue
				}
				_, err = tx.Exec(ctx, `INSERT INTO files (`+fileCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					f.ID, f.Name, f.FolderID, f.TransferID, f.Size, f.Mime, f.SHA256, f.CreatedAt, f.UpdatedAt, f.UserID, f.Blob)
				if db.IsUnique(err) && i < 1000 {
					continue // a concurrent upload took this name
				}
				if err != nil {
					return err
				}
				break
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE uploads SET file_id = ?, received = size, hash_state = '', updated_at = ? WHERE id = ?`, f.ID, now, up.ID); err != nil {
			return err
		}
		if !skipped {
			if _, err := tx.Exec(ctx, `UPDATE files SET crc32 = ? WHERE id = ?`, up.CRC, f.ID); err != nil {
				return err
			}
		}
		if share != nil {
			_, err := tx.Exec(ctx, `UPDATE shares SET upload_count = upload_count + 1, uploaded_bytes = uploaded_bytes + ?, last_access = ? WHERE id = ?`, f.Size, now, share.ID)
			return err
		}
		return nil
	})
	if err != nil {
		s.store.Remove("blobs/" + up.ID)
		return nil, false, err
	}
	if skipped {
		s.store.Remove("blobs/" + up.ID)
	}
	if oldBlob != "" {
		s.streams.cancel(f.ID)
		s.store.Remove("blobs/" + oldBlob)
	}
	if share == nil {
		s.audit(ctx, nil, owner.ID, "file_uploaded", f.ID, f.Name+" ("+humanSize(f.Size)+")")
	}
	if share != nil {
		s.audit(ctx, nil, owner.ID, "upload_link_received", share.ID, f.Name)
		detail := f.Name
		if up.Uploader != "" {
			detail += " — from " + up.Uploader
		}
		s.linkEvent(ctx, nil, share, "upload", detail)
		if share.Notify {
			go s.notifyUpload(owner, share, f, up.Uploader)
		}
	}
	return f, skipped, nil
}

// scan runs FERRY_SCAN_COMMAND with {path} replaced; non-zero exit rejects the file.
func (s *Server) scan(ctx context.Context, key string) error {
	p, ok := s.store.LocalPath(key)
	if !ok {
		return nil
	}
	args := strings.Fields(s.conf().ScanCommand)
	for i := range args {
		args[i] = strings.ReplaceAll(args[i], "{path}", p)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	s.scanMu.Lock() // scanners are CPU/memory heavy; one at a time
	defer s.scanMu.Unlock()
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(string(out)) + ": " + err.Error())
	}
	return nil
}
