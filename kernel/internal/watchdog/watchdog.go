// Package watchdog: the schedule spine lives HERE, not in agent
// self-scheduling. Agents may self-schedule extra runs; the watchdog
// guarantees the spine fires and repairs missed heartbeats.
package watchdog

import (
	"time"
	_ "time/tzdata" // embed tz database so containers need no tzdata package

	"github.com/robfig/cron/v3"
)

type Tick struct {
	Role string
	Cron string // in market tz
}

var Spine = []Tick{
	{"desk_master", "30 8 * * 1-5"}, // pre-market plan
	{"desk_master", "12 9 * * 1-5"}, // opening decision — fixed, never self-scheduled
	{"scout", "45 9-15 * * 1-5"},
	{"position_manager", "*/15 9-15 * * 1-5"},
	{"desk_master", "0 10,14 * * 1-5"},
	{"desk_master", "30 12 * * 1-5"},
	{"position_manager", "45 15 * * 1-5"}, // pre-close cleanup
	{"coach", "35 16 * * 1-5"},            // daily retro
	{"coach", "0 10 * * 6"},               // weekly review
}

// Start fires each spine tick via the given callback (kernel logs an event
// and — TODO — POSTs agent-runtime /wake). Repair job: compare last heartbeat
// per role vs expectation, force-run if missed.
func Start(marketTZ string, fire func(role string)) (*cron.Cron, error) {
	loc, err := time.LoadLocation(marketTZ)
	if err != nil {
		return nil, err
	}
	c := cron.New(cron.WithLocation(loc))
	for _, t := range Spine {
		role := t.Role
		if _, err := c.AddFunc(t.Cron, func() { fire(role) }); err != nil {
			return nil, err
		}
	}
	c.Start()
	return c, nil
}
