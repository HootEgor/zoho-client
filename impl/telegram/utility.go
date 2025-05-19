package telegram

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

func removeMarkup(input string) string {
	reservedChars := "\\`*_|"

	sanitized := ""
	for _, char := range input {
		if !strings.ContainsRune(reservedChars, char) {
			sanitized += string(char)
		}
	}

	return sanitized
}

func sanitize(input string) string {
	// Define a list of reserved characters that need to be escaped
	reservedChars := "\\`*_{}[]()#+-.!|"

	// Loop through each character in the input string
	sanitized := ""
	for _, char := range input {
		// Check if the character is reserved
		if strings.ContainsRune(reservedChars, char) {
			// Escape the character with a backslash
			sanitized += "\\" + string(char)
		} else {
			// Add the character to the sanitized string
			sanitized += string(char)
		}
	}

	return sanitized
}

func generatePinCode() string {
	rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%06d", rand.Intn(1000000))
}
