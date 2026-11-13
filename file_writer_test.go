package ulog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
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

	// Write data that will trigger rotation. An empty file is never rotated,
	// so the first write only fills the file and the second one rotates it.
	largeData := []byte(strings.Repeat("This is a log entry that will cause rotation\n", 3))
	for i := 0; i < 2; i++ {
		if _, err := fw.Write(largeData); err != nil {
			t.Errorf("Unexpected write error: %v", err)
		}
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

// TestFileWriterCleanOldBackups checks maxAge on pre-existing archives. Before
// v1.0.4 it only asserted "at most 3 archives left"; every archive here is
// older than maxAge, so age pruning removed them all and hid that the
// maxBackups pass deleted the newest archives instead of the oldest.
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
		writeArchive(t, filepath.Join(tempDir, backup), "old backup content", time.Now().Add(-10*24*time.Hour))
	}

	// Create FileWriter with maxBackups=3 and maxAge=7 days
	fw, err := NewFileWriter(logPath, 50, 3, 7)
	if err != nil {
		t.Fatalf("Failed to create FileWriter: %v", err)
	}
	defer fw.Close()

	// Fill the file, then trigger rotation which should clean old backups
	largeData := []byte(strings.Repeat("Data to trigger rotation and cleanup\n", 3))
	for i := 0; i < 2; i++ {
		if _, err := fw.Write(largeData); err != nil {
			t.Errorf("Unexpected write error: %v", err)
		}
	}

	// Only the archive made just now is younger than maxAge.
	archives := listArchives(t, tempDir, "cleanup.log")
	if len(archives) != 1 {
		t.Fatalf("Expected 1 archive (the new one), found %v", archives)
	}
	for _, old := range oldBackups {
		if archives[0] == old {
			t.Errorf("Archive %s is older than maxAge and should be removed", old)
		}
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

// testClockStart is the fake time tests start from. It is the real time,
// because archives are ranked and aged by their real modification times.
var testClockStart = time.Now().Truncate(time.Second)

// newTestWriter returns a FileWriter on <tempdir>/app.log with a fake clock.
// Assign through the returned pointer to move the clock.
func newTestWriter(t *testing.T, maxSize int64, maxBackups, maxAge int) (*FileWriter, string, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	fw, err := NewFileWriter(filepath.Join(dir, "app.log"), maxSize, maxBackups, maxAge)
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	t.Cleanup(func() { fw.Close() })

	clock := testClockStart
	fw.now = func() time.Time { return clock }
	return fw, dir, &clock
}

func mustWrite(t *testing.T, fw *FileWriter, s string) {
	t.Helper()
	n, err := fw.Write([]byte(s))
	if err != nil {
		t.Fatalf("Write(%q): %v", s, err)
	}
	if n != len(s) {
		t.Fatalf("Write(%q) wrote %d bytes, want %d", s, n, len(s))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// writeArchive creates a file with the given modification time.
func writeArchive(t *testing.T, path, content string, modTime time.Time) {
	t.Helper()
	writeFile(t, path, content)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes(%s): %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(data)
}

// listArchives returns the sorted names in dir that cleanOldBackups treats
// as archives of base.
func listArchives(t *testing.T, dir, base string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(entry.Name(), base+".")
		if !ok || entry.IsDir() {
			continue
		}
		if _, _, ok := parseArchiveSuffix(suffix); ok {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func archiveName(base string, ts time.Time) string {
	return base + "." + ts.Format(archiveLayout)
}

func skipIfPermissionsIgnored(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block writes on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
}

func TestFileWriterMaxBackupsKeepsNewest(t *testing.T) {
	fw, dir, clock := newTestWriter(t, 10, 3, 0)

	// Each record fills the file, so every write after the first rotates.
	// The archive holding record k is made at testClockStart + (k+1)s.
	for i := 0; i < 8; i++ {
		mustWrite(t, fw, fmt.Sprintf("record %02d\n", i))
		*clock = clock.Add(time.Second)
	}

	var want []string
	for k := 4; k <= 6; k++ {
		want = append(want, archiveName("app.log", testClockStart.Add(time.Duration(k+1)*time.Second)))
	}
	got := listArchives(t, dir, "app.log")
	if !slices.Equal(got, want) {
		t.Fatalf("archives = %v, want the newest three %v", got, want)
	}
	for i, name := range want {
		if content, wantContent := readFile(t, filepath.Join(dir, name)), fmt.Sprintf("record %02d\n", i+4); content != wantContent {
			t.Errorf("%s holds %q, want %q", name, content, wantContent)
		}
	}
	if content := readFile(t, filepath.Join(dir, "app.log")); content != "record 07\n" {
		t.Errorf("active file holds %q, want %q", content, "record 07\n")
	}
}

func TestFileWriterForeignFilesSurvive(t *testing.T) {
	fw, dir, clock := newTestWriter(t, 10, 2, 1)

	foreign := []string{
		"app.log.keep-me",
		"app.log.gz",
		"app.log.20200101-000000.bak",
		"app.log.20201301-000000", // month 13
		"app.log.20200101-00000",  // one digit short
		"app.log.20200101-000000.0",
		"app.log.20200101-000000.01",
		"app.log.20200101-000000.+1",
		"app.logx.20200101-000000",
	}
	for _, name := range foreign {
		writeFile(t, filepath.Join(dir, name), "not an archive")
	}
	// A directory with an archive name is not an archive either.
	if err := os.Mkdir(filepath.Join(dir, "app.log.20200102-000000"), 0755); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		mustWrite(t, fw, fmt.Sprintf("record %02d\n", i))
		*clock = clock.Add(time.Second)
	}

	for _, name := range append(foreign, "app.log.20200102-000000") {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("foreign file %s was removed: %v", name, err)
		}
	}
	// Foreign files do not count against maxBackups.
	if got := listArchives(t, dir, "app.log"); len(got) != 2 {
		t.Errorf("archives = %v, want 2", got)
	}
}

func TestFileWriterMaxAgeAndMaxBackups(t *testing.T) {
	ages := []time.Duration{
		10 * 24 * time.Hour,
		8 * 24 * time.Hour,
		4 * time.Hour,
		3 * time.Hour,
		2 * time.Hour,
		1 * time.Hour,
	}

	tests := []struct {
		name       string
		maxBackups int
		want       []time.Duration // ages of the surviving archives; 0 = the new one
	}{
		{"both limits", 3, []time.Duration{0, time.Hour, 2 * time.Hour}},
		{"age only", 0, []time.Duration{0, time.Hour, 2 * time.Hour, 3 * time.Hour, 4 * time.Hour}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fw, dir, _ := newTestWriter(t, 10, tt.maxBackups, 7)
			for _, age := range ages {
				ts := testClockStart.Add(-age)
				writeArchive(t, filepath.Join(dir, archiveName("app.log", ts)), "old", ts)
			}

			mustWrite(t, fw, "record 00\n")
			mustWrite(t, fw, "record 01\n") // rotates at testClockStart

			var want []string
			for _, age := range tt.want {
				want = append(want, archiveName("app.log", testClockStart.Add(-age)))
			}
			sort.Strings(want)
			if got := listArchives(t, dir, "app.log"); !slices.Equal(got, want) {
				t.Errorf("archives = %v, want %v", got, want)
			}
		})
	}
}

func TestFileWriterSameSecondRotations(t *testing.T) {
	fw, dir, _ := newTestWriter(t, 10, 0, 0)

	// The clock does not move: three rotations within one second.
	for i := 0; i < 4; i++ {
		mustWrite(t, fw, fmt.Sprintf("record %02d\n", i))
	}

	base := archiveName("app.log", testClockStart)
	want := []string{base, base + ".1", base + ".2"}
	if got := listArchives(t, dir, "app.log"); !slices.Equal(got, want) {
		t.Fatalf("archives = %v, want %v", got, want)
	}
	for i, name := range want {
		if content, wantContent := readFile(t, filepath.Join(dir, name)), fmt.Sprintf("record %02d\n", i); content != wantContent {
			t.Errorf("%s holds %q, want %q", name, content, wantContent)
		}
	}

	// Within one second the counter orders the archives.
	fw.maxBackups = 2
	if err := fw.cleanOldBackups(); err != nil {
		t.Fatalf("cleanOldBackups: %v", err)
	}
	if got, want := listArchives(t, dir, "app.log"), []string{base + ".1", base + ".2"}; !slices.Equal(got, want) {
		t.Errorf("after cleanup archives = %v, want %v", got, want)
	}
}

// TestFileWriterSameSecondRotationsWithRetention keeps retention on while many
// rotations happen in one second. Retention frees the lower names; reusing
// them would rank the newest archive below older ones and delete it.
func TestFileWriterSameSecondRotationsWithRetention(t *testing.T) {
	fw, dir, _ := newTestWriter(t, 10, 1, 0)
	base := archiveName("app.log", testClockStart)

	mustWrite(t, fw, "record 00\n")
	for k := 1; k <= 5; k++ {
		mustWrite(t, fw, fmt.Sprintf("record %02d\n", k)) // archives record k-1

		got := listArchives(t, dir, "app.log")
		if len(got) != 1 {
			t.Fatalf("after rotation %d: archives = %v, want 1", k, got)
		}
		if content, want := readFile(t, filepath.Join(dir, got[0])), fmt.Sprintf("record %02d\n", k-1); content != want {
			t.Fatalf("after rotation %d: %s holds %q, want the newest %q", k, got[0], content, want)
		}
	}
	if got := listArchives(t, dir, "app.log"); got[0] != base+".4" {
		t.Errorf("survivor = %s, want %s", got[0], base+".4")
	}
}

// TestFileWriterWallClockGoesBack covers a DST fall-back (or a time zone
// change): the second archive gets an earlier name than the first although it
// is newer. Ranking by name would delete it.
func TestFileWriterWallClockGoesBack(t *testing.T) {
	fw, dir, clock := newTestWriter(t, 10, 1, 0)

	mustWrite(t, fw, "record 00\n")
	mustWrite(t, fw, "record 01\n") // archive named 03:59-ish, holds record 00
	first := filepath.Join(dir, archiveName("app.log", testClockStart))
	if err := os.Chtimes(first, time.Now().Add(-time.Minute), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	*clock = testClockStart.Add(-59 * time.Minute) // wall clock jumps back
	mustWrite(t, fw, "record 02\n")                // archive holds record 01

	got := listArchives(t, dir, "app.log")
	want := archiveName("app.log", *clock)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("archives = %v, want only the newest %s", got, want)
	}
	if content := readFile(t, filepath.Join(dir, got[0])); content != "record 01\n" {
		t.Errorf("survivor holds %q, want %q", content, "record 01\n")
	}
}

func TestFileWriterCleanOldBackupsReportsRemoveError(t *testing.T) {
	skipIfPermissionsIgnored(t)
	fw, dir, _ := newTestWriter(t, 0, 1, 0)
	for i := 1; i <= 3; i++ {
		writeFile(t, filepath.Join(dir, archiveName("app.log", testClockStart.Add(-time.Duration(i)*time.Hour))), "old")
	}

	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })

	if err := fw.cleanOldBackups(); err == nil {
		t.Error("cleanOldBackups returned nil, want the removal error")
	}
}

