// Package logging configures the process-wide operational logger.
package logging

import (
	"io"
	"log/slog"
)

// Level controls the minimum severity emitted by New.
var Level slog.LevelVar

func New(out io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: &Level}))
}
