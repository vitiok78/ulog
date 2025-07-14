package ulog

import "testing"

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
