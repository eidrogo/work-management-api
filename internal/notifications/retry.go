package notifications

import (
	"errors"
	"time"
)

type SendError struct {
	err       error
	retryable bool
}

func (e *SendError) Error() string {
	if e == nil || e.err == nil {
		return "email delivery failed"
	}
	return e.err.Error()
}
func (e *SendError) Unwrap() error { return e.err }
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return &SendError{err: err, retryable: true}
}
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &SendError{err: err, retryable: false}
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
