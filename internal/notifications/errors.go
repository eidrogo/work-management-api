package notifications

import "errors"

var (
	ErrResourceNotFound   = errors.New("notification not found")
	ErrLeaseLost          = errors.New("notification lease lost")
	ErrNotReplayable      = errors.New("notification is not replayable")
	ErrReplayLimitReached = errors.New("notification replay limit reached")
	ErrInvalidInput       = errors.New("invalid notification input")
)

type domainError struct {
	kind    error
	message string
	details map[string]any
}

func (err *domainError) Error() string { return err.message }
func (err *domainError) Unwrap() error { return err.kind }

func newInvalidInput(message string, details map[string]any) error {
	return &domainError{kind: ErrInvalidInput, message: message, details: details}
}

func ErrorContract(err error) (string, string, map[string]any) {
	var domain *domainError
	if errors.As(err, &domain) {
		return "INVALID_INPUT", domain.message, domain.details
	}
	if errors.Is(err, ErrInvalidInput) {
		return "INVALID_INPUT", "The request contains invalid input.", nil
	}
	return "INTERNAL_ERROR", "An internal error occurred.", nil
}
