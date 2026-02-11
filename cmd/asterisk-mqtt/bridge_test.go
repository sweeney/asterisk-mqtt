package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sweeney/asterisk-mqtt/internal/ami"
	"github.com/sweeney/asterisk-mqtt/internal/correlator"
	"github.com/sweeney/asterisk-mqtt/internal/publisher"
)

func fixturesDir() string {
	return filepath.Join("..", "..", "testdata", "fixtures")
}

func runPipeline(t *testing.T, fixture, prefix string) *publisher.MockPublisher {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir(), fixture))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	mock := publisher.NewMockPublisher()
	events := ami.ParseBytes(data)
	corr := correlator.New()

	for _, evt := range events {
		changes := corr.Process(evt)
		for _, change := range changes {
			if err := publishChange(context.Background(), mock, prefix, change); err != nil {
				t.Fatalf("publish error: %v", err)
			}
		}
	}
	return mock
}

func parsePayload(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return m
}

// --- Answered outbound (1986 → 21) ---

func TestIntegrationAnsweredOutbound(t *testing.T) {
	mock := runPipeline(t, "answered-outbound.raw", "asterisk")
	msgs := mock.Messages()

	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Topics: asterisk/call/{id}/ringing, /answered, /hungup
	assertTopicSuffix(t, msgs[0].Topic, "/ringing")
	assertTopicSuffix(t, msgs[1].Topic, "/answered")
	assertTopicSuffix(t, msgs[2].Topic, "/hungup")

	// All topics share the same call ID segment
	callID := extractCallID(t, msgs[0].Topic)
	for _, m := range msgs[1:] {
		if extractCallID(t, m.Topic) != callID {
			t.Error("expected consistent call ID across topics")
		}
	}

	// Ringing payload
	ringing := parsePayload(t, msgs[0].Payload)
	assertPayloadField(t, ringing, "event", "ringing")
	assertPayloadField(t, ringing, "description", "A call is ringing and waiting to be answered")
	assertPayloadField(t, ringing, "call_id", callID)
	assertPayloadHasKey(t, ringing, "timestamp")
	from := ringing["from"].(map[string]any)
	if from["extension"] != "1986" {
		t.Errorf("expected from.extension=1986, got %v", from["extension"])
	}
	if from["name"] != "Martin" {
		t.Errorf("expected from.name=Martin, got %v", from["name"])
	}
	to := ringing["to"].(map[string]any)
	if to["extension"] != "21" {
		t.Errorf("expected to.extension=21, got %v", to["extension"])
	}
	if to["name"] != "Kitchen" {
		t.Errorf("expected to.name=Kitchen, got %v", to["name"])
	}

	// Answered payload
	answered := parsePayload(t, msgs[1].Payload)
	assertPayloadField(t, answered, "event", "answered")
	assertPayloadField(t, answered, "description", "The call has been answered and parties are now connected")
	assertPayloadHasKey(t, answered, "ring_duration_seconds")
	ringDur := answered["ring_duration_seconds"].(float64)
	if ringDur <= 0 {
		t.Errorf("expected positive ring_duration_seconds, got %f", ringDur)
	}

	// HungUp payload
	hungup := parsePayload(t, msgs[2].Payload)
	assertPayloadField(t, hungup, "event", "hungup")
	assertPayloadField(t, hungup, "description", "The call has ended")
	assertPayloadField(t, hungup, "cause", "normal_clearing")
	assertPayloadField(t, hungup, "cause_description", "The call was hung up normally by one of the parties")
	if hungup["cause_code"].(float64) != 16 {
		t.Errorf("expected cause_code=16, got %v", hungup["cause_code"])
	}
	assertPayloadHasKey(t, hungup, "talk_duration_seconds")
	assertPayloadHasKey(t, hungup, "total_duration_seconds")
	talkDur := hungup["talk_duration_seconds"].(float64)
	totalDur := hungup["total_duration_seconds"].(float64)
	if talkDur <= 0 {
		t.Errorf("expected positive talk_duration, got %f", talkDur)
	}
	if totalDur < talkDur {
		t.Errorf("expected total_duration >= talk_duration")
	}
}

// --- Answered internal (21 → 1986) ---

func TestIntegrationAnsweredInternal(t *testing.T) {
	mock := runPipeline(t, "answered-internal.raw", "pbx")
	msgs := mock.Messages()

	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Verify custom prefix
	if !strings.HasPrefix(msgs[0].Topic, "pbx/call/") {
		t.Errorf("expected topic prefix 'pbx/call/', got %q", msgs[0].Topic)
	}

	ringing := parsePayload(t, msgs[0].Payload)
	from := ringing["from"].(map[string]any)
	if from["extension"] != "21" {
		t.Errorf("expected from.extension=21, got %v", from["extension"])
	}
	if from["name"] != "Kitchen" {
		t.Errorf("expected from.name=Kitchen, got %v", from["name"])
	}
	to := ringing["to"].(map[string]any)
	if to["extension"] != "1986" {
		t.Errorf("expected to.extension=1986, got %v", to["extension"])
	}
}

