package ulog

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		hasError bool
	}{
		// Basic tests with different units
		{"10B", 10, false},
		{"10KB", 10240, false},
		{"10MB", 10485760, false},
		{"10GB", 10737418240, false},
		{"2TB", 2199023255552, false},

		// Test with decimal values
		{"1.5MB", 1572864, false},
		{"0.5GB", 536870912, false},

		// Test with lowercase units
		{"10mb", 10485760, false},
		{"5kb", 5120, false},

		// Test with invalid inputs
		{"", 0, true},
		{"10", 0, true},
		{"MB", 0, true},
		{"10 MB", 0, true},  // Space not allowed
		{"10ZB", 0, true},   // Unknown unit
		{"ten MB", 0, true}, // Non-numeric value
		{"-10MB", 0, true},  // Negative values not supported by regex
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result, err := ParseByteSize(test.input)

			// Check if error status matches expectation
			if (err != nil) != test.hasError {
				t.Errorf("ParseByteSize(%q): got error: %v, expected error: %v",
					test.input, err, test.hasError)
				return
			}

			// If no error is expected, check the result value
			if !test.hasError && result != test.expected {
				t.Errorf("ParseByteSize(%q) = %d, expected %d",
					test.input, result, test.expected)
			}
		})
	}
}

func TestNewFileWriter(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name       string
		path       string
		maxSize    int64
		maxBackups int
		maxAge     int
		wantError  bool
	}{
		{
			name:       "valid file writer",
			path:       filepath.Join(tempDir, "test.log"),
			maxSize:    1024,
			maxBackups: 5,
			maxAge:     7,
			wantError:  false,
		},
		{
			name:       "nested directory creation",
			path:       filepath.Join(tempDir, "nested", "dir", "test.log"),
			maxSize:    1024,
			maxBackups: 5,
			maxAge:     7,
			wantError:  false,
		},
		{
			name:       "zero values",
			path:       filepath.Join(tempDir, "zero.log"),
			maxSize:    0,
			maxBackups: 0,
			maxAge:     0,
			wantError:  false,
		},
		{
			name:       "invalid path (read-only directory)",
			path:       "/root/test.log", // Assuming this will fail on most systems
			maxSize:    1024,
			maxBackups: 5,
			maxAge:     7,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fw, err := NewFileWriter(tt.path, tt.maxSize, tt.maxBackups, tt.maxAge)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if fw == nil {
				t.Error("Expected non-nil FileWriter")
				return
			}

			// Check that the file was created
			if _, err := os.Stat(tt.path); os.IsNotExist(err) {
				t.Error("Expected file to be created")
			}

			// Check field values
			if fw.path != tt.path {
				t.Errorf("Expected path %s, got %s", tt.path, fw.path)
			}

			if fw.maxSize != tt.maxSize {
				t.Errorf("Expected maxSize %d, got %d", tt.maxSize, fw.maxSize)
			}

			if fw.maxBackups != tt.maxBackups {
				t.Errorf("Expected maxBackups %d, got %d", tt.maxBackups, fw.maxBackups)
			}

			if fw.maxAge != tt.maxAge {
				t.Errorf("Expected maxAge %d, got %d", tt.maxAge, fw.maxAge)
			}

			// Clean up
			fw.Close()
		})
	}
}

func TestFileWriterWrite(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	fw, err := NewFileWriter(logPath, 100, 3, 7) // Small maxSize for testing rotation
	if err != nil {
		t.Fatalf("Failed to create FileWriter: %v", err)
	}
	defer fw.Close()

	tests := []struct {
		name     string
		data     []byte
		expected int
	}{
		{
			name:     "write small data",
			data:     []byte("small log entry\n"),
			expected: 16,
		},
		{
			name:     "write empty data",
			data:     []byte(""),
			expected: 0,
		},
		{
			name:     "write large data that triggers rotation",
			data:     []byte(strings.Repeat("large log entry that should trigger rotation\n", 5)),
			expected: 225, // 45 * 5 (corrected calculation)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := fw.Write(tt.data)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if n != tt.expected {
				t.Errorf("Expected to write %d bytes, wrote %d", tt.expected, n)
			}
		})
	}
}

