package correlator_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sweeney/asterisk-mqtt/internal/ami"
	"github.com/sweeney/asterisk-mqtt/internal/correlator"
)

func fixturesDir() string {
	return filepath.Join("..", "..", "testdata", "fixtures")
}

func loadRawFixture(t *testing.T, name string) []ami.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir(), name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return ami.ParseBytes(data)
}

func loadJSONFixture(t *testing.T, name string) []ami.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir(), name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}

	var raw []map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parsing fixture %s: %v", name, err)
	}

	var events []ami.Event
	for _, m := range raw {
		var kvs []string
		for k, v := range m {
			kvs = append(kvs, k, v)
		}
		events = append(events, ami.NewEvent(kvs...))
	}
	return events
}

func processAll(t *testing.T, events []ami.Event) []correlator.CallStateChange {
	t.Helper()
	c := correlator.New()
	var changes []correlator.CallStateChange
	for _, evt := range events {
		changes = append(changes, c.Process(evt)...)
	}
	return changes
}

// --- Answered outbound call (1986 → 21, answered, hung up) ---

func TestAnsweredOutboundFromRaw(t *testing.T) {
	changes := processAll(t, loadRawFixture(t, "answered-outbound.raw"))

	if len(changes) != 3 {
		t.Fatalf("expected 3 state changes, got %d", len(changes))
	}

	assertRinging(t, changes[0], "1770888509.40")
	assertFrom(t, changes[0], "Martin", "1986")
	assertTo(t, changes[0], "21")
	if changes[0].To.Name != "Kitchen" {
		t.Errorf("expected to.name=Kitchen, got %s", changes[0].To.Name)
	}

	assertAnswered(t, changes[1], "1770888509.40")
	if changes[1].RingDuration <= 0 {
		t.Error("expected positive ring duration")
	}

	assertHungUp(t, changes[2], "1770888509.40", 16, "normal_clearing")
	if changes[2].TalkDuration <= 0 {
		t.Error("expected positive talk duration")
	}
	if changes[2].TotalDuration <= 0 {
		t.Error("expected positive total duration")
	}
	if changes[2].TotalDuration < changes[2].TalkDuration {
		t.Error("expected total duration >= talk duration")
	}
}

func TestAnsweredOutboundFromJSON(t *testing.T) {
	changes := processAll(t, loadJSONFixture(t, "answered-outbound.json"))

	if len(changes) != 3 {
		t.Fatalf("expected 3 state changes, got %d", len(changes))
	}

	assertRinging(t, changes[0], "1770888509.40")
	assertAnswered(t, changes[1], "1770888509.40")
	assertHungUp(t, changes[2], "1770888509.40", 16, "normal_clearing")
}

// --- Answered internal call (21 → 1986, answered, hung up) ---

func TestAnsweredInternalFromRaw(t *testing.T) {
	changes := processAll(t, loadRawFixture(t, "answered-internal.raw"))

	if len(changes) != 3 {
		t.Fatalf("expected 3 state changes, got %d", len(changes))
	}

	assertRinging(t, changes[0], "1770888534.43")
	assertFrom(t, changes[0], "Kitchen", "21")
	assertTo(t, changes[0], "1986")

	assertAnswered(t, changes[1], "1770888534.43")
	assertHungUp(t, changes[2], "1770888534.43", 16, "normal_clearing")
}

// --- Unanswered cancel (1986 → 21, caller cancels while ringing) ---

func TestUnansweredCancelFromRaw(t *testing.T) {
	changes := processAll(t, loadRawFixture(t, "unanswered-cancel.raw"))

	if len(changes) != 2 {
		t.Fatalf("expected 2 state changes (ringing + hungup), got %d", len(changes))
	}

	assertRinging(t, changes[0], "1770888559.46")
	assertFrom(t, changes[0], "Martin", "1986")
	assertTo(t, changes[0], "21")

	assertHungUp(t, changes[1], "1770888559.46", 127, "cancelled")
	if changes[1].TalkDuration != 0 {
		t.Errorf("expected zero talk duration for unanswered call, got %f", changes[1].TalkDuration)
	}
	if changes[1].TotalDuration <= 0 {
		t.Error("expected positive total duration")
	}
}

