package notifications

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/eidrogo/work-management-api/internal/platform/id"
)

type ListCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func EncodeListCursor(cursor ListCursor) (string, error) {
	if cursor.CreatedAt.IsZero() || !id.IsUUID(cursor.ID) {
		return "", fmt.Errorf("%w: invalid cursor fields", ErrInvalidInput)
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
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
		return ListCursor{}, fmt.Errorf("%w: malformed cursor", ErrInvalidInput)
	}
	if cursor.CreatedAt.IsZero() || !id.IsUUID(cursor.ID) {
		return ListCursor{}, fmt.Errorf("%w: invalid cursor fields", ErrInvalidInput)
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return cursor, nil
}
