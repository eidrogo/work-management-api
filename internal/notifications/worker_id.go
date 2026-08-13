package notifications

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxWorkerIDRunes = 255

func ValidateWorkerID(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("worker ID must be valid UTF-8")
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return "", fmt.Errorf("worker ID must not contain control characters")
		}
	}
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > MaxWorkerIDRunes {
		return "", fmt.Errorf("worker ID must contain between 1 and %d characters", MaxWorkerIDRunes)
	}
	return value, nil
}
