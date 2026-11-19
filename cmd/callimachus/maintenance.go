package main

import (
	"context"
	"log/slog"
	"time"
)

func (s *Server) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	s.maintainLogs(ctx, ticker.C)
}

// Passing ticks separately lets lifecycle tests advance time without sleeping.
func (s *Server) maintainLogs(ctx context.Context, ticks <-chan time.Time) {
	for {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.scanLibrary(ctx)
		if err := s.db.PruneLogs(ctx, s.maintenance.LogRetentionDays); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "log retention cleanup failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
