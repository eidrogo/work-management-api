package notifications

import (
	"context"
	"encoding/json"
	"time"
)

type Status string

const (
	StatusPending    Status = "PENDING"
	StatusProcessing Status = "PROCESSING"
	StatusSent       Status = "SENT"
	StatusFailed     Status = "FAILED"
)

type Outcome string

const (
	OutcomeSent           Outcome = "SENT"
	OutcomeRetryScheduled Outcome = "RETRY_SCHEDULED"
	OutcomeFailed         Outcome = "FAILED"
	OutcomeAbandoned      Outcome = "ABANDONED"
)

type FailureCode string

const (
	FailureNotificationPayloadInvalid FailureCode = "NOTIFICATION_PAYLOAD_INVALID"
	FailureSMTPConfiguration          FailureCode = "SMTP_CONFIGURATION_ERROR"
	FailureSMTPConnection             FailureCode = "SMTP_CONNECTION_FAILED"
	FailureSMTPTimeout                FailureCode = "SMTP_TIMEOUT"
	FailureSMTPTLS                    FailureCode = "SMTP_TLS_FAILED"
	FailureSMTPAuthentication         FailureCode = "SMTP_AUTHENTICATION_FAILED"
	FailureSMTPRecipientRejected      FailureCode = "SMTP_RECIPIENT_REJECTED"
	FailureSMTPMessageRejected        FailureCode = "SMTP_MESSAGE_REJECTED"
	FailureSMTPTemporary              FailureCode = "SMTP_TEMPORARY_FAILURE"
	FailureSMTPProtocol               FailureCode = "SMTP_PROTOCOL_ERROR"
	FailureDelivery                   FailureCode = "DELIVERY_FAILED"
	FailureLeaseExpired               FailureCode = "LEASE_EXPIRED"
	FailureRetryLimit                 FailureCode = "RETRY_LIMIT_EXHAUSTED"
)

type ReplayChannel string

const (
	ReplayChannelHTTP ReplayChannel = "HTTP"
	ReplayChannelCLI  ReplayChannel = "CLI"
	MaxDeliveryCycles               = 10
)

type ReplayEvent struct {
	Sequence      int64         `json:"sequence"`
	DeliveryCycle int           `json:"delivery_cycle"`
	ActorID       string        `json:"actor_id"`
	RequestID     string        `json:"request_id"`
	Channel       ReplayChannel `json:"channel"`
	Reason        string        `json:"reason"`
	OccurredAt    time.Time     `json:"occurred_at"`
}

type Attempt struct {
	Sequence      int64       `json:"sequence"`
	AttemptNumber int         `json:"attempt_number"`
	WorkerID      string      `json:"worker_id"`
	Outcome       Outcome     `json:"outcome"`
	ErrorCode     FailureCode `json:"error_code,omitempty"`
	ErrorMessage  string      `json:"error_message,omitempty"`
	StartedAt     time.Time   `json:"started_at"`
	FinishedAt    time.Time   `json:"finished_at"`
}

type Delivery struct {
	ID                string          `json:"id"`
	WorkItemID        string          `json:"work_item_id"`
	AssignmentEventID int64           `json:"assignment_event_sequence"`
	RecipientEmail    string          `json:"recipient_email"`
	RecipientName     string          `json:"recipient_name"`
	EventType         string          `json:"event_type"`
	Payload           json.RawMessage `json:"payload"`
	Status            Status          `json:"status"`
	AttemptCount      int             `json:"attempt_count"`
	DeliveryCycle     int             `json:"delivery_cycle"`
	CycleAttemptCount int             `json:"cycle_attempt_count"`
	NextAttemptAt     *time.Time      `json:"next_attempt_at,omitempty"`
	LastErrorCode     FailureCode     `json:"last_error_code,omitempty"`
	LastError         string          `json:"last_error,omitempty"`
	MessageID         string          `json:"message_id"`
	ProviderMessageID string          `json:"provider_message_id,omitempty"`
	LockedBy          string          `json:"locked_by,omitempty"`
	LeaseToken        string          `json:"-"`
	LockedUntil       *time.Time      `json:"locked_until,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	LastAttemptAt     *time.Time      `json:"last_attempt_at,omitempty"`
	SentAt            *time.Time      `json:"sent_at,omitempty"`
	DeduplicationKey  string          `json:"deduplication_key"`
	Attempts          []Attempt       `json:"attempts,omitempty"`
	Replays           []ReplayEvent   `json:"replays,omitempty"`
}

type Message struct {
	To, ToName, Subject, TextBody, MessageID string
}

type Sender interface {
	Send(context.Context, Message) error
}
