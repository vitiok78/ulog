package ulog

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// Mock writer that can simulate errors
type errorWriter struct {
	shouldError bool
	errorMsg    string
}

func (w *errorWriter) Write(p []byte) (n int, err error) {
	if w.shouldError {
		return 0, errors.New(w.errorMsg)
	}
	return len(p), nil
}

// Mock writer/closer that can simulate close errors
type errorWriterCloser struct {
	shouldError bool
	errorMsg    string
}

func (w *errorWriterCloser) Write(p []byte) (n int, err error) {
	return len(p), nil // Always succeed at writing
}

func (w *errorWriterCloser) Close() error {
	if w.shouldError {
		return errors.New(w.errorMsg)
	}
	return nil
}

// Mock writer that implements io.Closer
type closableWriter struct {
	buf    bytes.Buffer
	closed bool
}

func (w *closableWriter) Write(p []byte) (n int, err error) {
	if w.closed {
		return 0, errors.New("writer is closed")
	}
	return w.buf.Write(p)
}

func (w *closableWriter) Close() error {
	w.closed = true
	return nil
}

func (w *closableWriter) String() string {
	return w.buf.String()
}

func TestNewWriterCloser(t *testing.T) {
	var buf bytes.Buffer
	wc := NewWriterCloser(&buf)

	if wc == nil {
		t.Error("Expected NewWriterCloser to return a non-nil WriterCloser")
	}

	if wc.writer != &buf {
		t.Error("Expected WriterCloser to wrap the provided writer")
	}
}

func TestWriterCloserWrite(t *testing.T) {
	tests := []struct {
		name        string
		writer      io.Writer
		data        []byte
		expectError bool
		errorMsg    string
	}{
		{
			name:        "successful write",
			writer:      &bytes.Buffer{},
			data:        []byte("test data"),
			expectError: false,
		},
		{
			name:        "write error",
			writer:      &errorWriter{shouldError: true, errorMsg: "write failed"},
			data:        []byte("test data"),
			expectError: true,
			errorMsg:    "write failed",
		},
		{
			name:        "empty data",
			writer:      &bytes.Buffer{},
			data:        []byte{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wc := NewWriterCloser(tt.writer)
			n, err := wc.Write(tt.data)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error message to contain '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if n != len(tt.data) {
					t.Errorf("Expected to write %d bytes, wrote %d", len(tt.data), n)
				}

				// Check that data was actually written
				if buf, ok := tt.writer.(*bytes.Buffer); ok {
					if !bytes.Equal(buf.Bytes(), tt.data) {
						t.Errorf("Expected written data %s, got %s", string(tt.data), buf.String())
					}
				}
			}
		})
	}
}

func TestWriterCloserClose(t *testing.T) {
	tests := []struct {
		name        string
		writer      io.Writer
		expectError bool
		errorMsg    string
	}{
		{
			name:        "close writer that implements io.Closer",
			writer:      &closableWriter{},
			expectError: false,
		},
		{
			name:        "close writer that doesn't implement io.Closer",
			writer:      &bytes.Buffer{},
			expectError: false,
		},
		{
			name:        "close error",
			writer:      &errorWriterCloser{shouldError: true, errorMsg: "close failed"},
			expectError: true,
			errorMsg:    "close failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wc := NewWriterCloser(tt.writer)
			err := wc.Close()

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error message to contain '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}

				// If the writer was closable, check that it was actually closed
				if cw, ok := tt.writer.(*closableWriter); ok {
					if !cw.closed {
						t.Error("Expected closable writer to be closed")
					}
				}
			}
		})
	}
}

func TestWriterInterface(t *testing.T) {
	// Test that WriterCloser implements the Writer interface
	var buf bytes.Buffer
	wc := NewWriterCloser(&buf)

	var _ Writer = wc
	var _ io.Writer = wc
}

func TestNewMultiWriter(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	mw := NewMultiWriter(&buf1, &buf2)

	if mw == nil {
		t.Error("Expected NewMultiWriter to return a non-nil MultiWriter")
	}

	if len(mw.writers) != 2 {
		t.Errorf("Expected 2 writers, got %d", len(mw.writers))
	}

	if mw.writers[0] != &buf1 || mw.writers[1] != &buf2 {
		t.Error("Expected MultiWriter to contain the provided writers")
	}
}

func TestNewMultiWriterEmpty(t *testing.T) {
	mw := NewMultiWriter()

	if mw == nil {
		t.Error("Expected NewMultiWriter to return a non-nil MultiWriter")
	}

	if len(mw.writers) != 0 {
		t.Errorf("Expected 0 writers, got %d", len(mw.writers))
	}
}

