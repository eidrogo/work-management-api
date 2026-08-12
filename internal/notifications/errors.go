package notifications

import "errors"

var (
	ErrResourceNotFound = errors.New("notification not found")
	ErrLeaseLost        = errors.New("notification lease lost")
	ErrNotReplayable    = errors.New("notification is not replayable")
	ErrInvalidInput     = errors.New("invalid notification input")
)
