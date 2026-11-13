package ulog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// archiveLayout is the timestamp layout in archive names. An archive is named
// <path>.<archiveLayout>, or <path>.<archiveLayout>.<n> (n = 1, 2, ...) when
// more than one rotation happens within the same second.
const archiveLayout = "20060102-150405"

// FileWriter is a writer that writes to a file with optional rotation.
//
// When the file would grow past maxSize, it is renamed to an archive and a new
// file is started. After each rotation, archives beyond the newest maxBackups
// and archives older than maxAge days are removed. Zero disables each limit.
//
// A failed rotation never turns logging off: if the rename fails, the writer
// keeps appending to the active file and tries again after another maxSize
// bytes; if the new file cannot be opened, Write returns the error and the
// next Write tries to open it again.
type FileWriter struct {
	path       string
	file       *os.File
	maxSize    int64
	maxBackups int
	maxAge     int
	size       int64
	closed     bool
	mu         sync.Mutex

	// File operations and clock, replaced in tests to inject failures.
	openFile  func(name string, flag int, perm os.FileMode) (*os.File, error)
	rename    func(oldpath, newpath string) error
	closeFile func(f *os.File) error
	now       func() time.Time
}

// NewFileWriter creates a new FileWriter
func NewFileWriter(path string, maxSize int64, maxBackups int, maxAge int) (*FileWriter, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	w := &FileWriter{
		path:       path,
		maxSize:    maxSize,
		maxBackups: maxBackups,
		maxAge:     maxAge,
		openFile:   os.OpenFile,
		rename:     os.Rename,
		closeFile:  (*os.File).Close,
		now:        time.Now,
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// Write implements the io.Writer interface. After Close it returns
// os.ErrClosed.
func (w *FileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, os.ErrClosed
	}

	// A previous rotation could not open the new file: try again.
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}

	// Rotate when this write would pass maxSize. An empty file is never
	// rotated, so a single record larger than maxSize does not leave an empty
	// archive behind.
	if w.maxSize > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err = w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close implements the io.Closer interface. Closing twice is a no-op.
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	if w.file == nil {
		return nil
	}
	err := w.closeFile(w.file)
	w.file = nil
	return err
}

// open opens w.path for appending and makes it the active file.
func (w *FileWriter) open() error {
	file, err := w.openFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}

	w.file = file
	w.size = info.Size()
	return nil
}

// rotate archives the active file and starts a new one.
//
// It returns an error only when it leaves the writer without an open file;
// w.file is then nil and the next Write opens it again. A failed rename is not
// reported: the active file is reopened, the record goes there, and the next
// attempt comes after another maxSize bytes rather than on every write.
func (w *FileWriter) rotate() error {
	// The handle is gone after Close even when Close reports an error, so go
	// on with a fresh one either way.
	_ = w.closeFile(w.file)
	w.file = nil

	if err := w.rename(w.path, w.archivePath()); err != nil {
		if err := w.open(); err != nil {
			return err
		}
		w.size = 0
		return nil
	}

	// Retention problems must not stop logging. A failed removal is retried
	// at the next rotation.
	if w.maxBackups > 0 || w.maxAge > 0 {
		_ = w.cleanOldBackups()
	}

	return w.open()
}

// archivePath returns a name for the next archive that no file has yet.
func (w *FileWriter) archivePath() string {
	base := w.path + "." + w.now().Format(archiveLayout)
	name := base
	for n := 1; ; n++ {
		if _, err := os.Lstat(name); err != nil {
			return name
		}
		name = base + "." + strconv.Itoa(n)
	}
}

// archive is one rotated file of a FileWriter.
type archive struct {
	path string
	ts   time.Time
	seq  int
}

// cleanOldBackups applies maxBackups and maxAge to the archives of w.path.
//
// Only files named <base>.<archiveLayout> or <base>.<archiveLayout>.<n> count
// as archives; any other file in the directory is neither counted nor removed.
// Archives are ranked newest first by the timestamp in their name, then by n.
// Age is taken from that timestamp (the rotation time, local time zone), not
// from the file's modification time.
//
// Every removal is attempted; the first error is returned.
func (w *FileWriter) cleanOldBackups() error {
	dir := filepath.Dir(w.path)
	prefix := filepath.Base(w.path) + "."

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var archives []archive
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok || entry.IsDir() {
			continue
		}
		ts, seq, ok := parseArchiveSuffix(suffix)
		if !ok {
			continue
		}
		archives = append(archives, archive{path: filepath.Join(dir, entry.Name()), ts: ts, seq: seq})
	}

	sort.Slice(archives, func(i, j int) bool {
		if !archives[i].ts.Equal(archives[j].ts) {
			return archives[i].ts.After(archives[j].ts)
		}
		return archives[i].seq > archives[j].seq
	})

	var cutoff time.Time
	if w.maxAge > 0 {
		cutoff = w.now().Add(-time.Duration(w.maxAge) * 24 * time.Hour)
	}

	var firstErr error
	for i, a := range archives {
		tooMany := w.maxBackups > 0 && i >= w.maxBackups
		tooOld := w.maxAge > 0 && a.ts.Before(cutoff)
		if !tooMany && !tooOld {
			continue
		}
		if err := os.Remove(a.path); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// parseArchiveSuffix parses the part of an archive name after "<base>.":
// a timestamp in archiveLayout, optionally followed by ".<n>" with n >= 1.
func parseArchiveSuffix(suffix string) (ts time.Time, seq int, ok bool) {
	if len(suffix) < len(archiveLayout) {
		return time.Time{}, 0, false
	}
	stamp, rest := suffix[:len(archiveLayout)], suffix[len(archiveLayout):]

	ts, err := time.ParseInLocation(archiveLayout, stamp, time.Local)
	if err != nil || ts.Format(archiveLayout) != stamp {
		return time.Time{}, 0, false
	}
	if rest == "" {
		return ts, 0, true
	}

	digits, found := strings.CutPrefix(rest, ".")
	if !found || digits == "" || digits[0] == '0' {
		return time.Time{}, 0, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return time.Time{}, 0, false
		}
	}
	seq, err = strconv.Atoi(digits)
	if err != nil {
		return time.Time{}, 0, false
	}
	return ts, seq, true
}

func ParseByteSize(size string) (int64, error) {
	// Define unit multipliers
	units := map[string]int64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
		"TB": 1024 * 1024 * 1024 * 1024,
		"PB": 1024 * 1024 * 1024 * 1024 * 1024,
	}

	// Regular expression to match number and unit
	re := regexp.MustCompile(`^(\d+(?:\.\d+)?)([A-Za-z]+)$`)
	matches := re.FindStringSubmatch(size)

	if len(matches) != 3 {
		return 0, errors.New("invalid size format")
	}

	// Parse the numeric part
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, err
	}

	// Get the unit and convert to uppercase for case-insensitive matching
	unit := strings.ToUpper(matches[2])

	// Check if the unit exists in our map
	multiplier, exists := units[unit]
	if !exists {
		return 0, fmt.Errorf("unknown unit: %s", unit)
	}

	// Calculate the bytes
	bytes := int64(value * float64(multiplier))

	return bytes, nil
}
