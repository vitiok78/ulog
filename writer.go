package ulog

import (
	"io"
	"sync"
)

// Writer is an interface that wraps the basic Write method.
type Writer interface {
	io.Writer
	Close() error
}

// WriterCloser is a simple implementation of Writer that wraps an io.Writer
type WriterCloser struct {
	writer io.Writer
}

// NewWriterCloser creates a new WriterCloser
func NewWriterCloser(w io.Writer) *WriterCloser {
	return &WriterCloser{
		writer: w,
	}
}

// Write implements the io.Writer interface
func (w *WriterCloser) Write(p []byte) (n int, err error) {
	return w.writer.Write(p)
}

// Close implements the io.Closer interface
func (w *WriterCloser) Close() error {
	if closer, ok := w.writer.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// MultiWriter wraps multiple io.Writers into a single Writer. It is safe for
// concurrent use: writes are serialized, so writers that are not safe
// themselves (a bytes.Buffer, a bufio.Writer) can sit behind it.
type MultiWriter struct {
	mu      sync.Mutex
	writers []io.Writer
}

// NewMultiWriter creates a new MultiWriter with the provided writers
func NewMultiWriter(writers ...io.Writer) *MultiWriter {
	return &MultiWriter{writers: writers}
}

// Write implements the io.Writer interface
// It writes the same data to all writers and returns the number of bytes written
// and the first error encountered (if any)
func (mw *MultiWriter) Write(p []byte) (n int, err error) {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	for _, w := range mw.writers {
		n, err = w.Write(p)
		if err != nil {
			return n, err
		}
	}
	return len(p), nil
}

// AddWriter adds a new writer to the MultiWriter
func (mw *MultiWriter) AddWriter(w io.Writer) {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	mw.writers = append(mw.writers, w)
}
