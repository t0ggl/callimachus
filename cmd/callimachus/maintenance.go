package main

import (
	"context"
	"log"
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
		if _, err := s.scanLibrary(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[library] scan failed: %v", err)
		}
		if err := s.db.PruneLogs(ctx, s.maintenance.LogRetentionDays); err != nil && ctx.Err() == nil {
			log.Printf("[maintenance] log retention cleanup failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
