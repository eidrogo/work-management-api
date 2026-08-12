package workitems

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/eidrogo/work-management-api/internal/platform/id"
)

type ListCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func EncodeListCursor(cursor ListCursor) (string, error) {
	if cursor.CreatedAt.IsZero() || !id.IsUUID(cursor.ID) {
		return "", fmt.Errorf("%w: cursor fields are invalid", ErrInvalidInput)
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}
func DecodeListCursor(value string) (ListCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return ListCursor{}, fmt.Errorf("%w: malformed cursor", ErrInvalidInput)
	}
	var cursor ListCursor
	if err = json.Unmarshal(payload, &cursor); err != nil {
		return ListCursor{}, fmt.Errorf("%w: malformed cursor payload", ErrInvalidInput)
	}
	if cursor.CreatedAt.IsZero() || !id.IsUUID(cursor.ID) {
		return ListCursor{}, fmt.Errorf("%w: cursor fields are invalid", ErrInvalidInput)
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return cursor, nil
}
func EncodeEventCursor(sequence int64) (string, error) {
	if sequence <= 0 {
		return "", fmt.Errorf("%w: event cursor must be positive", ErrInvalidInput)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(sequence, 10))), nil
}
func DecodeEventCursor(value string) (int64, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, fmt.Errorf("%w: malformed event cursor", ErrInvalidInput)
	}
	sequence, err := strconv.ParseInt(string(payload), 10, 64)
	if err != nil || sequence <= 0 {
		return 0, fmt.Errorf("%w: malformed event cursor", ErrInvalidInput)
	}
	return sequence, nil
}
