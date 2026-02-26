# asterisk-mqtt: A Code Walkthrough

*2026-02-26T14:06:10Z by Showboat 0.6.1*
<!-- showboat-id: cbac0e6b-be10-48da-89ab-877bc8cfa697 -->

## Overview

asterisk-mqtt is a Go daemon that bridges an Asterisk PBX system to an MQTT broker. Asterisk emits dozens of low-level events over its Manager Interface (AMI) for a single phone call. This daemon listens to that stream, correlates the raw events into three human-readable call lifecycle states — **ringing**, **answered**, and **hungup** — and publishes each state change as a self-describing JSON message on an MQTT topic.

The end result is that any application on your network can subscribe to topics like `asterisk/call/1770888509.40/ringing` and receive clean, useful data without needing to understand Asterisk internals.

The pipeline is a simple chain of four stages:

```
Asterisk PBX
    │  (TCP, AMI protocol)
    ▼
ami.Parser        – tokenises the raw byte stream into Event structs
    │
    ▼
correlator.Correlator – state-machines each call, emits CallStateChange
    │
    ▼
publishChange()   – serialises to JSON, builds MQTT topic
    │  (MQTT, QoS 1)
    ▼
MQTT broker
```

## Project layout

```bash
find . -type f -name '*.go' | grep -v _test | sort && echo '---' && find . -type f -name '*.go' | grep _test | sort
```

```output
./cmd/asterisk-mqtt/main.go
./cmd/wiretap/main.go
./internal/ami/event.go
./internal/ami/parser.go
./internal/config/config.go
./internal/correlator/correlator.go
./internal/correlator/state.go
./internal/publisher/mock.go
./internal/publisher/mqtt.go
./internal/publisher/publisher.go
---
./cmd/asterisk-mqtt/bridge_test.go
./internal/ami/parser_test.go
./internal/config/config_test.go
./internal/correlator/correlator_test.go
./internal/publisher/mock_test.go
```

Production code is in five packages across two commands. Tests live alongside the code they test. The `testdata/fixtures/` directory holds real captured-and-sanitised AMI sessions used as regression fixtures.

We will walk through each package in pipeline order.

## Step 1: Configuration (`internal/config/config.go`)

Before anything connects, the daemon loads a YAML config file. The structs mirror the YAML structure exactly — one section for AMI, one for MQTT.

```bash
sed -n '1,31p' internal/config/config.go
```

```output
package config

import (
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	AMI  AMIConfig  `yaml:"ami"`
	MQTT MQTTConfig `yaml:"mqtt"`
}

type AMIConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Secret   string `yaml:"secret"`
}

type MQTTConfig struct {
	Broker      string `yaml:"broker"`
	ClientID    string `yaml:"client_id"`
	TopicPrefix string `yaml:"topic_prefix"`
}

func (c *AMIConfig) Addr() string {
	return net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
}
```

`Load` reads the YAML, sets sensible defaults, then validates every field. The defaults mean a minimal config only needs to supply credentials:

```bash
sed -n '33,60p' internal/config/config.go
```

```output
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := &Config{
		AMI: AMIConfig{
			Host: "127.0.0.1",
			Port: 5038,
		},
		MQTT: MQTTConfig{
			Broker:      "tcp://localhost:1883",
			ClientID:    "asterisk-mqtt",
			TopicPrefix: "asterisk",
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}
```

The strategy is to pre-populate the struct with defaults, then unmarshal over it. Any key present in the YAML overwrites the default; absent keys keep the default value. Validation runs last and rejects empty or out-of-range values before the daemon attempts any network connections.

## Step 2: The AMI Protocol (`internal/ami/`)

Asterisk's Manager Interface streams events over TCP as blocks of plain-text key-value pairs, separated by blank lines. Here is what the very first event in a real call looks like:

```bash
sed -n '1,16p' testdata/fixtures/answered-outbound.raw
```

```output
Event: Newchannel
Privilege: call,all
Channel: PJSIP/1986-00000019
ChannelState: 4
ChannelStateDesc: Ring
CallerIDNum: 1986
CallerIDName: Martin
ConnectedLineNum: <unknown>
ConnectedLineName: <unknown>
Language: en
AccountCode: 
Context: from-internal
Exten: 21
Priority: 1
Uniqueid: 1770888509.40
Linkedid: 1770888509.40
```

