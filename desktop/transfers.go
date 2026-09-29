package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Statuses are the same strings the server and the Android app use.
const (
	stCreated      = "created"
	stWaiting      = "waiting"
	stConnecting   = "connecting"
	stTransferring = "transferring"
	stVerifying    = "verifying"
	stCompleted    = "completed"
	stFailed       = "failed"
	stCancelled    = "cancelled"
	stRejected     = "rejected"
	stInterrupted  = "interrupted"
)

func final(s string) bool {
	return s == stCompleted || s == stFailed || s == stCancelled || s == stRejected
}

type TFile struct {
	ID           string `json:"id"`
	Name         string `json:"name"` // relative path shown to people ("Folder/a.jpg")
	Path         string `json:"path"` // local file: the source when sending, the saved file when receiving
	Size         int64  `json:"size"`
	Done         int64  `json:"done"`
	Mime         string `json:"mime"`
	SHA256       string `json:"sha256,omitempty"`
	ServerFileID string `json:"serverFileId,omitempty"`
}

type Transfer struct {
	ID               string  `json:"id"`
	Direction        string  `json:"direction"` // sent | received
	Method           string  `json:"method"`    // direct | server | link
	Peer             string  `json:"peer"`
	Status           string  `json:"status"`
	Note             string  `json:"note"`
	Error            string  `json:"error"`
	Files            []TFile `json:"files"`
	Total            int64   `json:"total"`
	Done             int64   `json:"done"`
	Speed            float64 `json:"speed"` // bytes per second
	CreatedAt        int64   `json:"createdAt"`
	UpdatedAt        int64   `json:"updatedAt"`
	ShareURL         string  `json:"shareUrl,omitempty"`
	SaveDir          string  `json:"saveDir,omitempty"`
	CanPause         bool    `json:"canPause"`
	Paused           bool    `json:"paused"`
	CanRetry         bool    `json:"canRetry"`
	ServerTransferID string  `json:"serverTransferId,omitempty"`
}

type control struct {
	cancel  context.CancelFunc
	paused  bool
	restart func() // resumes a paused transfer in place
	retry   func() // starts it again as a new transfer
	// cancelPeer tells a direct receiver we gave up (set while a paused direct send holds a session).
	cancelPeer func()
}

// store holds active transfers and history and pushes changes to the UI (at most every 200 ms).
type store struct {
	mu       sync.Mutex
	active   map[string]*Transfer
	history  []Transfer
	controls map[string]*control
	path     string
	dirty    bool
	emit     func(list []Transfer)
	speedAt  map[string]speedSample
}

type speedSample struct {
	t    time.Time
	done int64
}

func newStore(dir string, emit func([]Transfer)) *store {
	s := &store{active: map[string]*Transfer{}, controls: map[string]*control{}, path: filepath.Join(dir, "history.json"), emit: emit,
		speedAt: map[string]speedSample{}}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &s.history)
	}
	// Transfers that were running when the app closed are shown as interrupted.
	for i := range s.history {
		if !final(s.history[i].Status) {
			s.history[i].Status, s.history[i].Note, s.history[i].Speed = stInterrupted, "Ferry was closed during this transfer.", 0
		}
	}
	go func() {
		for range time.Tick(200 * time.Millisecond) {
			s.flush()
		}
	}()
	return s
}

func (s *store) flush() {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return
	}
	s.dirty = false
	list := s.listLocked()
	s.mu.Unlock()
	s.emit(list)
}

