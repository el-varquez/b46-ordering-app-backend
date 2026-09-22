package observability

import (
	"io"
	"log/slog"
)

func NewLogger(output io.Writer, environment string, level slog.Level) *slog.Logger {
	options := &slog.HandlerOptions{Level: level}
	if environment == "production" {
		return slog.New(slog.NewJSONHandler(output, options))
	}
	return slog.New(slog.NewTextHandler(output, options))
}
