package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sweeney/asterisk-mqtt/internal/ami"
	"github.com/sweeney/asterisk-mqtt/internal/config"
	"github.com/sweeney/asterisk-mqtt/internal/correlator"
	"github.com/sweeney/asterisk-mqtt/internal/publisher"
)

func main() {
	configPath := flag.String("config", "/etc/asterisk-mqtt/asterisk-mqtt.yaml", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("received signal %v, shutting down", sig)
		cancel()
	}()

	pub, err := publisher.NewMQTTPublisher(publisher.MQTTOptions{
		Broker:   cfg.MQTT.Broker,
		ClientID: cfg.MQTT.ClientID,
		QoS:      1,
	})
	if err != nil {
		log.Fatalf("connecting to MQTT: %v", err)
	}
	defer pub.Close()

	log.Printf("connected to MQTT broker %s", cfg.MQTT.Broker)

	if err := run(ctx, cfg, pub); err != nil && ctx.Err() == nil {
		log.Fatalf("error: %v", err)
	}

	log.Println("shutdown complete")
}

func run(ctx context.Context, cfg *config.Config, pub publisher.Publisher) error {
	for {
		err := runSession(ctx, cfg, pub)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("AMI session error: %v, reconnecting in 5s", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func runSession(ctx context.Context, cfg *config.Config, pub publisher.Publisher) error {
	addr := cfg.AMI.Addr()
	log.Printf("connecting to AMI at %s", addr)

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial AMI: %w", err)
	}
	defer conn.Close()

	// Close connection when context is cancelled
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	reader := bufio.NewReader(conn)

	// Read banner
	banner, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading AMI banner: %w", err)
	}
	log.Printf("AMI banner: %s", strings.TrimSpace(banner))

	// Login
	loginCmd := fmt.Sprintf("Action: Login\r\nUsername: %s\r\nSecret: %s\r\n\r\n", cfg.AMI.Username, cfg.AMI.Secret)
	if _, err := conn.Write([]byte(loginCmd)); err != nil {
		return fmt.Errorf("sending login: %w", err)
	}

	log.Println("AMI authenticated, processing events")

	// Process events
	parser := ami.NewParser(reader)
	corr := correlator.New()

	for {
		evt, ok := parser.Next()
		if !ok {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("AMI connection closed")
		}

		changes := corr.Process(evt)
		for _, change := range changes {
			if err := publishChange(ctx, pub, cfg.MQTT.TopicPrefix, change); err != nil {
				log.Printf("publish error: %v", err)
			}
		}
	}
}

// mqttPayload is the JSON structure published to MQTT.
type mqttPayload struct {
	Event            string   `json:"event"`
	Description      string   `json:"description"`
	CallID           string   `json:"call_id"`
	From             endpoint `json:"from"`
	To               endpoint `json:"to"`
	Timestamp        string   `json:"timestamp"`
	RingDuration     *float64 `json:"ring_duration_seconds,omitempty"`
	Cause            string   `json:"cause,omitempty"`
	CauseDescription string   `json:"cause_description,omitempty"`
	CauseCode        *int     `json:"cause_code,omitempty"`
	TalkDuration     *float64 `json:"talk_duration_seconds,omitempty"`
	TotalDuration    *float64 `json:"total_duration_seconds,omitempty"`
}

type endpoint struct {
	Extension string `json:"extension"`
	Name      string `json:"name,omitempty"`
}

var stateDescriptions = map[correlator.CallState]string{
	correlator.StateRinging:  "A call is ringing and waiting to be answered",
	correlator.StateAnswered: "The call has been answered and parties are now connected",
	correlator.StateHungUp:   "The call has ended",
}

func publishChange(ctx context.Context, pub publisher.Publisher, prefix string, change correlator.CallStateChange) error {
	topic := fmt.Sprintf("%s/call/%s/%s", prefix, change.CallID, change.State)

	payload := mqttPayload{
		Event:       string(change.State),
		Description: stateDescriptions[change.State],
		CallID:      change.CallID,
		From: endpoint{
			Extension: change.From.Extension,
			Name:      change.From.Name,
		},
		To: endpoint{
			Extension: change.To.Extension,
			Name:      change.To.Name,
		},
		Timestamp: change.Timestamp.UTC().Format(time.RFC3339),
	}

	switch change.State {
	case correlator.StateAnswered:
		payload.RingDuration = &change.RingDuration
	case correlator.StateHungUp:
		payload.Cause = change.Cause
		payload.CauseDescription = change.CauseDescription
		payload.CauseCode = &change.CauseCode
		payload.TalkDuration = &change.TalkDuration
		payload.TotalDuration = &change.TotalDuration
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	log.Printf("publishing %s", topic)
	return pub.Publish(ctx, topic, data)
}