// --- Unanswered hunt group (1986 → 666, 6 destinations, no answer) ---

func TestUnansweredHuntgroupFromRaw(t *testing.T) {
	changes := processAll(t, loadRawFixture(t, "unanswered-huntgroup.raw"))

	if len(changes) != 2 {
		t.Fatalf("expected 2 state changes (ringing + hungup), got %d", len(changes))
	}

	assertRinging(t, changes[0], "1770888635.49")
	assertFrom(t, changes[0], "Martin", "1986")
	assertTo(t, changes[0], "666")

	assertHungUp(t, changes[1], "1770888635.49", 16, "normal_clearing")
	if changes[1].TalkDuration != 0 {
		t.Errorf("expected zero talk duration, got %f", changes[1].TalkDuration)
	}
}

// --- Full live session (all 4 calls interleaved) ---

func TestLiveSessionAllCalls(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "captures", "live-session.raw"))
	if err != nil {
		t.Skipf("live session capture not available: %v", err)
	}

	events := ami.ParseBytes(data)
	changes := processAll(t, events)

	// 4 calls: 2 answered (3 changes each) + 2 unanswered (2 changes each) = 10
	if len(changes) != 10 {
		t.Fatalf("expected 10 state changes from live session, got %d", len(changes))
	}

	// Group by call ID
	calls := map[string][]correlator.CallStateChange{}
	for _, c := range changes {
		calls[c.CallID] = append(calls[c.CallID], c)
	}

	if len(calls) != 4 {
		t.Errorf("expected 4 distinct calls, got %d", len(calls))
	}

	// Count answered vs unanswered
	answered := 0
	unanswered := 0
	for _, states := range calls {
		hasAnswer := false
		for _, s := range states {
			if s.State == correlator.StateAnswered {
				hasAnswer = true
			}
		}
		if hasAnswer {
			answered++
		} else {
			unanswered++
		}
	}
	if answered != 2 {
		t.Errorf("expected 2 answered calls, got %d", answered)
	}
	if unanswered != 2 {
		t.Errorf("expected 2 unanswered calls, got %d", unanswered)
	}
}

// --- Edge cases ---

func TestNoEventsFromResponse(t *testing.T) {
	c := correlator.New()
	evt := ami.NewEvent("Response", "Success", "Message", "Authentication accepted")
	changes := c.Process(evt)
	if len(changes) != 0 {
		t.Errorf("expected no changes from response, got %d", len(changes))
	}
}

func TestNoEventsFromUnrelatedEvent(t *testing.T) {
	c := correlator.New()
	evt := ami.NewEvent("Event", "PeerStatus", "Peer", "PJSIP/trunk")
	changes := c.Process(evt)
	if len(changes) != 0 {
		t.Errorf("expected no changes from PeerStatus, got %d", len(changes))
	}
}

func TestNoEventsWithoutLinkedid(t *testing.T) {
	c := correlator.New()
	evt := ami.NewEvent("Event", "DeviceStateChange", "Device", "PJSIP/1986", "State", "INUSE")
	changes := c.Process(evt)
	if len(changes) != 0 {
		t.Errorf("expected no changes from event without Linkedid, got %d", len(changes))
	}
}

func TestConsistentCallIDsAcrossStates(t *testing.T) {
	changes := processAll(t, loadRawFixture(t, "answered-outbound.raw"))
	if len(changes) < 2 {
		t.Fatal("need at least 2 state changes")
	}
	for i := 1; i < len(changes); i++ {
		if changes[i].CallID != changes[0].CallID {
			t.Errorf("expected consistent call ID %q, got %q at index %d",
				changes[0].CallID, changes[i].CallID, i)
		}
	}
}

func TestStateTransitionOrdering(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		expected []correlator.CallState
	}{
		{"answered", "answered-outbound.raw", []correlator.CallState{
			correlator.StateRinging, correlator.StateAnswered, correlator.StateHungUp,
		}},
		{"unanswered", "unanswered-cancel.raw", []correlator.CallState{
			correlator.StateRinging, correlator.StateHungUp,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := processAll(t, loadRawFixture(t, tt.fixture))
			if len(changes) != len(tt.expected) {
				t.Fatalf("expected %d changes, got %d", len(tt.expected), len(changes))
			}
			for i, exp := range tt.expected {
				if changes[i].State != exp {
					t.Errorf("change[%d]: expected %s, got %s", i, exp, changes[i].State)
				}
			}
		})
	}
}

