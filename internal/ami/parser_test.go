package ami_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sweeney/asterisk-mqtt/internal/ami"
)

func fixturesDir() string {
	return filepath.Join("..", "..", "testdata", "fixtures")
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir(), name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

func TestParseAnsweredOutbound(t *testing.T) {
	events := ami.ParseBytes(loadFixture(t, "answered-outbound.raw"))

	if len(events) != 23 {
		t.Fatalf("expected 23 events, got %d", len(events))
	}

	// First event: Newchannel for caller
	if events[0].Type() != "Newchannel" {
		t.Errorf("expected first event Newchannel, got %q", events[0].Type())
	}
	if events[0].Get("CallerIDNum") != "1986" {
		t.Errorf("expected CallerIDNum=1986, got %q", events[0].Get("CallerIDNum"))
	}
	if events[0].Get("CallerIDName") != "Martin" {
		t.Errorf("expected CallerIDName=Martin, got %q", events[0].Get("CallerIDName"))
	}
	if events[0].Get("Context") != "from-internal" {
		t.Errorf("expected Context=from-internal, got %q", events[0].Get("Context"))
	}
	if events[0].Get("Exten") != "21" {
		t.Errorf("expected Exten=21, got %q", events[0].Get("Exten"))
	}
	if events[0].Get("Linkedid") != "1770888509.40" {
		t.Errorf("expected Linkedid=1770888509.40, got %q", events[0].Get("Linkedid"))
	}

	// Count event types
	types := countEventTypes(events)
	assertEventCount(t, types, "Newchannel", 2)
	assertEventCount(t, types, "NewConnectedLine", 4)
	assertEventCount(t, types, "DialBegin", 1)
	assertEventCount(t, types, "Newstate", 3)
	assertEventCount(t, types, "DialState", 1)
	assertEventCount(t, types, "DialEnd", 1)
	assertEventCount(t, types, "BridgeCreate", 0) // BridgeCreate doesn't have Linkedid in split
	assertEventCount(t, types, "BridgeEnter", 2)
	assertEventCount(t, types, "RTCPReceived", 2)
	assertEventCount(t, types, "HangupRequest", 1)
	assertEventCount(t, types, "BridgeLeave", 2)
	assertEventCount(t, types, "SoftHangupRequest", 1)
	assertEventCount(t, types, "Hangup", 2)
	assertEventCount(t, types, "UserEvent", 1)

	// All events share the same Linkedid
	for _, e := range events {
		lid := e.Get("Linkedid")
		if lid != "" && lid != "1770888509.40" {
			t.Errorf("unexpected Linkedid %q", lid)
		}
	}

	// Verify hangup events have cause info
	hangups := filterByType(events, "Hangup")
	for _, h := range hangups {
		if h.GetInt("Cause") != 16 {
			t.Errorf("expected Cause=16, got %d", h.GetInt("Cause"))
		}
		if h.Get("Cause-txt") != "Normal Clearing" {
			t.Errorf("expected Cause-txt=Normal Clearing, got %q", h.Get("Cause-txt"))
		}
	}
}

func TestParseAnsweredInternal(t *testing.T) {
	events := ami.ParseBytes(loadFixture(t, "answered-internal.raw"))

	if len(events) != 27 {
		t.Fatalf("expected 27 events, got %d", len(events))
	}

	// Caller is ext 21 (Kitchen) calling ext 1986 (Martin)
	if events[0].Get("CallerIDNum") != "21" {
		t.Errorf("expected CallerIDNum=21, got %q", events[0].Get("CallerIDNum"))
	}
	if events[0].Get("CallerIDName") != "Kitchen" {
		t.Errorf("expected CallerIDName=Kitchen, got %q", events[0].Get("CallerIDName"))
	}
	if events[0].Get("Exten") != "1986" {
		t.Errorf("expected Exten=1986, got %q", events[0].Get("Exten"))
	}

	types := countEventTypes(events)
	assertEventCount(t, types, "Newchannel", 2)
	assertEventCount(t, types, "Newstate", 3)
	assertEventCount(t, types, "Hangup", 2)
	assertEventCount(t, types, "RTCPReceived", 4)
	assertEventCount(t, types, "RTCPSent", 2)
}

func TestParseUnansweredCancel(t *testing.T) {
	events := ami.ParseBytes(loadFixture(t, "unanswered-cancel.raw"))

	if len(events) != 15 {
		t.Fatalf("expected 15 events, got %d", len(events))
	}

	types := countEventTypes(events)
	assertEventCount(t, types, "Newchannel", 2)
	assertEventCount(t, types, "Newstate", 1) // Only Ringing, no Up
	assertEventCount(t, types, "DialEnd", 1)
	assertEventCount(t, types, "Hangup", 2)

	// DialEnd should be CANCEL (caller hung up)
	for _, e := range events {
		if e.Type() == "DialEnd" {
			if e.Get("DialStatus") != "CANCEL" {
				t.Errorf("expected DialStatus=CANCEL, got %q", e.Get("DialStatus"))
			}
		}
	}

	// Primary hangup cause should be 127 (interworking)
	hangups := filterByType(events, "Hangup")
	primaryHangup := hangups[len(hangups)-1] // Last hangup is the primary channel
	if primaryHangup.GetInt("Cause") != 127 {
		t.Errorf("expected primary hangup Cause=127, got %d", primaryHangup.GetInt("Cause"))
	}
}

func TestParseUnansweredHuntgroup(t *testing.T) {
	events := ami.ParseBytes(loadFixture(t, "unanswered-huntgroup.raw"))

	if len(events) != 53 {
		t.Fatalf("expected 53 events, got %d", len(events))
	}

	types := countEventTypes(events)

	// 7 channels: 1 caller + 6 destinations
	assertEventCount(t, types, "Newchannel", 7)

	// 6 DialBegin events (one per destination)
	assertEventCount(t, types, "DialBegin", 6)

	// 6 Ringing states (one per destination)
	assertEventCount(t, types, "Newstate", 6)

	// 6 DialEnd (all NOANSWER)
	assertEventCount(t, types, "DialEnd", 6)
	for _, e := range events {
		if e.Type() == "DialEnd" && e.Get("DialStatus") != "NOANSWER" {
			t.Errorf("expected DialStatus=NOANSWER, got %q", e.Get("DialStatus"))
		}
	}

	// 7 Hangup events (all channels)
	assertEventCount(t, types, "Hangup", 7)

	// Verify the hunt group destinations
	destinations := map[string]bool{}
	for _, e := range events {
		if e.Type() == "DialBegin" {
			destinations[e.Get("DestCallerIDName")] = true
		}
	}
	expected := []string{"Office", "Games Room", "Kitchen", "Bea & Iris", "Minnie", "Alphie"}
	for _, name := range expected {
		if !destinations[name] {
			t.Errorf("expected hunt group destination %q", name)
		}
	}
}

func TestParseLiveSession(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "captures", "live-session.raw"))
	if err != nil {
		t.Skipf("live session capture not available: %v", err)
	}

	events := ami.ParseBytes(data)

	if len(events) < 100 {
		t.Errorf("expected at least 100 events from live session, got %d", len(events))
	}

	// Should contain multiple Linkedids
	linkedids := map[string]bool{}
	for _, e := range events {
		if lid := e.Get("Linkedid"); lid != "" {
			linkedids[lid] = true
		}
	}
	if len(linkedids) < 4 {
		t.Errorf("expected at least 4 distinct Linkedids, got %d", len(linkedids))
	}
}

