package lan

import "io"

// Pipeline overlaps the two ends of a copy: produce fills buffers on its own goroutine (returning io.EOF at the end)
// while consume handles them on the caller's — socket reads against disk writes, or disk reads against socket writes.
// Memory is bounded to depth buffers of size bytes. The first error from either side is returned.
func Pipeline(size, depth int, produce func(buf []byte) (int, error), consume func(buf []byte) error) error {
	type chunk struct {
		buf []byte
		n   int
		err error
	}
	free := make(chan *chunk, depth)
	full := make(chan *chunk, depth)
	for i := 0; i < depth; i++ {
		free <- &chunk{buf: make([]byte, size)}
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(full)
		for {
			var c *chunk
			select {
			case c = <-free:
			case <-done:
				return
			}
			c.n, c.err = produce(c.buf)
			select {
			case full <- c:
			case <-done:
				return
			}
			if c.err != nil {
				return
			}
		}
	}()
	for c := range full {
		if c.n > 0 {
			if err := consume(c.buf[:c.n]); err != nil {
				return err
			}
		}
		if c.err != nil {
			if c.err == io.EOF {
				return nil
			}
			return c.err
		}
		free <- c
	}
	return nil
}

// ReadAhead returns a reader over produce's output that is filled ahead of the consumer on its own goroutine.
// Closing it stops the producer.
func ReadAhead(size, depth int, produce func(buf []byte) (int, error)) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(Pipeline(size, depth, produce, func(b []byte) error { _, err := pw.Write(b); return err }))
	}()
	return pr
}
