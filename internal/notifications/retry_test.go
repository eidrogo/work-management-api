package notifications

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRetryDelayAndClassification(t *testing.T) {
	t.Parallel()
	if got := RetryDelay(2*time.Second, 30*time.Second, 3, .5); got != 6*time.Second {
		t.Fatalf("got=%s", got)
	}
	if !IsRetryable(Retryable(FailureSMTPConnection, errors.New("network"))) {
		t.Fatal("retryable misclassified")
	}
	if IsRetryable(Permanent(FailureSMTPRecipientRejected, errors.New("recipient"))) {
		t.Fatal("permanent misclassified")
	}
}

func TestSendErrorExposesOnlyAllowlistedFailure(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("provider-secret user@example.test")

	typed := Retryable(FailureSMTPConnection, sentinel)
	if got := typed.Error(); got != SafeFailureMessage(FailureSMTPConnection) {
		t.Fatalf("typed error=%q", got)
	}
	if strings.Contains(typed.Error(), "provider-secret") || !errors.Is(typed, sentinel) {
		t.Fatalf("typed error leaked its cause or lost unwrap semantics: %q", typed.Error())
	}
	if got := FailureCodeOf(typed); got != FailureSMTPConnection {
		t.Fatalf("typed code=%q", got)
	}

	raw := sentinel
	if got := FailureCodeOf(raw); got != FailureDelivery {
		t.Fatalf("raw code=%q", got)
	}
	if got := SafeFailureMessage(FailureCodeOf(raw)); got != "The notification could not be delivered." {
		t.Fatalf("raw safe message=%q", got)
	}

	invalid := Retryable(FailureCode("PROVIDER_SECRET"), sentinel)
	if got := FailureCodeOf(invalid); got != FailureDelivery {
		t.Fatalf("invalid code=%q", got)
	}
	if strings.Contains(invalid.Error(), "provider-secret") {
		t.Fatalf("invalid typed error leaked its cause: %q", invalid.Error())
	}
}

func TestLeaseTokenNeverSerializes(t *testing.T) {
	t.Parallel()
	token := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	encoded, err := json.Marshal(Delivery{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", LeaseToken: token})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "lease_token") || strings.Contains(string(encoded), token) {
		t.Fatalf("delivery JSON exposes lease token: %s", encoded)
	}
}
