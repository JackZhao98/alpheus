package watchdog

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func TestOccurrenceIDUsesScheduledSlot(t *testing.T) {
	tick := Tick{Role: "scout", Cron: "45 9-15 * * 1-5"}
	slot := time.Date(2026, time.July, 17, 16, 45, 0, 0, time.UTC)
	first := OccurrenceID(tick, slot)
	if first != OccurrenceID(tick, slot) {
		t.Fatal("same scheduled slot produced different occurrence ids")
	}
	if first == OccurrenceID(tick, slot.Add(time.Hour)) {
		t.Fatal("different scheduled slots produced the same occurrence id")
	}
	if first == OccurrenceID(Tick{Role: tick.Role, Cron: "0 10 * * 1-5"}, slot) {
		t.Fatal("different cron rules produced the same occurrence id")
	}
}

func TestSlotTrackingSchedulePreservesDeliveryOrder(t *testing.T) {
	schedule, err := cron.ParseStandard("45 9-15 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	tracked := &slotTrackingSchedule{schedule: schedule}
	after := time.Date(2026, time.July, 17, 9, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	first := tracked.Next(after)
	second := tracked.Next(first)
	if got := tracked.take(); !got.Equal(first) {
		t.Fatalf("first delivered slot=%s, want %s", got, first)
	}
	if got := tracked.take(); !got.Equal(second) {
		t.Fatalf("second delivered slot=%s, want %s", got, second)
	}
	if got := tracked.take(); !got.IsZero() {
		t.Fatalf("empty queue returned %s", got)
	}
}
