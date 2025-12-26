package bot

import (
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "plain text no escaping",
			input:    "Hello World",
			expected: "Hello World",
		},
		{
			name:     "underscore escaped",
			input:    "hello_world",
			expected: "hello\\_world",
		},
		{
			name:     "curly braces escaped",
			input:    "code{block}",
			expected: "code\\{block\\}",
		},
		{
			name:     "hash escaped",
			input:    "#hashtag",
			expected: "\\#hashtag",
		},
		{
			name:     "plus escaped",
			input:    "1+1=2",
			expected: "1\\+1\\=2",
		},
		{
			name:     "dash escaped",
			input:    "foo-bar",
			expected: "foo\\-bar",
		},
		{
			name:     "dot escaped",
			input:    "file.txt",
			expected: "file\\.txt",
		},
		{
			name:     "exclamation escaped",
			input:    "Hello!",
			expected: "Hello\\!",
		},
		{
			name:     "pipe escaped",
			input:    "a|b",
			expected: "a\\|b",
		},
		{
			name:     "parentheses escaped",
			input:    "func(arg)",
			expected: "func\\(arg\\)",
		},
		{
			name:     "square brackets escaped",
			input:    "array[0]",
			expected: "array\\[0\\]",
		},
		{
			name:     "equals escaped",
			input:    "x=5",
			expected: "x\\=5",
		},
		{
			name:     "asterisk escaped",
			input:    "bold*text*",
			expected: "bold\\*text\\*",
		},
		{
			name:     "backslash escaped",
			input:    "path\\to\\file",
			expected: "path\\\\to\\\\file",
		},
		{
			name:     "all reserved chars",
			input:    "\\_{}#+-.!|()[]=*",
			expected: "\\\\\\_\\{\\}\\#\\+\\-\\.\\!\\|\\(\\)\\[\\]\\=\\*",
		},
		{
			name:     "mixed content",
			input:    "Order #123: Total = 100.50 PLN",
			expected: "Order \\#123: Total \\= 100\\.50 PLN",
		},
		{
			name:     "unicode preserved",
			input:    "Привет мир",
			expected: "Привет мир",
		},
		{
			name:     "emoji preserved",
			input:    "Hello 👋 World 🌍",
			expected: "Hello 👋 World 🌍",
		},
		{
			name:     "typical log message",
			input:    "[ERROR] Failed to process order #123: connection timeout (retry=3)",
			expected: "\\[ERROR\\] Failed to process order \\#123: connection timeout \\(retry\\=3\\)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Sanitize(tt.input)
			if result != tt.expected {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitize_NoDoubleEscape(t *testing.T) {
	// Test that already escaped content gets escaped again (as expected)
	// This is correct behavior - we escape the backslash
	input := "already\\_escaped"
	result := Sanitize(input)

	// Should escape both the backslash and the underscore
	if !strings.Contains(result, "\\\\") {
		t.Error("Backslash should be escaped")
	}
}

func TestSanitize_ReservedCharsComplete(t *testing.T) {
	// The reserved chars string from the function: \\_{}#+-.!|()[]=*
	reservedChars := "\\_{}#+-.!|()[]=*"

	for _, char := range reservedChars {
		input := string(char)
		result := Sanitize(input)

		// Each reserved char should be escaped (prefixed with backslash)
		if result != "\\"+input {
			t.Errorf("Character %q should be escaped, got %q, want %q", input, result, "\\"+input)
		}
	}
}