Each event is a block of `Key: Value` lines, terminated by a blank line. Two fields matter most here: `Event` (the type) and `Linkedid` (the stable identifier that ties all channels in a single call together, which the correlator will use as a grouping key).

### The Event type (`internal/ami/event.go`)

Events are represented as an ordered slice of key-value pairs rather than a map, because AMI can emit duplicate keys and the insertion order must be preserved.

```bash
sed -n '8,68p' internal/ami/event.go
```

```output
// Event represents a parsed AMI event as an ordered set of key-value pairs.
type Event struct {
	headers []header
}

type header struct {
	Key   string
	Value string
}

// New creates an Event from a slice of key-value pairs.
func NewEvent(kvs ...string) Event {
	e := Event{}
	for i := 0; i+1 < len(kvs); i += 2 {
		e.headers = append(e.headers, header{Key: kvs[i], Value: kvs[i+1]})
	}
	return e
}

// Get returns the value for the given key, or empty string if not found.
func (e Event) Get(key string) string {
	for _, h := range e.headers {
		if h.Key == key {
			return h.Value
		}
	}
	return ""
}

// Type returns the Event header value (the AMI event type).
func (e Event) Type() string {
	return e.Get("Event")
}

// GetInt returns the integer value for the given key, or 0 if not found/parseable.
func (e Event) GetInt(key string) int {
	v, _ := strconv.Atoi(e.Get(key))
	return v
}

// GetFloat returns the float value for the given key, or 0 if not found/parseable.
func (e Event) GetFloat(key string) float64 {
	v, _ := strconv.ParseFloat(e.Get(key), 64)
	return v
}

// GetTime returns the timestamp for the given key parsed as RFC3339, or zero time.
func (e Event) GetTime(key string) time.Time {
	t, _ := time.Parse(time.RFC3339, e.Get(key))
	return t
}

// Headers returns all headers as key-value pairs.
func (e Event) Headers() []header {
	return e.headers
}

// IsResponse returns true if this is an AMI response rather than an event.
func (e Event) IsResponse() bool {
	return e.Get("Response") != ""
}
```

The typed accessors (`GetInt`, `GetFloat`, `GetTime`) swallow parse errors and return zero values — the correlator only calls them on fields where it already knows the type is correct, so silent zero-on-error is the right tradeoff for this domain.

`IsResponse` distinguishes AMI *responses* (replies to commands, which carry a `Response:` header) from *events* (unsolicited notifications, which carry an `Event:` header). The correlator skips responses.

### The Parser (`internal/ami/parser.go`)

The parser is a simple line-by-line scanner. It accumulates headers until a blank line, then returns the event.

```bash
cat internal/ami/parser.go
```

```output
package ami

import (
	"bufio"
	"io"
	"strings"
)

// Parser reads an AMI byte stream and emits Events.
type Parser struct {
	scanner *bufio.Scanner
}

// NewParser creates a Parser that reads from the given reader.
func NewParser(r io.Reader) *Parser {
	return &Parser{scanner: bufio.NewScanner(r)}
}

// Next reads the next event from the stream.
// Returns the event and true if an event was read, or a zero Event and false at EOF.
func (p *Parser) Next() (Event, bool) {
	var headers []header

	for p.scanner.Scan() {
		line := p.scanner.Text()

		// Strip trailing \r if present (AMI uses \r\n)
		line = strings.TrimRight(line, "\r")

		// Blank line marks end of an event block
		if line == "" {
			if len(headers) > 0 {
				return Event{headers: headers}, true
			}
			continue
		}

		// Parse "Key: Value" format
		idx := strings.Index(line, ": ")
		if idx < 0 {
			// Some AMI lines (like the banner) don't have ": " — skip them
			// unless we're already collecting headers
			if len(headers) == 0 {
				continue
			}
			// Malformed line inside an event — include as-is with empty key
			headers = append(headers, header{Key: "", Value: line})
			continue
		}

		key := line[:idx]
		value := line[idx+2:]
		headers = append(headers, header{Key: key, Value: value})
	}

	// EOF — return any pending event
	if len(headers) > 0 {
		return Event{headers: headers}, true
	}
	return Event{}, false
}

// ParseAll reads all events from the stream and returns them.
func (p *Parser) ParseAll() []Event {
	var events []Event
	for {
		evt, ok := p.Next()
		if !ok {
			break
		}
		events = append(events, evt)
	}
	return events
}

// ParseBytes is a convenience function that parses all events from a byte slice.
func ParseBytes(data []byte) []Event {
	return NewParser(strings.NewReader(string(data))).ParseAll()
}
```

