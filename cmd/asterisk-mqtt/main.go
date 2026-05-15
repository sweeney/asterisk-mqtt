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
	"sync"
	"syscall"
	"time"

	"github.com/sweeney/asterisk-mqtt/internal/ami"
	"github.com/sweeney/asterisk-mqtt/internal/config"
	"github.com/sweeney/asterisk-mqtt/internal/correlator"
	"github.com/sweeney/asterisk-mqtt/internal/publisher"
	"github.com/sweeney/asterisk-mqtt/internal/stats"
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

	mqttOpts := publisher.MQTTOptions{
		Broker:   cfg.MQTT.Broker,
		ClientID: cfg.MQTT.ClientID,
		QoS:      1,
	}

	statusTopic := ""
	offlineBytes := []byte(`{"state":"offline"}`)
	if cfg.Heartbeat.Interval > 0 {
		statusTopic = fmt.Sprintf("%s/%s", cfg.MQTT.TopicPrefix, cfg.Heartbeat.Topic)
		mqttOpts.WillTopic = statusTopic
		mqttOpts.WillPayload = offlineBytes
	}

	pub, err := publisher.NewMQTTPublisher(mqttOpts)
	if err != nil {
		log.Fatalf("connecting to MQTT: %v", err)
	}
	defer pub.Close()

	log.Printf("connected to MQTT broker %s", cfg.MQTT.Broker)

	s := stats.New()

	var hbWg sync.WaitGroup
	if statusTopic != "" {
		// Publish an initial "online" status immediately so subscribers see
		// state without waiting a full interval, and start the periodic loop.
		if err := publishHeartbeat(ctx, pub, statusTopic, s); err != nil {
			log.Printf("initial heartbeat publish error: %v", err)
		}
		hbWg.Add(1)
		go func() {
			defer hbWg.Done()
			heartbeatLoop(ctx, pub, statusTopic, cfg.Heartbeat.Interval.Duration(), s)
		}()
	}

	if err := run(ctx, cfg, pub, s); err != nil && ctx.Err() == nil {
		log.Fatalf("error: %v", err)
	}

	// Wait for the heartbeat goroutine to fully drain so its in-flight
	// "online" publish cannot land after the final "offline" we publish
	// below. Paho preserves FIFO order on the wire, so once the goroutine
	// has returned, any next publish we queue is guaranteed to be last.
	hbWg.Wait()

	// Clean shutdown: replace the retained "online" payload with "offline"
	// so subscribers don't see stale state after we exit. On crash, the
	// broker delivers the LWT instead; either way the retained payload
	// reflects reality.
	if statusTopic != "" {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := pub.PublishRetained(shutdownCtx, statusTopic, offlineBytes); err != nil {
			log.Printf("publishing offline status: %v", err)
		}
	}

	log.Println("shutdown complete")
}

func run(ctx context.Context, cfg *config.Config, pub publisher.Publisher, s *stats.Stats) error {
	for {
		err := runSession(ctx, cfg, pub, s)
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

func runSession(ctx context.Context, cfg *config.Config, pub publisher.Publisher, s *stats.Stats) error {
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
			s.Record(string(change.State))
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

// heartbeatPayload is the JSON published to the status topic while the daemon
// is healthy. The offline payload (LWT + clean-shutdown final) is a separate,
// minimal `{"state":"offline"}` so we don't have to mark every field with
// omitempty and pretend zero values are "missing".
type heartbeatPayload struct {
	State         string           `json:"state"`
	StartedAt     string           `json:"started_at"`
	UptimeSeconds float64          `json:"uptime_seconds"`
	Timestamp     string           `json:"timestamp"`
	Events        eventCountsBlock `json:"events"`
}

type eventCountsBlock struct {
	Lifetime   map[string]uint64 `json:"lifetime"`
	LastMinute map[string]uint64 `json:"last_minute"`
	LastHour   map[string]uint64 `json:"last_hour"`
	LastDay    map[string]uint64 `json:"last_day"`
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

func heartbeatLoop(ctx context.Context, pub publisher.Publisher, topic string, interval time.Duration, s *stats.Stats) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := publishHeartbeat(ctx, pub, topic, s); err != nil {
				log.Printf("heartbeat publish error: %v", err)
			}
		}
	}
}

func publishHeartbeat(ctx context.Context, pub publisher.Publisher, topic string, s *stats.Stats) error {
	snap := s.Snapshot()
	payload := heartbeatPayload{
		State:         "online",
		StartedAt:     snap.StartedAt.UTC().Format(time.RFC3339),
		UptimeSeconds: snap.UptimeSeconds,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Events: eventCountsBlock{
			Lifetime:   snap.Lifetime,
			LastMinute: snap.LastMinute,
			LastHour:   snap.LastHour,
			LastDay:    snap.LastDay,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling heartbeat: %w", err)
	}
	return pub.PublishRetained(ctx, topic, data)
}