func TestParseEmptyInput(t *testing.T) {
	events := ami.ParseBytes([]byte(""))
	if len(events) != 0 {
		t.Errorf("expected 0 events from empty input, got %d", len(events))
	}
}

func TestParseBannerOnly(t *testing.T) {
	events := ami.ParseBytes([]byte("Asterisk Call Manager/11.0.0\r\n\r\n"))
	if len(events) != 0 {
		t.Errorf("expected 0 events from banner only, got %d", len(events))
	}
}

func TestEventAccessors(t *testing.T) {
	evt := ami.NewEvent(
		"Event", "Hangup",
		"Cause", "16",
		"Channel", "PJSIP/1986-00000019",
	)

	if evt.Type() != "Hangup" {
		t.Errorf("expected Type()=Hangup, got %q", evt.Type())
	}
	if evt.GetInt("Cause") != 16 {
		t.Errorf("expected GetInt(Cause)=16, got %d", evt.GetInt("Cause"))
	}
	if evt.Get("Missing") != "" {
		t.Errorf("expected empty string for missing key, got %q", evt.Get("Missing"))
	}
	if evt.GetInt("Channel") != 0 {
		t.Errorf("expected GetInt on non-numeric to return 0, got %d", evt.GetInt("Channel"))
	}
	if !evt.IsResponse() == true {
		// This is not a response
	}

	resp := ami.NewEvent("Response", "Success", "Message", "Authentication accepted")
	if !resp.IsResponse() {
		t.Error("expected IsResponse()=true for response event")
	}
}

func TestParserStreamReading(t *testing.T) {
	input := "Event: Test\r\nKey: Value\r\n\r\nEvent: Test2\r\nKey2: Value2\r\n\r\n"
	parser := ami.NewParser(strings.NewReader(input))

	evt1, ok := parser.Next()
	if !ok {
		t.Fatal("expected first event")
	}
	if evt1.Type() != "Test" {
		t.Errorf("expected Test, got %q", evt1.Type())
	}

	evt2, ok := parser.Next()
	if !ok {
		t.Fatal("expected second event")
	}
	if evt2.Type() != "Test2" {
		t.Errorf("expected Test2, got %q", evt2.Type())
	}

	_, ok = parser.Next()
	if ok {
		t.Error("expected no more events")
	}
}

func TestParserNoTrailingBlankLine(t *testing.T) {
	// AMI stream that ends without a trailing blank line
	input := "Event: Final\r\nKey: Value"
	events := ami.ParseBytes([]byte(input))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type() != "Final" {
		t.Errorf("expected Final, got %q", events[0].Type())
	}
}

// helpers

func countEventTypes(events []ami.Event) map[string]int {
	types := map[string]int{}
	for _, e := range events {
		if t := e.Type(); t != "" {
			types[t]++
		}
	}
	return types
}

func assertEventCount(t *testing.T, types map[string]int, eventType string, expected int) {
	t.Helper()
	if types[eventType] != expected {
		t.Errorf("expected %d %s events, got %d", expected, eventType, types[eventType])
	}
}

func filterByType(events []ami.Event, eventType string) []ami.Event {
	var result []ami.Event
	for _, e := range events {
		if e.Type() == eventType {
			result = append(result, e)
		}
	}
	return result
}