The parser owns no goroutines and has no error state — it is a pure pull iterator. Callers call `Next()` in a loop until it returns `false`. Two edge cases are handled explicitly:

1. **The banner line** — the first line Asterisk sends is a free-form greeting like `Asterisk Call Manager/9.0`, which has no `:` separator. The parser skips it when no headers have been collected yet.
2. **Trailing \r** — AMI uses Windows-style `\r\n` line endings; `bufio.Scanner` strips the `\n` but leaves the `\r`, so each line gets trimmed.

The fixture file we saw earlier has 53 raw events for a single unanswered call. Let's count how many events the parser extracts from it:

```bash
grep -c '^Event:' testdata/fixtures/answered-outbound.raw && grep -c '^Event:' testdata/fixtures/unanswered-huntgroup.raw
```

```output
23
53
```

23 raw events produce a 3-message lifecycle (ringing → answered → hungup). 53 raw events for the hunt-group call also produce just 2 messages (ringing → hungup). The correlator does all that reduction.

## Step 3: The Correlator (`internal/correlator/`)

This is the heart of the daemon. The correlator is a stateful object that tracks every in-progress call and emits a `CallStateChange` value each time a call crosses a lifecycle boundary.

### Output types (`state.go`)

First, the types the correlator produces:

```bash
cat internal/correlator/state.go
```

```output
package correlator

import "time"

// CallState represents the lifecycle state of a call.
type CallState string

const (
	StateRinging  CallState = "ringing"
	StateAnswered CallState = "answered"
	StateHungUp   CallState = "hungup"
)

// Endpoint represents an internal extension.
type Endpoint struct {
	Extension string `json:"extension"`
	Name      string `json:"name,omitempty"`
}

// CallStateChange is emitted by the correlator when a call transitions state.
type CallStateChange struct {
	State     CallState `json:"event"`
	CallID    string    `json:"call_id"`
	From      Endpoint  `json:"from"`
	To        Endpoint  `json:"to"`
	Timestamp time.Time `json:"timestamp"`

	// Ringing -> Answered
	RingDuration float64 `json:"ring_duration_seconds,omitempty"`

	// HungUp fields
	Cause            string  `json:"cause,omitempty"`
	CauseDescription string  `json:"cause_description,omitempty"`
	CauseCode        int     `json:"cause_code,omitempty"`
	TalkDuration     float64 `json:"talk_duration_seconds,omitempty"`
	TotalDuration    float64 `json:"total_duration_seconds,omitempty"`
}

// HangupCause maps Asterisk hangup cause codes to names and descriptions.
var HangupCause = map[int]struct {
	Name        string
	Description string
}{
	0:   {"unknown", "Unknown or no cause provided"},
	16:  {"normal_clearing", "The call was hung up normally by one of the parties"},
	17:  {"user_busy", "The destination was busy"},
	18:  {"no_answer", "The destination did not answer"},
	19:  {"no_answer", "The destination did not answer within the timeout"},
	21:  {"call_rejected", "The call was rejected by the destination"},
	31:  {"normal_unspecified", "Normal call clearing, unspecified cause"},
	34:  {"congestion", "All circuits are busy or no circuit is available"},
	127: {"interworking", "An interworking error occurred"},
}
```

`CallStateChange` is a single flat struct that carries all possible fields for all three states. The `omitempty` tags mean unused fields are invisible in the JSON output, so the `ringing` event only has common fields, `answered` adds `ring_duration_seconds`, and `hungup` adds the cause and duration fields.

`HangupCause` translates Q.850/Asterisk integer cause codes into human-readable names and descriptions. Note code 127 (`interworking`) — this is what Asterisk emits when the caller hangs up before the callee answers. The correlator detects this case separately and overrides it with a clearer name: `cancelled`.

### Internal state and event dispatch (`correlator.go`)

```bash
sed -n '9,86p' internal/correlator/correlator.go
```

