package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

func (server *Server) decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return &apiError{Status: http.StatusUnsupportedMediaType, Code: "UNSUPPORTED_MEDIA_TYPE", Message: "Content-Type must be application/json."}
	}
	r.Body = http.MaxBytesReader(w, r.Body, server.maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return invalid("The request body exceeds the configured size limit.", map[string]any{"maximum_bytes": server.maxBodyBytes})
		}
		if errors.Is(err, io.EOF) {
			return invalid("A JSON request body is required.", nil)
		}
		return invalid("The JSON request body is invalid.", nil)
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalid("The request body must contain exactly one JSON object.", nil)
	}
	return nil
}
func parseIfMatch(r *http.Request) (int64, error) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	if value == "" {
		return 0, nil
	}
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, invalid("If-Match must be a quoted positive work-item version.", map[string]any{"header": "If-Match"})
	}
	version, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || version <= 0 {
		return 0, invalid("If-Match must be a quoted positive work-item version.", map[string]any{"header": "If-Match"})
	}
	return version, nil
}
func parseLimit(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, invalid("limit must be an integer between 1 and 100.", map[string]any{"field": "limit"})
	}
	return limit, nil
}
func quoteETag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }
