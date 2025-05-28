package util

import (
	"fmt"
	"regexp"
)

func ValidateEmail(email string) (string, error) {
	// Regular expression to validate email format
	const emailRegex = `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	re := regexp.MustCompile(emailRegex)
	if !re.MatchString(email) {
		return "", fmt.Errorf("invalid email format: %s", email)
	}
	return email, nil
}
