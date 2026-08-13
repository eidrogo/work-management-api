package persistedtext

import (
	"errors"
	"strings"
)

var errNUL = errors.New("persisted text contains U+0000")

// Validate rejects U+0000, the one code point this persistence contract excludes.
func Validate(value string) error {
	if strings.IndexByte(value, 0) >= 0 {
		return errNUL
	}
	return nil
}