func (s *store) list() []Transfer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *store) listLocked() []Transfer {
	out := make([]Transfer, 0, len(s.active)+len(s.history))
	for _, t := range s.active {
		c := *t
		c.Files = append([]TFile(nil), t.Files...)
		c.CanRetry = s.controls[t.ID] != nil && s.controls[t.ID].retry != nil
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return append(out, s.history...)
}

func (s *store) add(t *Transfer) {
	now := time.Now().UnixMilli()
	t.CreatedAt, t.UpdatedAt = now, now
	for _, f := range t.Files {
		t.Total += f.Size
	}
	s.mu.Lock()
	s.active[t.ID] = t
	s.dirty = true
	s.mu.Unlock()
}

func (s *store) get(id string) (Transfer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.active[id]
	if !ok {
		return Transfer{}, false
	}
	c := *t
	c.Files = append([]TFile(nil), t.Files...)
	return c, true
}

func (s *store) update(id string, fn func(t *Transfer)) {
	s.mu.Lock()
	if t, ok := s.active[id]; ok {
		fn(t)
		t.UpdatedAt = time.Now().UnixMilli()
		s.dirty = true
	}
	s.mu.Unlock()
}

func (s *store) status(id, status, note string) {
	s.update(id, func(t *Transfer) {
		t.Status, t.Note = status, note
		if status != stTransferring {
			t.Speed = 0
		}
	})
}

// progress sets how many bytes of one file are done and keeps a smoothed speed.
func (s *store) progress(id, fileID string, done int64) {
	s.update(id, func(t *Transfer) {
		var total int64
		for i := range t.Files {
			if t.Files[i].ID == fileID {
				t.Files[i].Done = done
			}
			total += t.Files[i].Done
		}
		now := time.Now()
		if prev, ok := s.speedAt[id]; ok {
			if dt := now.Sub(prev.t).Seconds(); dt >= 0.5 {
				inst := float64(total-prev.done) / dt
				if inst >= 0 {
					if t.Speed == 0 {
						t.Speed = inst
					} else {
						t.Speed = t.Speed*0.7 + inst*0.3
					}
				}
				s.speedAt[id] = speedSample{now, total}
			}
		} else {
			s.speedAt[id] = speedSample{now, total}
		}
		t.Done = total
	})
}

// finish moves a transfer to history.
func (s *store) finish(id, status, errMsg string) {
	s.mu.Lock()
	t, ok := s.active[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	t.Status, t.Error, t.Speed, t.Paused = status, errMsg, 0, false
	if status == stCompleted {
		t.Note = ""
		t.Done = t.Total
		for i := range t.Files {
			t.Files[i].Done = t.Files[i].Size
		}
	}
	t.UpdatedAt = time.Now().UnixMilli()
	c := s.controls[id]
	// Failed transfers that can be retried stay in the active list with their retry button.
	if status == stFailed && c != nil && c.retry != nil {
		s.dirty = true
		s.mu.Unlock()
		return
	}
	delete(s.active, id)
	delete(s.controls, id)
	delete(s.speedAt, id)
	s.history = append([]Transfer{*t}, s.history...)
	if len(s.history) > 300 {
		s.history = s.history[:300]
	}
	s.dirty = true
	h := append([]Transfer(nil), s.history...)
	s.mu.Unlock()
	s.saveHistory(h)
}

func (s *store) saveHistory(h []Transfer) {
	b, _ := json.Marshal(h)
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, s.path)
	}
}

func (s *store) control(id string) *control {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.controls[id]
	if c == nil {
		c = &control{}
		s.controls[id] = c
	}
	return c
}

// dismiss removes a transfer from the list (active failed ones, or history entries).
func (s *store) dismiss(id string) {
	s.mu.Lock()
	if t, ok := s.active[id]; ok && final(t.Status) {
		delete(s.active, id)
		delete(s.controls, id)
	}
	for i, t := range s.history {
		if t.ID == id {
			s.history = append(s.history[:i], s.history[i+1:]...)
			break
		}
	}
	s.dirty = true
	h := append([]Transfer(nil), s.history...)
	s.mu.Unlock()
	s.saveHistory(h)
}

func (s *store) clearHistory() {
	s.mu.Lock()
	s.history = nil
	for id, t := range s.active {
		if final(t.Status) {
			delete(s.active, id)
			delete(s.controls, id)
		}
	}
	s.dirty = true
	s.mu.Unlock()
	s.saveHistory(nil)
}

// findHistory returns a finished transfer (for "Show in folder").
func (s *store) find(id string) (Transfer, bool) {
	if t, ok := s.get(id); ok {
		return t, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.history {
		if t.ID == id {
			return t, true
		}
	}
	return Transfer{}, false
}
