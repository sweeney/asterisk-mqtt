// Package stats tracks counts of call lifecycle events over rolling time windows
// (last minute, hour, and 24 hours) plus lifetime totals, for inclusion in the
// service's MQTT heartbeat payload.
package stats

import (
	"sync"
	"time"
)

const (
	bucketCount    = 24 * 60 // 1440 one-minute buckets = 24 hours
	bucketDuration = time.Minute
)

type bucket struct {
	minute int64
	counts map[string]uint64
}

// Clock provides the current time. Defaults to time.Now; override in tests.
type Clock func() time.Time

// Stats records per-event-type counts over a 24h rolling window plus lifetime
// totals. All methods are safe for concurrent use.
type Stats struct {
	mu       sync.Mutex
	clock    Clock
	started  time.Time
	lifetime map[string]uint64
	buckets  [bucketCount]bucket
}

// New creates a Stats instance using time.Now as its clock.
func New() *Stats {
	return NewWithClock(time.Now)
}

// NewWithClock creates a Stats instance with a custom clock (for tests).
func NewWithClock(clock Clock) *Stats {
	return &Stats{
		clock:    clock,
		started:  clock(),
		lifetime: make(map[string]uint64),
	}
}

// Record increments the counter for event by 1 at the current time.
func (s *Stats) Record(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	minute := now.Unix() / 60
	idx := int(minute % bucketCount)
	if idx < 0 {
		idx += bucketCount
	}
	if s.buckets[idx].minute != minute || s.buckets[idx].counts == nil {
		s.buckets[idx].minute = minute
		s.buckets[idx].counts = make(map[string]uint64)
	}
	s.buckets[idx].counts[event]++
	s.lifetime[event]++
}

// Snapshot is a point-in-time view of the recorded stats.
type Snapshot struct {
	StartedAt     time.Time         `json:"started_at"`
	UptimeSeconds float64           `json:"uptime_seconds"`
	Lifetime      map[string]uint64 `json:"lifetime"`
	LastMinute    map[string]uint64 `json:"last_minute"`
	LastHour      map[string]uint64 `json:"last_hour"`
	LastDay       map[string]uint64 `json:"last_day"`
}

// Snapshot returns the current counts. The maps in the returned snapshot are
// independent copies — callers may modify them freely.
func (s *Stats) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	nowMinute := now.Unix() / 60

	return Snapshot{
		StartedAt:     s.started,
		UptimeSeconds: now.Sub(s.started).Seconds(),
		Lifetime:      copyCounts(s.lifetime),
		LastMinute:    s.sumWindow(nowMinute, 1),
		LastHour:      s.sumWindow(nowMinute, 60),
		LastDay:       s.sumWindow(nowMinute, bucketCount),
	}
}

// sumWindow returns total counts across the most recent `minutes` buckets,
// ending at nowMinute inclusive. Caller must hold s.mu.
func (s *Stats) sumWindow(nowMinute int64, minutes int64) map[string]uint64 {
	out := make(map[string]uint64)
	start := nowMinute - minutes + 1
	for m := start; m <= nowMinute; m++ {
		idx := int(m % bucketCount)
		if idx < 0 {
			idx += bucketCount
		}
		b := &s.buckets[idx]
		if b.minute != m {
			continue
		}
		for k, v := range b.counts {
			out[k] += v
		}
	}
	return out
}

func copyCounts(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
