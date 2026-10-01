package lan

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestPipelineCopiesInOrder(t *testing.T) {
	src := bytes.Repeat([]byte("0123456789abcdef"), 10_000)
	r := bytes.NewReader(src)
	var out bytes.Buffer
	err := Pipeline(1000, 3, r.Read, func(b []byte) error { out.Write(b); return nil })
	if err != nil || !bytes.Equal(out.Bytes(), src) {
		t.Fatalf("copy mismatch: err=%v len=%d want=%d", err, out.Len(), len(src))
	}
}

func TestPipelineReportsEitherSideFailure(t *testing.T) {
	boom := errors.New("boom")
	if err := Pipeline(10, 2, func([]byte) (int, error) { return 0, boom }, func([]byte) error { return nil }); err != boom {
		t.Fatalf("producer error lost: %v", err)
	}
	r := bytes.NewReader(make([]byte, 1000))
	if err := Pipeline(10, 2, r.Read, func([]byte) error { return boom }); err != boom {
		t.Fatalf("consumer error lost: %v", err)
	}
}

func TestReadAheadStopsOnClose(t *testing.T) {
	ra := ReadAhead(10, 2, func(b []byte) (int, error) { return len(b), nil }) // endless source
	buf := make([]byte, 25)
	if _, err := io.ReadFull(ra, buf); err != nil {
		t.Fatal(err)
	}
	ra.Close() // must not leak or block
}
