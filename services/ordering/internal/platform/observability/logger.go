package observability

import (
	"io"
	"log/slog"
)

// NewLogger creates the process logger. Production emits JSON while local
// development uses readable structured text.
func NewLogger(output io.Writer, environment string, level slog.Level) *slog.Logger {
	options := &slog.HandlerOptions{Level: level}
	if environment == "production" {
		return slog.New(slog.NewJSONHandler(output, options))
	}
	return slog.New(slog.NewTextHandler(output, options))
}
