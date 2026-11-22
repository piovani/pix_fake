package payment

import (
	"context"
	"log/slog"
	"time"
)

// Longer than ResolutionTTL, so a call that stopped after reserving can no longer be repeated when its money is released.
const orphanAfter = 10 * time.Minute

// ReleaseOrphans gives back money held for a payment that was never stored, because the process stopped between reserving
// and recording it. It returns how many reservations it released.
func (s *Service) ReleaseOrphans(ctx context.Context) (int, error) {
	held, err := s.accounts.HeldBefore(ctx, s.now().Add(-orphanAfter), 50)
	if err != nil {
		return 0, err
	}
	released := 0
	for _, r := range held {
		_, found, err := s.store.ByReservation(ctx, r.ID)
		if err != nil {
			return released, err
		}
		if found {
			continue
		}
		if err := s.accounts.Release(ctx, r.ID); err != nil {
			return released, err
		}
		slog.Warn("released orphan reservation", "reservation", r.ID, "reference", r.Reference)
		released++
	}
	return released, nil
}
