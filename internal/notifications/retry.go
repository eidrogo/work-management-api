package notifications

import (
	"errors"
	"time"
)

type SendError struct {
	cause     error
	code      FailureCode
	retryable bool
}

func (e *SendError) Error() string {
	if e == nil {
		return SafeFailureMessage(FailureDelivery)
	}
	return SafeFailureMessage(e.code)
}
func (e *SendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
func Retryable(code FailureCode, cause error) error {
	if cause == nil {
		return nil
	}
	return &SendError{cause: cause, code: normalizedFailureCode(code), retryable: true}
}
func Permanent(code FailureCode, cause error) error {
	if cause == nil {
		return nil
	}
	return &SendError{cause: cause, code: normalizedFailureCode(code), retryable: false}
}
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var deliveryError *SendError
	if errors.As(err, &deliveryError) {
		return deliveryError.retryable
	}
	return true
}
func FailureCodeOf(err error) FailureCode {
	var sendError *SendError
	if errors.As(err, &sendError) && sendError != nil {
		return normalizedFailureCode(sendError.code)
	}
	return FailureDelivery
}
func normalizedFailureCode(code FailureCode) FailureCode {
	if code.Valid() {
		return code
	}
	return FailureDelivery
}
func (code FailureCode) Valid() bool {
	switch code {
	case FailureNotificationPayloadInvalid,
		FailureSMTPConfiguration,
		FailureSMTPConnection,
		FailureSMTPTimeout,
		FailureSMTPTLS,
		FailureSMTPAuthentication,
		FailureSMTPRecipientRejected,
		FailureSMTPMessageRejected,
		FailureSMTPTemporary,
		FailureSMTPProtocol,
		FailureDelivery,
		FailureLeaseExpired,
		FailureRetryLimit:
		return true
	default:
		return false
	}
}
func SafeFailureMessage(code FailureCode) string {
	switch normalizedFailureCode(code) {
	case FailureNotificationPayloadInvalid:
		return "The notification payload is invalid."
	case FailureSMTPConfiguration:
		return "The email service is not configured correctly."
	case FailureSMTPConnection:
		return "The email service could not be reached."
	case FailureSMTPTimeout:
		return "The email service timed out."
	case FailureSMTPTLS:
		return "A secure connection to the email service could not be established."
	case FailureSMTPAuthentication:
		return "The email service rejected authentication."
	case FailureSMTPRecipientRejected:
		return "The email service rejected the recipient."
	case FailureSMTPMessageRejected:
		return "The email service rejected the message."
	case FailureSMTPTemporary:
		return "The email service temporarily could not accept the message."
	case FailureSMTPProtocol:
		return "The email service returned an unexpected response."
	case FailureLeaseExpired:
		return "The delivery lease expired before completion."
	case FailureRetryLimit:
		return "The notification retry limit was exhausted."
	default:
		return "The notification could not be delivered."
	}
}
func RetryDelay(base, maximum time.Duration, attempt int, random float64) time.Duration {
	if base <= 0 || maximum <= 0 {
		return 0
	}
	if attempt < 1 {
		attempt = 1
	}
	if random < 0 {
		random = 0
	}
	if random > 1 {
		random = 1
	}
	exponential := base
	for index := 1; index < attempt && exponential < maximum; index++ {
		if exponential > maximum/2 {
			exponential = maximum
			break
		}
		exponential *= 2
	}
	if exponential > maximum {
		exponential = maximum
	}
	half := exponential / 2
	return half + time.Duration(random*float64(exponential-half))
}
