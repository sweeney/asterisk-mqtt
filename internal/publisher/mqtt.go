package publisher

import (
	"context"
	"fmt"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTTPublisher wraps a Paho MQTT client.
type MQTTPublisher struct {
	client mqtt.Client
	qos    byte
}

// MQTTOptions configures the MQTT publisher.
type MQTTOptions struct {
	Broker   string
	ClientID string
	QoS      byte

	// Will, if non-empty topic, configures an MQTT Last Will & Testament so
	// subscribers learn immediately if this client dies without a clean
	// disconnect. The will payload is published by the broker, retained, at
	// the publisher's QoS.
	WillTopic   string
	WillPayload []byte
}

// NewMQTTPublisher creates and connects an MQTT publisher.
func NewMQTTPublisher(opts MQTTOptions) (*MQTTPublisher, error) {
	clientOpts := mqtt.NewClientOptions().
		AddBroker(opts.Broker).
		SetClientID(opts.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetMaxReconnectInterval(60 * time.Second)

	if opts.WillTopic != "" {
		clientOpts = clientOpts.SetBinaryWill(opts.WillTopic, opts.WillPayload, opts.QoS, true)
	}

	client := mqtt.NewClient(clientOpts)
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("connecting to MQTT broker %s: %w", opts.Broker, err)
	}

	return &MQTTPublisher{
		client: client,
		qos:    opts.QoS,
	}, nil
}

func (p *MQTTPublisher) Publish(_ context.Context, topic string, payload []byte) error {
	token := p.client.Publish(topic, p.qos, false, payload)
	token.Wait()
	return token.Error()
}

func (p *MQTTPublisher) PublishRetained(_ context.Context, topic string, payload []byte) error {
	token := p.client.Publish(topic, p.qos, true, payload)
	token.Wait()
	return token.Error()
}

func (p *MQTTPublisher) Close() error {
	p.client.Disconnect(1000)
	return nil
}