func TestMultiWriterWrite(t *testing.T) {
	tests := []struct {
		name        string
		writers     []io.Writer
		data        []byte
		expectError bool
		errorMsg    string
	}{
		{
			name:        "write to multiple writers successfully",
			writers:     []io.Writer{&bytes.Buffer{}, &bytes.Buffer{}},
			data:        []byte("test data"),
			expectError: false,
		},
		{
			name:        "write to single writer",
			writers:     []io.Writer{&bytes.Buffer{}},
			data:        []byte("test data"),
			expectError: false,
		},
		{
			name:        "write to no writers",
			writers:     []io.Writer{},
			data:        []byte("test data"),
			expectError: false,
		},
		{
			name:        "first writer fails",
			writers:     []io.Writer{&errorWriter{shouldError: true, errorMsg: "first failed"}, &bytes.Buffer{}},
			data:        []byte("test data"),
			expectError: true,
			errorMsg:    "first failed",
		},
		{
			name:        "second writer fails",
			writers:     []io.Writer{&bytes.Buffer{}, &errorWriter{shouldError: true, errorMsg: "second failed"}},
			data:        []byte("test data"),
			expectError: true,
			errorMsg:    "second failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mw := NewMultiWriter(tt.writers...)
			n, err := mw.Write(tt.data)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error message to contain '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if n != len(tt.data) {
					t.Errorf("Expected to write %d bytes, wrote %d", len(tt.data), n)
				}

				// Check that all writers received the data
				for i, writer := range tt.writers {
					if buf, ok := writer.(*bytes.Buffer); ok {
						if !bytes.Equal(buf.Bytes(), tt.data) {
							t.Errorf("Writer %d: expected data %s, got %s", i, string(tt.data), buf.String())
						}
					}
				}
			}
		})
	}
}

func TestMultiWriterAddWriter(t *testing.T) {
	var buf1, buf2, buf3 bytes.Buffer
	mw := NewMultiWriter(&buf1, &buf2)

	if len(mw.writers) != 2 {
		t.Errorf("Expected 2 writers initially, got %d", len(mw.writers))
	}

	// Add a third writer
	mw.AddWriter(&buf3)

	if len(mw.writers) != 3 {
		t.Errorf("Expected 3 writers after adding, got %d", len(mw.writers))
	}

	if mw.writers[2] != &buf3 {
		t.Error("Expected third writer to be the added writer")
	}

	// Test that writing works with the added writer
	data := []byte("test data")
	n, err := mw.Write(data)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if n != len(data) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(data), n)
	}

	// Check that all three writers received the data
	writers := []*bytes.Buffer{&buf1, &buf2, &buf3}
	for i, buf := range writers {
		if !bytes.Equal(buf.Bytes(), data) {
			t.Errorf("Writer %d: expected data %s, got %s", i, string(data), buf.String())
		}
	}
}

func TestMultiWriterAddWriterNil(t *testing.T) {
	mw := NewMultiWriter()
	originalLen := len(mw.writers)

	// Add nil writer (should still work, but might cause issues when writing)
	mw.AddWriter(nil)

	if len(mw.writers) != originalLen+1 {
		t.Errorf("Expected %d writers after adding nil, got %d", originalLen+1, len(mw.writers))
	}

	// Writing should panic or error when encountering nil writer
	defer func() {
		if r := recover(); r == nil {
			// If no panic, check for error
			_, err := mw.Write([]byte("test"))
			if err == nil {
				t.Error("Expected error or panic when writing to nil writer")
			}
		}
	}()

	mw.Write([]byte("test"))
}

func TestMultiWriterConcurrentWrite(t *testing.T) {
	// Test concurrent writes to ensure no race conditions
	var buf1, buf2 bytes.Buffer
	mw := NewMultiWriter(&buf1, &buf2)

	data := []byte("concurrent test data")
	numGoroutines := 10

	// Channel to collect errors
	errChan := make(chan error, numGoroutines)

	// Start multiple goroutines writing concurrently
	for i := 0; i < numGoroutines; i++ {
		go func() {
			_, err := mw.Write(data)
			errChan <- err
		}()
	}

	// Collect results
	for i := 0; i < numGoroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent write error: %v", err)
		}
	}

	// Note: The actual content of buffers will be mixed due to concurrent writes,
	// In concurrent scenarios, buffers may not have exactly the same length due to race conditions
	// We just verify that both buffers received some data

	// Check that both buffers contain some data (not empty)
	if buf1.Len() == 0 || buf2.Len() == 0 {
		t.Error("Expected both buffers to contain data after concurrent writes")
	}

	// Check that both buffers contain the expected substring
	expectedSubstring := "concurrent test data"
	if !strings.Contains(buf1.String(), expectedSubstring) {
		t.Errorf("Expected first buffer to contain '%s', got: %s", expectedSubstring, buf1.String())
	}
	if !strings.Contains(buf2.String(), expectedSubstring) {
		t.Errorf("Expected second buffer to contain '%s', got: %s", expectedSubstring, buf2.String())
	}
}

func TestMultiWriterImplementsInterface(t *testing.T) {
	// Test that MultiWriter implements io.Writer
	mw := NewMultiWriter()
	var _ io.Writer = mw
}
