package server

// ZIP downloads with a known size that can be resumed. The archive is uncompressed (store), and
// every file's size and CRC-32 are known up front, so the complete byte layout of the ZIP is
// computed before sending anything. That lets http.ServeContent answer Range requests: a dropped
// download continues where it stopped instead of starting over, and browsers show real progress.

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"ferry/internal/storage"
)

// zipSeg is one piece of the archive: either literal header bytes or a range of a stored file.
type zipSeg struct {
	start int64
	mem   []byte
	blob  string
	size  int64
}

// zipPlan records what zip.Writer produces. File data isn't copied while planning: only its
// length is counted and remembered as a reference to the stored file.
type zipPlan struct {
	segs []zipSeg
	size int64
	blob string // set while "writing" a file's data
}

func (p *zipPlan) Write(b []byte) (int, error) {
	n := int64(len(b))
	switch last := len(p.segs) - 1; {
	case p.blob != "" && last >= 0 && p.segs[last].blob == p.blob:
		p.segs[last].size += n
	case p.blob != "":
		p.segs = append(p.segs, zipSeg{start: p.size, blob: p.blob, size: n})
	case last >= 0 && p.segs[last].blob == "":
		p.segs[last].mem = append(p.segs[last].mem, b...)
		p.segs[last].size += n
	default:
		p.segs = append(p.segs, zipSeg{start: p.size, mem: append([]byte(nil), b...), size: n})
	}
	p.size += n
	return len(b), nil
}

var zeros = make([]byte, 1<<20)

