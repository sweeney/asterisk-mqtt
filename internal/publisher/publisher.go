package publisher

import "context"

// Publisher defines the interface for publishing messages.
type Publisher interface {
	// Publish sends a non-retained message at the publisher's configured QoS.
	Publish(ctx context.Context, topic string, payload []byte) error
	// PublishRetained sends a retained message so late subscribers receive
	// the most recent payload immediately upon subscribing. Used for
	// heartbeat / status topics.
	PublishRetained(ctx context.Context, topic string, payload []byte) error
	Close() error
}
