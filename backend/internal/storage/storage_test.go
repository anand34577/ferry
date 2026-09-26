package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalLifecycle(t *testing.T) {
	root := t.TempDir()
	l, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := l.OpenAppend("uploads/abc123", 0)
	w.Write([]byte("hello world"))
	w.Close()
	// Resuming at an earlier offset truncates the unsaved tail.
	w, _ = l.OpenAppend("uploads/abc123", 5)
	w.Write([]byte("!"))
	w.Close()
	if _, err := l.OpenAppend("uploads/abc123", 99); err == nil {
		t.Fatal("offset past the data must fail")
	}
	if err := l.Commit("uploads/abc123", "blobs/abc123"); err != nil {
		t.Fatal(err)
	}
	rc, n, _, err := l.OpenRead("blobs/abc123")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello!" || n != 6 {
		t.Fatalf("%q %d", b, n)
	}
	if _, err := os.Stat(filepath.Join(root, "blobs", "ab", "abc123")); err != nil {
		t.Fatal("blob fan-out", err)
	}
	l.Remove("blobs/abc123")
	if _, _, _, err := l.OpenRead("blobs/abc123"); !errors.Is(err, ErrNotFound) {
		t.Fatal("want ErrNotFound", err)
	}
	os.Remove(filepath.Join(root, sentinel))
	if _, _, _, err := l.OpenRead("blobs/abc123"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("want ErrUnavailable when the storage root is gone", err)
	}
}