// --- Deterministic duration tests (using injectable clock) ---

func TestDeterministicDurations(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	c := correlator.NewWithOptions(correlator.WithClock(clock))

	// Newchannel
	c.Process(ami.NewEvent("Event", "Newchannel",
		"CallerIDNum", "1986", "CallerIDName", "Martin",
		"Exten", "21", "Uniqueid", "test.1", "Linkedid", "test.1"))

	// Ringing at t=0
	changes := c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Ringing", "Uniqueid", "test.2", "Linkedid", "test.1"))
	if len(changes) != 1 || changes[0].State != correlator.StateRinging {
		t.Fatal("expected ringing event")
	}

	// Answered at t=5s
	now = now.Add(5 * time.Second)
	changes = c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Up", "Uniqueid", "test.2", "Linkedid", "test.1"))
	if len(changes) != 1 || changes[0].State != correlator.StateAnswered {
		t.Fatal("expected answered event")
	}
	if changes[0].RingDuration != 5.0 {
		t.Errorf("expected ring_duration=5.0, got %f", changes[0].RingDuration)
	}

	// Hangup at t=35s (30s talk)
	now = now.Add(30 * time.Second)
	changes = c.Process(ami.NewEvent("Event", "Hangup",
		"Cause", "16", "Uniqueid", "test.1", "Linkedid", "test.1"))
	if len(changes) != 1 || changes[0].State != correlator.StateHungUp {
		t.Fatal("expected hungup event")
	}
	if changes[0].TalkDuration != 30.0 {
		t.Errorf("expected talk_duration=30.0, got %f", changes[0].TalkDuration)
	}
	if changes[0].TotalDuration != 35.0 {
		t.Errorf("expected total_duration=35.0, got %f", changes[0].TotalDuration)
	}
}

// --- State machine edge cases ---

func TestHangupWithoutNewchannel(t *testing.T) {
	c := correlator.New()
	changes := c.Process(ami.NewEvent("Event", "Hangup",
		"Cause", "16", "Uniqueid", "unknown.1", "Linkedid", "unknown.1"))
	if len(changes) != 0 {
		t.Errorf("expected no changes for unknown call, got %d", len(changes))
	}
}

func TestHangupCleansUpState(t *testing.T) {
	c := correlator.New()

	// First call
	c.Process(ami.NewEvent("Event", "Newchannel",
		"CallerIDNum", "1986", "Exten", "21", "Uniqueid", "call.1", "Linkedid", "call.1"))
	if c.ActiveCalls() != 1 {
		t.Fatalf("expected 1 active call, got %d", c.ActiveCalls())
	}

	c.Process(ami.NewEvent("Event", "Hangup",
		"Cause", "16", "Uniqueid", "call.1", "Linkedid", "call.1"))
	if c.ActiveCalls() != 0 {
		t.Fatalf("expected 0 active calls after hangup, got %d", c.ActiveCalls())
	}

	// Second call reusing same linkedid should work
	c.Process(ami.NewEvent("Event", "Newchannel",
		"CallerIDNum", "21", "Exten", "1986", "Uniqueid", "call.1", "Linkedid", "call.1"))
	c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Ringing", "Uniqueid", "call.2", "Linkedid", "call.1"))
	changes := c.Process(ami.NewEvent("Event", "Hangup",
		"Cause", "16", "Uniqueid", "call.1", "Linkedid", "call.1"))

	if len(changes) != 1 {
		t.Fatalf("expected 1 hangup for reused linkedid, got %d", len(changes))
	}
	if changes[0].From.Extension != "21" {
		t.Errorf("expected from=21 (second call), got %s", changes[0].From.Extension)
	}
}

func TestDuplicateRingingIgnored(t *testing.T) {
	c := correlator.New()
	c.Process(ami.NewEvent("Event", "Newchannel",
		"CallerIDNum", "1986", "Exten", "21", "Uniqueid", "dup.1", "Linkedid", "dup.1"))

	first := c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Ringing", "Uniqueid", "dup.2", "Linkedid", "dup.1"))
	second := c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Ringing", "Uniqueid", "dup.3", "Linkedid", "dup.1"))

	if len(first) != 1 {
		t.Error("expected first ringing to emit")
	}
	if len(second) != 0 {
		t.Error("expected duplicate ringing to be ignored")
	}
}