```output
// Clock provides the current time. Defaults to time.Now; override in tests.
type Clock func() time.Time

// callState tracks the internal state of an in-progress call.
type callState struct {
	linkedID   string
	from       Endpoint
	to         Endpoint
	ringTime   time.Time
	answerTime time.Time
	answered   bool
	rung       bool
	cancelled  bool // DialEnd with DialStatus=CANCEL seen
}

// Correlator tracks AMI events and emits CallStateChange structs
// when calls transition between lifecycle states.
type Correlator struct {
	calls map[string]*callState // keyed by Linkedid
	clock Clock
}

// New creates a new Correlator.
func New() *Correlator {
	return &Correlator{
		calls: make(map[string]*callState),
		clock: time.Now,
	}
}

// Option configures a Correlator.
type Option func(*Correlator)

// WithClock sets the time source for the correlator.
func WithClock(c Clock) Option {
	return func(corr *Correlator) { corr.clock = c }
}

// NewWithOptions creates a Correlator with the given options.
func NewWithOptions(opts ...Option) *Correlator {
	c := New()
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Process ingests an AMI event and returns any resulting state changes.
func (c *Correlator) Process(evt ami.Event) []CallStateChange {
	if evt.IsResponse() {
		return nil
	}

	linkedID := evt.Get("Linkedid")
	if linkedID == "" {
		return nil
	}

	switch evt.Type() {
	case "Newchannel":
		return c.handleNewchannel(evt, linkedID)
	case "DialBegin":
		return c.handleDialBegin(evt, linkedID)
	case "Newstate":
		return c.handleNewstate(evt, linkedID)
	case "DialEnd":
		return c.handleDialEnd(evt, linkedID)
	case "Hangup":
		return c.handleHangup(evt, linkedID)
	default:
		return nil
	}
}

// ActiveCalls returns the number of calls currently being tracked.
func (c *Correlator) ActiveCalls() int {
	return len(c.calls)
}
```

`callState` is the per-call scratchpad. Boolean flags track what has already happened (`rung`, `answered`, `cancelled`) to guard against duplicate events — Asterisk can and does fire the same state transition on multiple channels within the same logical call.

`Process` is the entry point: it discards responses and events with no `Linkedid`, then dispatches to one of five handlers. All other event types are silently ignored — the daemon only cares about the handful that signal lifecycle changes.

The `Clock` injection is the testability hook: tests replace `time.Now` with a deterministic function so duration assertions are exact.

### The five event handlers

**Newchannel** — the first event for any call. Creates the tracking record, extracting caller extension/name and the destination extension. It exits early if an entry already exists (the originating and destination channels can both fire `Newchannel` for the same `Linkedid`).

```bash
sed -n '88,104p' internal/correlator/correlator.go
```

```output
func (c *Correlator) handleNewchannel(evt ami.Event, linkedID string) []CallStateChange {
	if _, exists := c.calls[linkedID]; exists {
		return nil
	}

	c.calls[linkedID] = &callState{
		linkedID: linkedID,
		from: Endpoint{
			Extension: evt.Get("CallerIDNum"),
			Name:      evt.Get("CallerIDName"),
		},
		to: Endpoint{
			Extension: evt.Get("Exten"),
		},
	}
	return nil
}
```

Notice that `to.Name` is left empty here — the `Newchannel` event carries the destination *extension* (`Exten`) but not the destination's display name. That arrives later in `DialBegin`.

**DialBegin** — fires when Asterisk starts dialling the destination. The destination's display name is available here as `DestCallerIDName`, so the handler backfills `to.Name` if it is still empty.

```bash
sed -n '106,117p' internal/correlator/correlator.go
```

```output
func (c *Correlator) handleDialBegin(evt ami.Event, linkedID string) []CallStateChange {
	cs := c.calls[linkedID]
	if cs == nil {
		return nil
	}
	if cs.to.Name == "" {
		if name := evt.Get("DestCallerIDName"); name != "" {
			cs.to.Name = name
		}
	}
	return nil
}
```

**Newstate** — the most important handler. It watches `ChannelStateDesc` for two values:

- `"Ringing"`: records the ring start time, sets the `rung` guard flag, and emits a `StateRinging` change.
- `"Up"`: records the answer time, computes `ring_duration_seconds`, sets the `answered` guard flag, and emits a `StateAnswered` change.

