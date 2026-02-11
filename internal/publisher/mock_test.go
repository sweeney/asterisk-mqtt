package publisher

import (
	"context"
	"errors"
	"testing"
)

func TestMockPublishAndMessages(t *testing.T) {
	m := NewMockPublisher()

	if err := m.Publish(context.Background(), "topic/a", []byte("hello")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := m.Publish(context.Background(), "topic/b", []byte("world")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := m.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Topic != "topic/a" || string(msgs[0].Payload) != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Topic != "topic/b" || string(msgs[1].Payload) != "world" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}
}

func TestMockPayloadIsCopied(t *testing.T) {
	m := NewMockPublisher()

	payload := []byte("original")
	if err := m.Publish(context.Background(), "t", payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutate the original — recorded message should be unaffected
	payload[0] = 'X'

	msgs := m.Messages()
	if string(msgs[0].Payload) != "original" {
		t.Errorf("payload was not copied, got %q", msgs[0].Payload)
	}
}

func TestMockReset(t *testing.T) {
	m := NewMockPublisher()
	m.Publish(context.Background(), "t", []byte("x"))
	m.Reset()

	if len(m.Messages()) != 0 {
		t.Errorf("expected 0 messages after reset, got %d", len(m.Messages()))
	}
}

func TestMockClose(t *testing.T) {
	m := NewMockPublisher()
	if m.Closed() {
		t.Fatal("expected not closed initially")
	}

	if err := m.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.Closed() {
		t.Fatal("expected closed after Close()")
	}
}

func TestMockSetError(t *testing.T) {
	m := NewMockPublisher()
	testErr := errors.New("broker down")
	m.SetError(testErr)

	err := m.Publish(context.Background(), "t", []byte("x"))
	if !errors.Is(err, testErr) {
		t.Fatalf("expected %v, got %v", testErr, err)
	}

	// Should not have recorded the failed publish
	if len(m.Messages()) != 0 {
		t.Errorf("expected 0 messages after error, got %d", len(m.Messages()))
	}

	// Clear error
	m.SetError(nil)
	if err := m.Publish(context.Background(), "t", []byte("y")); err != nil {
		t.Fatalf("unexpected error after clearing: %v", err)
	}
	if len(m.Messages()) != 1 {
		t.Errorf("expected 1 message after clearing error, got %d", len(m.Messages()))
	}
}
