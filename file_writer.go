package ulog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FileWriter is a writer that writes to a file with optional rotation
type FileWriter struct {
	path       string
	file       *os.File
	maxSize    int64
	maxBackups int
	maxAge     int
	size       int64
	mu         sync.Mutex
}

// NewFileWriter creates a new FileWriter
func NewFileWriter(path string, maxSize int64, maxBackups int, maxAge int) (*FileWriter, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// Open the file
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	// Get file info to determine current size
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	return &FileWriter{
		path:       path,
		file:       file,
		maxSize:    maxSize,
		maxBackups: maxBackups,
		maxAge:     maxAge,
		size:       info.Size(),
	}, nil
}

// Write implements the io.Writer interface
func (w *FileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if rotation is needed
	if w.maxSize > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	// Write to file
	n, err = w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close implements the io.Closer interface
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// rotate rotates the current file
func (w *FileWriter) rotate() error {
	// Close current file
	if err := w.file.Close(); err != nil {
		return err
	}

	// Rotate files
	timestamp := time.Now().Format("20060102-150405")
	rotatedPath := fmt.Sprintf("%s.%s", w.path, timestamp)

	if err := os.Rename(w.path, rotatedPath); err != nil {
		return err
	}

	// Open a new file
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.file = file
	w.size = 0

	// Clean old backups
	if w.maxBackups > 0 || w.maxAge > 0 {
		w.cleanOldBackups()
	}

	return nil
}

// cleanOldBackups removes old backup files
func (w *FileWriter) cleanOldBackups() error {
	dir := filepath.Dir(w.path)
	base := filepath.Base(w.path)

	// List all files in directory
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// Find backup files
	var backups []string
	for _, file := range files {
		if !file.IsDir() && filepath.Base(file.Name()) != base &&
			len(file.Name()) > len(base) && file.Name()[:len(base)] == base {
			backups = append(backups, filepath.Join(dir, file.Name()))
		}
	}

	// Sort backups by modification time (newest first)
	if len(backups) > 0 {
		// Keep only maxBackups files
		if w.maxBackups > 0 && len(backups) > w.maxBackups {
			for i := w.maxBackups; i < len(backups); i++ {
				os.Remove(backups[i])
			}
		}

		// Remove files older than maxAge
		if w.maxAge > 0 {
			cutoff := time.Now().Add(-time.Duration(w.maxAge) * 24 * time.Hour)
			for _, backup := range backups {
				info, err := os.Stat(backup)
				if err == nil && info.ModTime().Before(cutoff) {
					os.Remove(backup)
				}
			}
		}
	}

	return nil
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