Both guards (`cs.rung`, `cs.answered`) prevent duplicate emissions if the same state fires on a second channel.

```bash
sed -n '119,164p' internal/correlator/correlator.go
```

```output
func (c *Correlator) handleNewstate(evt ami.Event, linkedID string) []CallStateChange {
	cs := c.calls[linkedID]
	if cs == nil {
		return nil
	}

	stateDesc := evt.Get("ChannelStateDesc")
	now := c.clock()

	switch stateDesc {
	case "Ringing":
		if cs.rung {
			return nil
		}
		cs.rung = true
		cs.ringTime = now
		return []CallStateChange{{
			State:     StateRinging,
			CallID:    linkedID,
			From:      cs.from,
			To:        cs.to,
			Timestamp: now,
		}}

	case "Up":
		if cs.answered {
			return nil
		}
		cs.answered = true
		cs.answerTime = now
		ringDur := 0.0
		if !cs.ringTime.IsZero() {
			ringDur = now.Sub(cs.ringTime).Seconds()
		}
		return []CallStateChange{{
			State:        StateAnswered,
			CallID:       linkedID,
			From:         cs.from,
			To:           cs.to,
			RingDuration: ringDur,
			Timestamp:    now,
		}}
	}

	return nil
}
```

**DialEnd** — fires when dialling concludes, for any reason. The handler only cares about one outcome: `DialStatus == "CANCEL"` means the caller hung up before the destination answered. The `cancelled` flag is set here and used later in `handleHangup`.

```bash
sed -n '166,175p' internal/correlator/correlator.go
```

```output
func (c *Correlator) handleDialEnd(evt ami.Event, linkedID string) []CallStateChange {
	cs := c.calls[linkedID]
	if cs == nil {
		return nil
	}
	if evt.Get("DialStatus") == "CANCEL" {
		cs.cancelled = true
	}
	return nil
}
```

**Hangup** — the final handler, and the most complex. Asterisk fires a `Hangup` event for *every* channel in a call, but we only want to emit one `hungup` state change per logical call. The guard is elegant: `Uniqueid == Linkedid` is only true for the *originating* channel of the call — so only that one Hangup triggers the output.

```bash
sed -n '177,226p' internal/correlator/correlator.go
```

```output
func (c *Correlator) handleHangup(evt ami.Event, linkedID string) []CallStateChange {
	cs := c.calls[linkedID]
	if cs == nil {
		return nil
	}

	// Only emit hangup once — on the first Hangup event for this call
	uniqueID := evt.Get("Uniqueid")
	if uniqueID != linkedID {
		return nil
	}

	now := c.clock()
	causeCode := evt.GetInt("Cause")

	causeName := "unknown"
	causeDesc := "Unknown or no cause provided"
	if cs.cancelled && !cs.answered {
		causeName = "cancelled"
		causeDesc = "The call was cancelled by the caller before being answered"
	} else if info, ok := HangupCause[causeCode]; ok {
		causeName = info.Name
		causeDesc = info.Description
	}

	talkDur := 0.0
	if cs.answered && !cs.answerTime.IsZero() {
		talkDur = now.Sub(cs.answerTime).Seconds()
	}
	totalDur := 0.0
	if !cs.ringTime.IsZero() {
		totalDur = now.Sub(cs.ringTime).Seconds()
	}

	change := CallStateChange{
		State:            StateHungUp,
		CallID:           linkedID,
		From:             cs.from,
		To:               cs.to,
		Cause:            causeName,
		CauseDescription: causeDesc,
		CauseCode:        causeCode,
		TalkDuration:     talkDur,
		TotalDuration:    totalDur,
		Timestamp:        now,
	}

	delete(c.calls, linkedID)
	return []CallStateChange{change}
}
```

The cause-code logic has a specific priority:

1. If `cancelled && !answered` — use `"cancelled"` regardless of what Asterisk says. (Asterisk reports code 127 / `interworking` in this case, which is misleading.)
2. Else look up the integer code in `HangupCause`.
3. Else fall through to `"unknown"`.

After building the `CallStateChange`, the call is removed from the tracking map with `delete(c.calls, linkedID)`. Memory stays bounded: every call that starts (Newchannel) eventually ends (Hangup on the originating channel).

## Step 4: The Publisher (`internal/publisher/`)

