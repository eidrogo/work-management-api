package workitems

import "errors"

var (
	ErrPermissionDenied           = errors.New("permission denied")
	ErrResourceNotFound           = errors.New("resource not found")
	ErrInvalidInput               = errors.New("invalid input")
	ErrInvalidTransition          = errors.New("invalid work-item transition")
	ErrAssignmentConflict         = errors.New("assignment conflict")
	ErrConcurrentModification     = errors.New("concurrent modification")
	ErrAssigneeMustBeWorker       = errors.New("assignee must be a worker")
	ErrReassignmentReasonRequired = errors.New("reassignment reason is required")
	ErrCompletedImmutable         = errors.New("completed work item is immutable")
	ErrIdempotencyKeyRequired     = errors.New("idempotency key is required")
	ErrIdempotencyKeyReused       = errors.New("idempotency key reused with different request")
	ErrExpectedVersionRequired    = errors.New("expected version is required")
)

type DomainError struct {
	Kind    error
	Code    string
	Message string
	Details map[string]any
}

func (e *DomainError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Kind != nil {
		return e.Kind.Error()
	}
	return "domain error"
}
func (e *DomainError) Unwrap() error { return e.Kind }

func newError(kind error, code, message string, details map[string]any) error {
	return &DomainError{Kind: kind, Code: code, Message: message, Details: details}
}

func ErrorContract(err error) (string, string, map[string]any) {
	var domain *DomainError
	if errors.As(err, &domain) {
		return domain.Code, domain.Message, domain.Details
	}
	switch {
	case errors.Is(err, ErrPermissionDenied):
		return "PERMISSION_DENIED", "You are not allowed to perform this action.", nil
	case errors.Is(err, ErrResourceNotFound):
		return "RESOURCE_NOT_FOUND", "The requested resource was not found.", nil
	case errors.Is(err, ErrInvalidInput):
		return "INVALID_INPUT", "The request contains invalid input.", nil
	case errors.Is(err, ErrInvalidTransition):
		return "WORK_ITEM_INVALID_TRANSITION", "The requested work-item transition is not allowed.", nil
	case errors.Is(err, ErrAssignmentConflict):
		return "ASSIGNMENT_CONFLICT", "The work item cannot be assigned in its current state.", nil
	case errors.Is(err, ErrConcurrentModification):
		return "CONCURRENT_MODIFICATION", "The work item changed after it was read.", nil
	case errors.Is(err, ErrIdempotencyKeyReused):
		return "IDEMPOTENCY_KEY_REUSED", "The idempotency key was already used with a different request.", nil
	default:
		return "INTERNAL_ERROR", "An internal error occurred.", nil
	}
}
