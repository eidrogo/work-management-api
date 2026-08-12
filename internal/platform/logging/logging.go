package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

func New(level string, writer io.Writer) (*slog.Logger, error) {
	if writer == nil {
		return nil, fmt.Errorf("log writer is required")
	}
	var parsed slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		parsed = slog.LevelDebug
	case "info":
		parsed = slog.LevelInfo
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: parsed})), nil
}
