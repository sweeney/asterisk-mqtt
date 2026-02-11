package publisher

import (
	"context"
	"sync"
)

// Message records a single published message.
type Message struct {
	Topic   string
	Payload []byte
}

// MockPublisher records all publishes for test assertions.
type MockPublisher struct {
	mu       sync.Mutex
	messages []Message
	closed   bool
	err      error // if set, Publish returns this error
}

// NewMockPublisher creates a new MockPublisher.
func NewMockPublisher() *MockPublisher {
	return &MockPublisher{}
}

func (m *MockPublisher) Publish(_ context.Context, topic string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	p := make([]byte, len(payload))
	copy(p, payload)
	m.messages = append(m.messages, Message{Topic: topic, Payload: p})
	return nil
}

func (m *MockPublisher) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// Messages returns a copy of all published messages.
func (m *MockPublisher) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := make([]Message, len(m.messages))
	copy(msgs, m.messages)
	return msgs
}

// Reset clears all recorded messages.
func (m *MockPublisher) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = nil
}

// Closed returns whether Close was called.
func (m *MockPublisher) Closed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// SetError causes all subsequent Publish calls to return err.
// Pass nil to clear.
func (m *MockPublisher) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}
