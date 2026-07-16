package main

import (
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/watchdog"

	"github.com/robfig/cron/v3"
)

func startWatchdog(s *server) (*cron.Cron, error) {
	tz := config.Env("TZ_MARKET", "America/New_York")
	return watchdog.Start(tz, func(role string) {
		// TODO: POST agent-runtime /wake {role}. For now, audit only.
		s.store.Event("spine_tick", map[string]string{"role": role})
	})
}
