package main

import (
	"alpheus/kernel/internal/watchdog"

	"github.com/robfig/cron/v3"
)

func startWatchdog(s *server) (*cron.Cron, error) {
	return watchdog.Start(s.marketTimezone(), func(role, occurrenceID string) {
		s.fireSpineTick(role, occurrenceID)
	})
}

func (s *server) fireSpineTick(role, occurrenceID string) {
	payload := map[string]string{"role": role, "occurrence_id": occurrenceID}
	s.store.Event("spine_tick", payload)
	// The static agent-runtime wake target is retired. Kernel keeps the
	// deterministic schedule occurrence as an audit fact, but it cannot turn a
	// timer into a model request. A future Cortex Scheduler must admit its own
	// canonical objective and authority rather than impersonating UserRequest.
}
