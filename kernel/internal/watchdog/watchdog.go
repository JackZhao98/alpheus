// Package watchdog: the schedule spine lives HERE, not in agent
// self-scheduling. Agents may self-schedule extra runs; the watchdog
// guarantees the spine fires and repairs missed heartbeats.
package watchdog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
	_ "time/tzdata" // embed tz database so containers need no tzdata package

	"github.com/robfig/cron/v3"
)

type slotTrackingSchedule struct {
	schedule cron.Schedule
	mu       sync.Mutex
	slots    []time.Time
}

func (s *slotTrackingSchedule) Next(after time.Time) time.Time {
	next := s.schedule.Next(after)
	s.mu.Lock()
	s.slots = append(s.slots, next)
	s.mu.Unlock()
	return next
}

func (s *slotTrackingSchedule) take() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.slots) == 0 {
		return time.Time{}
	}
	slot := s.slots[0]
	s.slots = s.slots[1:]
	return slot
}

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

// OccurrenceID is stable for one role, cron rule, and scheduled slot. Delayed
// delivery and retries therefore keep the same id instead of deriving a new
// one from wall-clock delivery time.
func OccurrenceID(tick Tick, scheduledSlot time.Time) string {
	digest := sha256.Sum256([]byte(tick.Cron))
	return fmt.Sprintf("%s:%s:%s", tick.Role,
		scheduledSlot.UTC().Format("20060102T150405Z"), hex.EncodeToString(digest[:4]))
}

// Start fires each spine tick via the given callback. Repair job: compare last
// heartbeat per role vs expectation, force-run if missed.
func Start(marketTZ string, fire func(role, occurrenceID string)) (*cron.Cron, error) {
	loc, err := time.LoadLocation(marketTZ)
	if err != nil {
		return nil, err
	}
	c := cron.New(cron.WithLocation(loc))
	for _, t := range Spine {
		tick := t
		schedule, err := cron.ParseStandard(tick.Cron)
		if err != nil {
			return nil, err
		}
		tracked := &slotTrackingSchedule{schedule: schedule}
		c.Schedule(tracked, cron.FuncJob(func() {
			scheduledSlot := tracked.take()
			if scheduledSlot.IsZero() {
				return
			}
			fire(tick.Role, OccurrenceID(tick, scheduledSlot))
		}))
	}
	c.Start()
	return c, nil
}
