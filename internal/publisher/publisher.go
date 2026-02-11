package publisher

import "context"

// Publisher defines the interface for publishing messages.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload []byte) error
	Close() error
}