func TestFileWriterOversizedRecordOnEmptyFile(t *testing.T) {
	fw, dir, _ := newTestWriter(t, 10, 0, 0)

	big := strings.Repeat("x", 50) + "\n"
	mustWrite(t, fw, big)
	if got := listArchives(t, dir, "app.log"); len(got) != 0 {
		t.Fatalf("archives = %v, want none: an empty file must not be rotated", got)
	}

	mustWrite(t, fw, "next\n")
	got := listArchives(t, dir, "app.log")
	if len(got) != 1 || readFile(t, filepath.Join(dir, got[0])) != big {
		t.Errorf("archives = %v, want one holding the big record", got)
	}
}

func TestFileWriterRenameFailureKeepsWriting(t *testing.T) {
	fw, dir, _ := newTestWriter(t, 10, 0, 0)
	fw.rename = func(string, string) error { return errors.New("injected rename failure") }

	mustWrite(t, fw, "record 00\n")
	mustWrite(t, fw, "record 01\n") // rotation fails, record still written
	mustWrite(t, fw, "record 02\n")

	if got := listArchives(t, dir, "app.log"); len(got) != 0 {
		t.Fatalf("archives = %v, want none", got)
	}
	active := filepath.Join(dir, "app.log")
	if got, want := readFile(t, active), "record 00\nrecord 01\nrecord 02\n"; got != want {
		t.Fatalf("active file holds %q, want %q", got, want)
	}

	// Once rename works again, rotation resumes.
	fw.rename = os.Rename
	mustWrite(t, fw, "record 03\n")
	got := listArchives(t, dir, "app.log")
	if len(got) != 1 || readFile(t, filepath.Join(dir, got[0])) != "record 00\nrecord 01\nrecord 02\n" {
		t.Errorf("archives = %v, want one holding records 00-02", got)
	}
	if got := readFile(t, active); got != "record 03\n" {
		t.Errorf("active file holds %q, want %q", got, "record 03\n")
	}
}

