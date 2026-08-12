package notifications

import (
	"errors"
	"testing"
	"time"
)

func TestRetryDelayAndClassification(t *testing.T) {
	t.Parallel()
	if got := RetryDelay(2*time.Second, 30*time.Second, 3, .5); got != 6*time.Second {
		t.Fatalf("got=%s", got)
	}
	if !IsRetryable(Retryable(errors.New("network"))) {
		t.Fatal("retryable misclassified")
	}
	if IsRetryable(Permanent(errors.New("recipient"))) {
		t.Fatal("permanent misclassified")
	}
}