func TestFileWriterClose(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	fw, err := NewFileWriter(logPath, 1024, 3, 7)
	if err != nil {
		t.Fatalf("Failed to create FileWriter: %v", err)
	}

	// Write some data
	_, err = fw.Write([]byte("test data"))
	if err != nil {
		t.Errorf("Unexpected write error: %v", err)
	}

	// Close the writer
	err = fw.Close()
	if err != nil {
		t.Errorf("Unexpected close error: %v", err)
	}

	// Verify file is closed by checking that the file handle is nil
	if fw.file != nil {
		t.Error("Expected file handle to be nil after close")
	}

	// Multiple closes should not error
	err = fw.Close()
	if err != nil {
		t.Errorf("Unexpected error on second close: %v", err)
	}
}

func TestFileWriterRotation(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "rotation.log")

	// Create FileWriter with small maxSize to trigger rotation
	fw, err := NewFileWriter(logPath, 50, 3, 7)
	if err != nil {
		t.Fatalf("Failed to create FileWriter: %v", err)
	}
	defer fw.Close()

	// Write data that will trigger rotation
	largeData := []byte(strings.Repeat("This is a log entry that will cause rotation\n", 3))
	_, err = fw.Write(largeData)
	if err != nil {
		t.Errorf("Unexpected write error: %v", err)
	}

	// Check that backup files were created
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp directory: %v", err)
	}

	backupCount := 0
	for _, file := range files {
		if strings.Contains(file.Name(), "rotation.log.") {
			backupCount++
		}
	}

	if backupCount == 0 {
		t.Error("Expected at least one backup file to be created")
	}
}

func TestFileWriterCleanOldBackups(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "cleanup.log")

	// Create some old backup files manually
	oldBackups := []string{
		"cleanup.log.20230101-120000",
		"cleanup.log.20230102-120000",
		"cleanup.log.20230103-120000",
		"cleanup.log.20230104-120000",
		"cleanup.log.20230105-120000",
	}

	for _, backup := range oldBackups {
		backupPath := filepath.Join(tempDir, backup)
		file, err := os.Create(backupPath)
		if err != nil {
			t.Fatalf("Failed to create backup file %s: %v", backup, err)
		}
		file.WriteString("old backup content")
		file.Close()

		// Set old modification time
		oldTime := time.Now().Add(-10 * 24 * time.Hour) // 10 days ago
		os.Chtimes(backupPath, oldTime, oldTime)
	}

	// Create FileWriter with maxBackups=3 and maxAge=7 days
	fw, err := NewFileWriter(logPath, 50, 3, 7)
	if err != nil {
		t.Fatalf("Failed to create FileWriter: %v", err)
	}
	defer fw.Close()

	// Trigger rotation which should clean old backups
	largeData := []byte(strings.Repeat("Data to trigger rotation and cleanup\n", 3))
	_, err = fw.Write(largeData)
	if err != nil {
		t.Errorf("Unexpected write error: %v", err)
	}

	// Check remaining files
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp directory: %v", err)
	}

	backupCount := 0
	for _, file := range files {
		if strings.Contains(file.Name(), "cleanup.log.") && !strings.HasSuffix(file.Name(), ".log") {
			backupCount++
		}
	}

	// Should have at most maxBackups (3) backup files
	if backupCount > 3 {
		t.Errorf("Expected at most 3 backup files, found %d", backupCount)
	}
}

func TestFileWriterConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "concurrent.log")

	fw, err := NewFileWriter(logPath, 1024, 3, 7)
	if err != nil {
		t.Fatalf("Failed to create FileWriter: %v", err)
	}
	defer fw.Close()

	// Test concurrent writes
	numGoroutines := 10
	numWrites := 10
	errChan := make(chan error, numGoroutines*numWrites)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < numWrites; j++ {
				data := []byte(strings.Repeat("concurrent write test\n", 1))
				_, err := fw.Write(data)
				errChan <- err
			}
		}(i)
	}

	// Collect results
	for i := 0; i < numGoroutines*numWrites; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent write error: %v", err)
		}
	}

	// Verify file exists and has content
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("Expected log file to exist after concurrent writes")
	}
}

func TestFileWriterImplementsInterfaces(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "interface.log")

	fw, err := NewFileWriter(logPath, 1024, 3, 7)
	if err != nil {
		t.Fatalf("Failed to create FileWriter: %v", err)
	}
	defer fw.Close()

	// Test that FileWriter implements required interfaces
	var _ Writer = fw
	var _ io.Writer = fw
}
