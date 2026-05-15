package stats

import (
	"sync"
	"testing"
	"time"
)

// fakeClock returns the value of *t each time it's called. Callers advance
// time by writing to *t.
func fakeClock(t *time.Time) Clock {
	return func() time.Time { return *t }
}

func TestSnapshotEmpty(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	s := NewWithClock(fakeClock(&now))

	snap := s.Snapshot()
	if !snap.StartedAt.Equal(now) {
		t.Errorf("expected StartedAt=%s, got %s", now, snap.StartedAt)
	}
	if snap.UptimeSeconds != 0 {
		t.Errorf("expected uptime=0, got %v", snap.UptimeSeconds)
	}
	if len(snap.Lifetime) != 0 || len(snap.LastMinute) != 0 || len(snap.LastHour) != 0 || len(snap.LastDay) != 0 {
		t.Errorf("expected all maps empty, got %+v", snap)
	}
}

func TestRecordIncrementsLifetimeAndWindows(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	s := NewWithClock(fakeClock(&now))

	s.Record("ringing")
	s.Record("ringing")
	s.Record("answered")

	snap := s.Snapshot()
	if snap.Lifetime["ringing"] != 2 || snap.Lifetime["answered"] != 1 {
		t.Errorf("lifetime wrong: %+v", snap.Lifetime)
	}
	if snap.LastMinute["ringing"] != 2 || snap.LastMinute["answered"] != 1 {
		t.Errorf("last_minute wrong: %+v", snap.LastMinute)
	}
	if snap.LastHour["ringing"] != 2 || snap.LastDay["ringing"] != 2 {
		t.Errorf("last_hour/day wrong: %+v / %+v", snap.LastHour, snap.LastDay)
	}
}

func TestRollingWindows(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	s := NewWithClock(fakeClock(&now))

	s.Record("hungup") // t=0

	now = now.Add(90 * time.Second) // 1m 30s later -> out of last_minute
	s.Record("hungup")

	now = now.Add(70 * time.Minute) // 70m past second record -> first event also outside last_hour
	s.Record("hungup")

	snap := s.Snapshot()
	// last_minute: only the most recent
	if snap.LastMinute["hungup"] != 1 {
		t.Errorf("expected last_minute=1, got %d", snap.LastMinute["hungup"])
	}
	// last_hour: only the most recent (others are >60min old)
	if snap.LastHour["hungup"] != 1 {
		t.Errorf("expected last_hour=1, got %d", snap.LastHour["hungup"])
	}
	// last_day: all three (well within 24h)
	if snap.LastDay["hungup"] != 3 {
		t.Errorf("expected last_day=3, got %d", snap.LastDay["hungup"])
	}
	if snap.Lifetime["hungup"] != 3 {
		t.Errorf("expected lifetime=3, got %d", snap.Lifetime["hungup"])
	}
}

func TestBucketsExpireAfter24h(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	s := NewWithClock(fakeClock(&now))

	s.Record("hungup")

	// Jump 25 hours forward: the old bucket should fall outside last_day
	now = now.Add(25 * time.Hour)

	snap := s.Snapshot()
	if snap.LastDay["hungup"] != 0 {
		t.Errorf("expected old event to age out of last_day, got %d", snap.LastDay["hungup"])
	}
	// Lifetime is monotonic
	if snap.Lifetime["hungup"] != 1 {
		t.Errorf("expected lifetime to persist, got %d", snap.Lifetime["hungup"])
	}
}

func TestRingBufferReusesBucket(t *testing.T) {
	// Exact 24h later, the same ring slot is reused. Verify the old count
	// is cleared rather than included in the new minute's total.
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	s := NewWithClock(fakeClock(&now))

	s.Record("ringing") // bucket idx X

	now = now.Add(24 * time.Hour) // same minute-of-day, ring slot collides
	s.Record("ringing")

	snap := s.Snapshot()
	if snap.LastMinute["ringing"] != 1 {
		t.Errorf("expected last_minute=1 (stale bucket cleared), got %d", snap.LastMinute["ringing"])
	}
	if snap.Lifetime["ringing"] != 2 {
		t.Errorf("expected lifetime=2, got %d", snap.Lifetime["ringing"])
	}
}

func TestSnapshotMapsAreCopies(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	s := NewWithClock(fakeClock(&now))
	s.Record("ringing")

	snap := s.Snapshot()
	snap.Lifetime["ringing"] = 999
	snap.LastMinute["ringing"] = 999

	snap2 := s.Snapshot()
	if snap2.Lifetime["ringing"] != 1 {
		t.Errorf("snapshot maps should be independent copies, lifetime mutated to %d", snap2.Lifetime["ringing"])
	}
	if snap2.LastMinute["ringing"] != 1 {
		t.Errorf("snapshot maps should be independent copies, last_minute mutated to %d", snap2.LastMinute["ringing"])
	}
}

func TestUptimeSeconds(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	s := NewWithClock(fakeClock(&now))

	now = now.Add(123 * time.Second)
	snap := s.Snapshot()
	if snap.UptimeSeconds != 123 {
		t.Errorf("expected uptime=123, got %v", snap.UptimeSeconds)
	}
}

func TestRecordIsRaceFree(t *testing.T) {
	// Exercises the mutex under -race; failure shows up as a race report.
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Record("ringing")
				_ = s.Snapshot()
			}
		}()
	}
	wg.Wait()

	snap := s.Snapshot()
	if snap.Lifetime["ringing"] != 800 {
		t.Errorf("expected lifetime=800, got %d", snap.Lifetime["ringing"])
	}
}