func TestFileWriterRenameFailureReadOnlyDir(t *testing.T) {
	skipIfPermissionsIgnored(t)
	fw, dir, _ := newTestWriter(t, 10, 3, 0)

	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })

	mustWrite(t, fw, "record 00\n")
	mustWrite(t, fw, "record 01\n") // the real rename fails
	mustWrite(t, fw, "record 02\n")

	if got, want := readFile(t, filepath.Join(dir, "app.log")), "record 00\nrecord 01\nrecord 02\n"; got != want {
		t.Errorf("active file holds %q, want %q", got, want)
	}
}

func TestFileWriterReopenFailureRecovers(t *testing.T) {
	fw, dir, _ := newTestWriter(t, 10, 0, 0)

	mustWrite(t, fw, "record 00\n")

	failures := 1
	fw.openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if failures > 0 {
			failures--
			return nil, errors.New("injected open failure")
		}
		return os.OpenFile(name, flag, perm)
	}

	// Rename succeeds, the new file cannot be opened: this record is lost.
	if _, err := fw.Write([]byte("record 01\n")); err == nil {
		t.Fatal("Write returned nil, want the open error")
	}
	if fw.file != nil {
		t.Fatal("writer kept a closed file handle")
	}

	// The next write opens the file again.
	mustWrite(t, fw, "record 02\n")
	if got := readFile(t, filepath.Join(dir, "app.log")); got != "record 02\n" {
		t.Errorf("active file holds %q, want %q", got, "record 02\n")
	}
	got := listArchives(t, dir, "app.log")
	if len(got) != 1 || readFile(t, filepath.Join(dir, got[0])) != "record 00\n" {
		t.Errorf("archives = %v, want one holding record 00", got)
	}
}

func TestFileWriterCloseFailureDuringRotation(t *testing.T) {
	fw, dir, _ := newTestWriter(t, 10, 0, 0)
	fw.closeFile = func(f *os.File) error {
		f.Close()
		return errors.New("injected close failure")
	}

	mustWrite(t, fw, "record 00\n")
	mustWrite(t, fw, "record 01\n")

	if got := readFile(t, filepath.Join(dir, "app.log")); got != "record 01\n" {
		t.Errorf("active file holds %q, want %q", got, "record 01\n")
	}
	got := listArchives(t, dir, "app.log")
	if len(got) != 1 || readFile(t, filepath.Join(dir, got[0])) != "record 00\n" {
		t.Errorf("archives = %v, want one holding record 00", got)
	}
}

func TestFileWriterWriteAfterClose(t *testing.T) {
	fw, _, _ := newTestWriter(t, 0, 0, 0)
	if err := fw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := fw.Write([]byte("late\n")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Write after Close = %v, want os.ErrClosed", err)
	}
}