// --- Unanswered cancel (1986 → 21, caller cancels) ---

func TestIntegrationUnansweredCancel(t *testing.T) {
	mock := runPipeline(t, "unanswered-cancel.raw", "asterisk")
	msgs := mock.Messages()

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (ringing + hungup), got %d", len(msgs))
	}

	assertTopicSuffix(t, msgs[0].Topic, "/ringing")
	assertTopicSuffix(t, msgs[1].Topic, "/hungup")

	// No answered message
	for _, m := range msgs {
		p := parsePayload(t, m.Payload)
		if p["event"] == "answered" {
			t.Error("unexpected answered event for unanswered call")
		}
	}

	hungup := parsePayload(t, msgs[1].Payload)
	if hungup["cause_code"].(float64) != 127 {
		t.Errorf("expected cause_code=127, got %v", hungup["cause_code"])
	}
	assertPayloadField(t, hungup, "cause", "cancelled")
	assertPayloadField(t, hungup, "cause_description", "The call was cancelled by the caller before being answered")
	if hungup["talk_duration_seconds"].(float64) != 0 {
		t.Errorf("expected zero talk_duration for unanswered call")
	}
}

// --- Unanswered hunt group (1986 → 666) ---

func TestIntegrationUnansweredHuntgroup(t *testing.T) {
	mock := runPipeline(t, "unanswered-huntgroup.raw", "asterisk")
	msgs := mock.Messages()

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (ringing + hungup), got %d", len(msgs))
	}

	ringing := parsePayload(t, msgs[0].Payload)
	to := ringing["to"].(map[string]any)
	if to["extension"] != "666" {
		t.Errorf("expected to.extension=666 (hunt group), got %v", to["extension"])
	}
}

// --- Full live session ---

func TestIntegrationLiveSession(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "captures", "live-session.raw"))
	if err != nil {
		t.Skipf("live session capture not available: %v", err)
	}

	mock := publisher.NewMockPublisher()
	events := ami.ParseBytes(data)
	corr := correlator.New()

	for _, evt := range events {
		changes := corr.Process(evt)
		for _, change := range changes {
			if err := publishChange(context.Background(), mock, "asterisk", change); err != nil {
				t.Fatalf("publish error: %v", err)
			}
		}
	}

	msgs := mock.Messages()
	// 4 calls: 2 answered (3 msgs) + 2 unanswered (2 msgs) = 10
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}

	// All payloads should be valid JSON with required fields
	for i, m := range msgs {
		p := parsePayload(t, m.Payload)
		for _, required := range []string{"event", "description", "call_id", "from", "to", "timestamp"} {
			if _, ok := p[required]; !ok {
				t.Errorf("message %d missing required field %q", i, required)
			}
		}
	}

	// Group messages by call
	calls := map[string][]map[string]any{}
	for _, m := range msgs {
		p := parsePayload(t, m.Payload)
		id := p["call_id"].(string)
		calls[id] = append(calls[id], p)
	}

	if len(calls) != 4 {
		t.Errorf("expected 4 distinct calls, got %d", len(calls))
	}
}

// --- Payload structure validation ---

func TestPayloadCommonShape(t *testing.T) {
	mock := runPipeline(t, "answered-outbound.raw", "asterisk")
	msgs := mock.Messages()

	requiredFields := []string{"event", "description", "call_id", "from", "to", "timestamp"}

	for i, m := range msgs {
		p := parsePayload(t, m.Payload)
		for _, field := range requiredFields {
			if _, ok := p[field]; !ok {
				t.Errorf("message %d: missing required field %q", i, field)
			}
		}

		// event field should match topic suffix
		event := p["event"].(string)
		if !strings.HasSuffix(m.Topic, "/"+event) {
			t.Errorf("message %d: event %q doesn't match topic %q", i, event, m.Topic)
		}
	}
}

// --- helpers ---

func assertTopicSuffix(t *testing.T, topic, suffix string) {
	t.Helper()
	if !strings.HasSuffix(topic, suffix) {
		t.Errorf("expected topic %q to end with %q", topic, suffix)
	}
}

func assertPayloadField(t *testing.T, p map[string]any, key string, expected string) {
	t.Helper()
	if v, ok := p[key]; !ok {
		t.Errorf("missing field %q", key)
	} else if v != expected {
		t.Errorf("expected %s=%q, got %q", key, expected, v)
	}
}

func assertPayloadHasKey(t *testing.T, p map[string]any, key string) {
	t.Helper()
	if _, ok := p[key]; !ok {
		t.Errorf("missing field %q", key)
	}
}

func extractCallID(t *testing.T, topic string) string {
	t.Helper()
	// topic format: prefix/call/{id}/{state}
	parts := strings.Split(topic, "/")
	if len(parts) < 4 {
		t.Fatalf("unexpected topic format: %q", topic)
	}
	return parts[len(parts)-2]
}