The publisher layer is tiny on purpose. A one-method interface enables the real MQTT client and a test mock to be swapped without changing any calling code.

```bash
cat internal/publisher/publisher.go
```

```output
package publisher

import "context"

// Publisher defines the interface for publishing messages.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload []byte) error
	Close() error
}
```

The real implementation wraps the Eclipse Paho Go client. Key configuration choices:

```bash
cat internal/publisher/mqtt.go
```

```output
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

func (p *MQTTPublisher) Close() error {
	p.client.Disconnect(1000)
	return nil
}
```

`SetAutoReconnect(true)` + `SetConnectRetry(true)` mean the MQTT connection is self-healing — if the broker goes away and comes back, Paho reconnects without the daemon needing to know. The retry interval grows from 5 s to a ceiling of 60 s.

Messages are published with `retain: false` — subscribers only receive events that occur after they subscribe. QoS 1 (at-least-once delivery) ensures each event reaches the broker even over a flaky network link, at the cost of possible duplicates.

## Step 5: The Bridge (`cmd/asterisk-mqtt/main.go`)

This is where the pipeline is assembled. `main` has four responsibilities: load config, connect to MQTT, install signal handlers, and enter the reconnection loop.

```bash
sed -n '23,61p' cmd/asterisk-mqtt/main.go
```

```output
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
```

The SIGINT/SIGTERM handler cancels the root context. Everything downstream that blocks on I/O will unblock because the context cancellation causes the AMI TCP connection to be closed (see the goroutine in `runSession` below).

### The reconnection loop: `run`

The AMI connection is not expected to be permanent. If `runSession` returns an error (connection reset, authentication failure, etc.), `run` waits 5 seconds then tries again. If the context is cancelled (clean shutdown) it returns immediately.

```bash
sed -n '63,78p' cmd/asterisk-mqtt/main.go
```

```output
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
```

The `select` during the backoff sleep is important: if shutdown is requested while the daemon is waiting to reconnect, it exits immediately rather than waiting the full 5 seconds.

### A single AMI session: `runSession`

Each call to `runSession` owns one TCP connection to Asterisk. It dials, authenticates, then runs the event loop until the connection closes or the context is cancelled.

```bash
sed -n '80,133p' cmd/asterisk-mqtt/main.go
```

```output
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
```

The shutdown goroutine (`go func() { <-ctx.Done(); conn.Close() }`) is the bridge between context cancellation and the blocking `parser.Next()` call. When the context is cancelled, `conn.Close()` causes `scanner.Scan()` inside the parser to return `false`, which unblocks the event loop.

The AMI login is a raw write of two `Key: Value` lines followed by a blank line — the same format the parser reads. After writing the login action the daemon does not wait for the response; the login response will arrive as the first event from `parser.Next()` and will be silently discarded by the correlator's `IsResponse()` check.

### Serialising state changes: `publishChange`

This function converts a `CallStateChange` into the JSON payload and constructs the MQTT topic.

```bash
sed -n '135,198p' cmd/asterisk-mqtt/main.go
```

```output
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
```

The numeric duration fields use `*float64` pointers rather than plain `float64` values. This is required so that `omitempty` actually omits them — a plain `float64` zero value is a valid measurement (e.g., a call answered instantly has `ring_duration_seconds: 0`), but `omitempty` on a non-pointer zero would still suppress it. Using a pointer means `nil` = absent and `&0.0` = explicitly zero.

The topic format is `{prefix}/call/{callID}/{state}`. For the default prefix and a typical Asterisk call ID that produces topics like:

```
asterisk/call/1770888509.40/ringing
asterisk/call/1770888509.40/answered
asterisk/call/1770888509.40/hungup
```

Subscribers can use MQTT wildcards — `asterisk/call/+/ringing` to watch all incoming calls, or `asterisk/call/1770888509.40/#` to follow one specific call.

## Step 6: Testing strategy

The project is tested at two levels: unit tests in each package and integration tests in `cmd/asterisk-mqtt/bridge_test.go`.

### Fixture-driven testing

The test data is not synthetic. The `cmd/wiretap` tool connects to a live Asterisk system, captures the raw AMI byte stream for real calls, and saves it to `.raw` files. A sanitise pass redacts passwords and replaces real phone numbers and IP addresses. The result is a ground-truth byte stream that the tests replay byte-for-byte.