// planZip lays out a ZIP of entries. crcs holds each file's CRC-32.
func planZip(entries []entry, crcs map[string]uint32) (*zipPlan, error) {
	p := &zipPlan{}
	zw := zip.NewWriter(p)
	for _, e := range entries {
		size := uint64(e.File.Size)
		fh := &zip.FileHeader{Name: e.Path, Method: zip.Store, CRC32: crcs[e.File.ID], CompressedSize64: size, UncompressedSize64: size,
			Modified: time.UnixMilli(e.File.UpdatedAt).UTC()}
		//lint:ignore SA1019 CreateRaw, unlike CreateHeader, doesn't derive the DOS time fields from Modified
		fh.ModifiedDate, fh.ModifiedTime = dosTime(fh.Modified)
		fh.SetMode(0o644)
		fh.Flags |= 0x8 // sizes follow the data (with ZIP64 for big files), as in streamed archives
		if !isASCII(e.Path) {
			fh.Flags |= 0x800 // UTF-8 names
		}
		fh.ReaderVersion = 20
		if size >= 1<<32-1 {
			fh.ReaderVersion = 45
		}
		fw, err := zw.CreateRaw(fh)
		if err != nil {
			return nil, err
		}
		// zip.Writer buffers internally: flush so header bytes and file bytes reach the plan separately.
		if err := zw.Flush(); err != nil {
			return nil, err
		}
		p.blob = e.File.Blob
		for left := e.File.Size; left > 0; {
			n := min(left, int64(len(zeros)))
			fw.Write(zeros[:n]) // counted, not stored (see Write)
			left -= n
		}
		if err := zw.Flush(); err != nil {
			return nil, err
		}
		p.blob = ""
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return p, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// dosTime converts to the MS-DOS date/time stored in ZIP headers.
func dosTime(t time.Time) (date, tm uint16) {
	if t.Year() < 1980 {
		t = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9), uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
}

// zipReader reads a planned archive, opening stored files as needed.
type zipReader struct {
	ctx   context.Context
	store storage.Backend
	plan  *zipPlan
	pos   int64
	open  string
	rc    storage.ReadSeekCloser
}

func (z *zipReader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		off += z.pos
	case io.SeekEnd:
		off += z.plan.size
	}
	if off < 0 {
		return 0, errors.New("negative position")
	}
	z.pos = off
	return off, nil
}

func (z *zipReader) Read(b []byte) (int, error) {
	if err := z.ctx.Err(); err != nil {
		return 0, err
	}
	if z.pos >= z.plan.size {
		return 0, io.EOF
	}
	// Find the segment containing pos (few segments per file; a linear scan is fine).
	i := 0
	for i < len(z.plan.segs)-1 && z.plan.segs[i+1].start <= z.pos {
		i++
	}
	sg := z.plan.segs[i]
	off := z.pos - sg.start
	b = b[:min(int64(len(b)), sg.size-off)]
	var n int
	if sg.blob == "" {
		n = copy(b, sg.mem[off:])
	} else {
		if z.open != sg.blob {
			z.Close()
			rc, _, _, err := z.store.OpenRead("blobs/" + sg.blob)
			if err != nil {
				return 0, err
			}
			z.rc, z.open = rc, sg.blob
		}
		if _, err := z.rc.Seek(off, io.SeekStart); err != nil {
			return 0, err
		}
		var err error
		if n, err = io.ReadFull(z.rc, b); err != nil {
			return n, err // a stored file shorter than recorded: fail instead of sending a broken archive
		}
	}
	z.pos += int64(n)
	return n, nil
}

func (z *zipReader) Close() error {
	if z.rc != nil {
		z.rc.Close()
		z.rc, z.open = nil, ""
	}
	return nil
}

// fileCRCs returns the CRC-32 of each file, computing (once, then storing) any that are missing
// for files uploaded before CRCs were recorded.
func (s *Server) fileCRCs(ctx context.Context, entries []entry) (map[string]uint32, error) {
	out := map[string]uint32{}
	for _, e := range entries {
		var c int64 = -1
		s.db.QueryRow(ctx, `SELECT crc32 FROM files WHERE id = ?`, e.File.ID).Scan(&c)
		if c < 0 {
			rc, _, _, err := s.store.OpenRead("blobs/" + e.File.Blob)
			if err != nil {
				return nil, s.storageAPIErr(e.File, err)
			}
			h := crc32.NewIEEE()
			_, err = io.Copy(h, rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			c = int64(h.Sum32())
			s.db.Exec(ctx, `UPDATE files SET crc32 = ? WHERE id = ?`, c, e.File.ID)
		}
		out[e.File.ID] = uint32(c)
	}
	return out, nil
}

// serveZip answers a ZIP download, including Range requests for resuming. It fails before sending
// anything if a file is unreadable, so recipients never get a silently incomplete archive.
func (s *Server) serveZip(w http.ResponseWriter, r *http.Request, name string, entries []entry, streamKeys ...string) error {
	if err := s.checkBlobs(entries); err != nil {
		return err
	}
	crcs, err := s.fileCRCs(r.Context(), entries)
	if err != nil {
		return err
	}
	plan, err := planZip(entries, crcs)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	keys := append([]string{}, streamKeys...)
	etag := sha256.New()
	var mod int64
	for _, e := range entries {
		keys = append(keys, e.File.ID)
		etag.Write([]byte(e.Path + "\x00" + e.File.SHA256 + "\x00"))
		mod = max(mod, e.File.UpdatedAt)
	}
	defer s.streams.add(keys, cancel)()
	s.metrics.downloadsActive.Add(1)
	defer s.metrics.downloadsActive.Add(-1)

	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", contentDisposition("attachment", name))
	h.Set("Cache-Control", "private, no-cache")
	// The ETag changes when any file changes, so a resumed download never mixes two versions.
	h.Set("ETag", `"z-`+hex.EncodeToString(etag.Sum(nil))[:24]+`-`+strconv.FormatInt(plan.size, 36)+`"`)
	zr := &zipReader{ctx: ctx, store: s.store, plan: plan}
	defer zr.Close()
	http.ServeContent(w, r.WithContext(ctx), "", time.UnixMilli(mod), zr)
	return nil
}