func TestDuplicateAnsweredIgnored(t *testing.T) {
	c := correlator.New()
	c.Process(ami.NewEvent("Event", "Newchannel",
		"CallerIDNum", "1986", "Exten", "21", "Uniqueid", "dup.1", "Linkedid", "dup.1"))
	c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Ringing", "Uniqueid", "dup.2", "Linkedid", "dup.1"))

	first := c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Up", "Uniqueid", "dup.2", "Linkedid", "dup.1"))
	second := c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Up", "Uniqueid", "dup.3", "Linkedid", "dup.1"))

	if len(first) != 1 {
		t.Error("expected first Up to emit answered")
	}
	if len(second) != 0 {
		t.Error("expected duplicate Up to be ignored")
	}
}

func TestUnansweredHangupHasZeroTalkDuration(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := correlator.NewWithOptions(correlator.WithClock(clock))

	c.Process(ami.NewEvent("Event", "Newchannel",
		"CallerIDNum", "1986", "Exten", "21", "Uniqueid", "na.1", "Linkedid", "na.1"))

	now = now.Add(1 * time.Second)
	c.Process(ami.NewEvent("Event", "Newstate",
		"ChannelStateDesc", "Ringing", "Uniqueid", "na.2", "Linkedid", "na.1"))

	now = now.Add(10 * time.Second)
	changes := c.Process(ami.NewEvent("Event", "Hangup",
		"Cause", "16", "Uniqueid", "na.1", "Linkedid", "na.1"))

	if len(changes) != 1 {
		t.Fatal("expected hangup event")
	}
	if changes[0].TalkDuration != 0 {
		t.Errorf("expected zero talk_duration for unanswered call, got %f", changes[0].TalkDuration)
	}
	if changes[0].TotalDuration != 10.0 {
		t.Errorf("expected total_duration=10.0, got %f", changes[0].TotalDuration)
	}
}

// --- Assertion helpers ---

func assertRinging(t *testing.T, c correlator.CallStateChange, callID string) {
	t.Helper()
	if c.State != correlator.StateRinging {
		t.Errorf("expected state=ringing, got %s", c.State)
	}
	if c.CallID != callID {
		t.Errorf("expected call_id=%s, got %s", callID, c.CallID)
	}
	if c.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func assertAnswered(t *testing.T, c correlator.CallStateChange, callID string) {
	t.Helper()
	if c.State != correlator.StateAnswered {
		t.Errorf("expected state=answered, got %s", c.State)
	}
	if c.CallID != callID {
		t.Errorf("expected call_id=%s, got %s", callID, c.CallID)
	}
}

func assertHungUp(t *testing.T, c correlator.CallStateChange, callID string, causeCode int, cause string) {
	t.Helper()
	if c.State != correlator.StateHungUp {
		t.Errorf("expected state=hungup, got %s", c.State)
	}
	if c.CallID != callID {
		t.Errorf("expected call_id=%s, got %s", callID, c.CallID)
	}
	if c.CauseCode != causeCode {
		t.Errorf("expected cause_code=%d, got %d", causeCode, c.CauseCode)
	}
	if c.Cause != cause {
		t.Errorf("expected cause=%s, got %s", cause, c.Cause)
	}
	if c.CauseDescription == "" {
		t.Error("expected non-empty cause_description")
	}
}

func assertFrom(t *testing.T, c correlator.CallStateChange, name, ext string) {
	t.Helper()
	if name != "" && c.From.Name != name {
		t.Errorf("expected from.name=%s, got %s", name, c.From.Name)
	}
	if ext != "" && c.From.Extension != ext {
		t.Errorf("expected from.extension=%s, got %s", ext, c.From.Extension)
	}
}

func assertTo(t *testing.T, c correlator.CallStateChange, ext string) {
	t.Helper()
	if ext != "" && c.To.Extension != ext {
		t.Errorf("expected to.extension=%s, got %s", ext, c.To.Extension)
	}
}