```bash
ls testdata/fixtures/
```

```output
answered-internal.json
answered-internal.raw
answered-outbound.json
answered-outbound.raw
unanswered-cancel.json
unanswered-cancel.raw
unanswered-huntgroup.json
unanswered-huntgroup.raw
```

Four call scenarios are captured:

- **answered-outbound**: Extension 1986 calls extension 21, the call is answered, both parties hang up normally (3 MQTT events).
- **answered-internal**: Extension 21 calls extension 1986, same outcome (3 MQTT events).
- **unanswered-cancel**: Caller dials, then hangs up before the destination answers. Demonstrates the `cancelled` cause override (2 MQTT events).
- **unanswered-huntgroup**: Caller dials a hunt group (extension 666). The group rotates through multiple agents, none answer. Demonstrates that 53 raw AMI events reduce to 2 MQTT events.

Each scenario has both a `.raw` file (the actual bytes the parser sees) and a `.json` file (same events as structured JSON, used for the correlator unit tests which operate at the parsed-event level rather than the byte level).

### Integration test example

The bridge integration tests feed a `.raw` fixture through the full pipeline (parser → correlator → `publishChange`) using the mock publisher, then assert on the resulting MQTT messages.

```bash
sed -n '1,80p' cmd/asterisk-mqtt/bridge_test.go
```

```output
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
```

`runPipeline` is the test helper: load fixture bytes → parse → correlate → publish to mock. The mock records every (topic, payload) pair. The test then walks the recorded messages and asserts on topics and JSON field values.

### Running the tests

```bash
go test ./... 2>&1
```

```output
go: downloading github.com/eclipse/paho.mqtt.golang v1.5.1
go: downloading gopkg.in/yaml.v3 v3.0.1
go: downloading golang.org/x/sync v0.17.0
go: downloading golang.org/x/net v0.44.0
go: downloading github.com/gorilla/websocket v1.5.3
ok  	github.com/sweeney/asterisk-mqtt/cmd/asterisk-mqtt	0.024s
?   	github.com/sweeney/asterisk-mqtt/cmd/wiretap	[no test files]
ok  	github.com/sweeney/asterisk-mqtt/internal/ami	0.018s
ok  	github.com/sweeney/asterisk-mqtt/internal/config	0.042s
ok  	github.com/sweeney/asterisk-mqtt/internal/correlator	0.019s
ok  	github.com/sweeney/asterisk-mqtt/internal/publisher	0.018s
```

All tests pass. The `cmd/wiretap` package has no tests — it is a capture utility, not library code.

## The complete picture

Putting it all together, here is the call lifecycle as seen by each layer for a simple answered call:

```bash
grep '^Event:' testdata/fixtures/answered-outbound.raw | awk '{print }'
```

```output
Event: Newchannel
Event: NewConnectedLine
Event: NewConnectedLine
Event: Newchannel
Event: NewConnectedLine
Event: DialBegin
Event: NewConnectedLine
Event: Newstate
Event: DialState
Event: Newstate
Event: DialEnd
Event: Newstate
Event: BridgeEnter
Event: BridgeEnter
Event: RTCPReceived
Event: RTCPReceived
Event: HangupRequest
Event: BridgeLeave
Event: BridgeLeave
Event: SoftHangupRequest
Event: Hangup
Event: UserEvent
Event: Hangup
```

23 raw AMI events. The correlator responds to exactly 5 of them:

| AMI event | Handler | Action |
|---|---|---|
| `Newchannel` (first) | `handleNewchannel` | Creates tracking record |
| `DialBegin` | `handleDialBegin` | Fills in `to.Name` |
| `Newstate` (Ringing) | `handleNewstate` | Emits → `ringing` on MQTT |
| `Newstate` (Up) | `handleNewstate` | Emits → `answered` on MQTT |
| `Hangup` (Uniqueid==Linkedid) | `handleHangup` | Emits → `hungup` on MQTT, deletes tracking record |

The other 18 events — `NewConnectedLine`, `DialState`, `BridgeEnter`, `RTCPReceived`, `HangupRequest`, `BridgeLeave`, `SoftHangupRequest`, `UserEvent`, and the second `Hangup` (on the destination channel) — are silently dropped.
