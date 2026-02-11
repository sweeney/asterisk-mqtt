# asterisk-mqtt — Implementation Plan

## Context

Build a Go daemon that connects to Asterisk PBX via AMI (Asterisk Manager Interface), correlates raw events into call lifecycle state transitions (Ringing, Answered, HungUp), and publishes them to MQTT. The project is use-case agnostic.

**Network constraint:** This network is internal-only — no SIP trunks or external calls. All calls are between local extensions. There is no inbound/outbound direction classification; `from`/`to` endpoints are identified solely by extension number and name.

The approach is **test-first with real-world fixtures**: a standalone wiretap tool captures live AMI sessions, sanitized captures become test fixtures, and all MQTT interaction is mocked — no real broker needed for tests.

Deployable as a Linux systemd service. CI via GitHub Actions with compiled binaries and test coverage.

---

## Build Order

### Phase 1: Project scaffolding
- `go mod init github.com/sweeney/asterisk-mqtt` (Go 1.23)
- Directory structure (see below)
- `.gitignore` (binaries, raw captures, `.bak` files)
- `Makefile` with build/test/lint targets
- systemd unit file
- GitHub Actions workflow

### Phase 2: Wiretap capture tool (`cmd/wiretap/`)
- Standalone binary, zero dependency on bridge logic
- Connects to AMI (host/port/user/secret via flags)
- Logs in, streams raw bytes to a timestamped file in `testdata/captures/`
- `--sanitize <file>` subcommand: redacts sensitive fields in-place, keeps `.bak`
  - Redacts: passwords, IP addresses, real extension numbers/caller IDs
  - Preserves: event structure, field ordering, timing relationships

### Phase 3: AMI parser (`internal/ami/`)
- `parser.go` — reads raw AMI byte stream, emits `Event` structs
  - AMI protocol: `\r\n`-delimited key/value pairs, blank line between events
  - `Event` struct: map-like with typed accessors for common fields
- `parser_test.go` — feeds fixture `.raw` files through parser, asserts correct event extraction
- No external AMI library — protocol is simple enough to own

### Phase 4: Correlator (`internal/correlator/`)
- `correlator.go` — state machine that tracks calls by `Linkedid`/`Uniqueid`
  - Ingests raw `Event` stream, emits `CallStateChange` structs
  - States: `Ringing`, `Answered`, `HungUp` (extensible later)
  - Handles event interleaving (filters by call ID)
  - Assigns stable call IDs derived from `Linkedid`
- `correlator_test.go` — tested against JSON event sequence fixtures
  - Asserts correct state transition ordering
  - Asserts only stable fields (ignores timestamps, channel counters)

### Phase 5: Publisher interface + mock (`internal/publisher/`)
- `publisher.go` — `Publisher` interface:
  ```go
  type Publisher interface {
      Publish(ctx context.Context, topic string, payload []byte) error
      Close() error
  }
  ```
- `mock.go` — `MockPublisher` that records all publishes for test assertions
- `mqtt.go` — real implementation wrapping `eclipse/paho.mqtt.golang`
  - Bridge code never imports Paho directly

### Phase 6: Bridge daemon (`cmd/asterisk-mqtt/`)
- Wires parser -> correlator -> publisher pipeline
- Config via YAML file (`asterisk-mqtt.yaml`):
  ```yaml
  ami:
    host: 127.0.0.1
    port: 5038
    username: admin
    secret: changeme
  mqtt:
    broker: tcp://localhost:1883
    client_id: asterisk-mqtt
    topic_prefix: asterisk
  ```
- MQTT topic scheme: `{prefix}/call/{CallID}/{state}` (e.g. `asterisk/call/abc123/ringing`)
- Graceful shutdown (SIGINT/SIGTERM)
- Reconnection logic for both AMI and MQTT connections
- Integration tests: raw fixture bytes -> mock publisher -> assert correct topics + payloads

---

## MQTT Event Spec

Correlated call lifecycle events only — no raw AMI event passthrough. Payloads are self-describing JSON designed for consumers who know nothing about Asterisk internals.

### `{prefix}/call/{CallID}/ringing`

Published when a call begins ringing.

```json
{
  "event": "ringing",
  "description": "A call is ringing and waiting to be answered",
  "call_id": "1770888509.40",
  "from": {
    "extension": "1986",
    "name": "Martin"
  },
  "to": {
    "extension": "21"
  },
  "timestamp": "2026-02-12T10:30:00Z"
}
```

### `{prefix}/call/{CallID}/answered`

Published when a ringing call is picked up.

```json
{
  "event": "answered",
  "description": "The call has been answered and parties are now connected",
  "call_id": "1770888509.40",
  "from": {
    "extension": "1986",
    "name": "Martin"
  },
  "to": {
    "extension": "21"
  },
  "ring_duration_seconds": 4.5,
  "timestamp": "2026-02-12T10:30:04Z"
}
```

### `{prefix}/call/{CallID}/hungup`

Published when a call ends, regardless of who hung up or why.

```json
{
  "event": "hungup",
  "description": "The call has ended",
  "call_id": "1770888509.40",
  "from": {
    "extension": "1986",
    "name": "Martin"
  },
  "to": {
    "extension": "21"
  },
  "cause": "normal_clearing",
  "cause_description": "The call was hung up normally by one of the parties",
  "cause_code": 16,
  "talk_duration_seconds": 32.0,
  "total_duration_seconds": 36.5,
  "timestamp": "2026-02-12T10:30:36Z"
}
```

### Design notes

- **`event` field** mirrors the topic suffix — consumers can subscribe to `asterisk/call/+/+` and switch on the `event` field without parsing the topic
- **`description` field** gives plain-English context for each event type, useful for logging, dashboards, or consumers unfamiliar with telephony
- **`cause_description`** translates Asterisk's numeric hangup cause codes into human-readable text (e.g. cause 17 -> "The destination was busy")
- **Durations in seconds** (float) rather than milliseconds — more human-readable, sufficient precision
- **`from`/`to` objects** identify endpoints by extension number and name
- All payloads share a common base shape (`event`, `description`, `call_id`, `from`, `to`, `timestamp`) — consumers can rely on this structure

---

## Project Layout

```
asterisk-mqtt/
  cmd/
    wiretap/
      main.go                    # capture tool entrypoint
    asterisk-mqtt/
      main.go                    # bridge daemon entrypoint
  internal/
    ami/
      parser.go
      parser_test.go
      event.go                   # Event type definition
    correlator/
      correlator.go
      correlator_test.go
      state.go                   # CallStateChange type, state enum
    publisher/
      publisher.go               # interface
      mock.go                    # test mock
      mqtt.go                    # Paho wrapper
    config/
      config.go                  # config loading (YAML + env override)
  testdata/
    captures/                    # raw captures (gitignored except fixtures)
    fixtures/
      answered-outbound.raw      # sanitized raw byte captures
      answered-outbound.json     # parsed event sequences for correlator tests
      answered-internal.raw
      answered-internal.json
      unanswered-cancel.raw
      unanswered-cancel.json
      unanswered-huntgroup.raw
      unanswered-huntgroup.json
  deploy/
    asterisk-mqtt.service        # systemd unit file
  .github/
    workflows/
      ci.yml                     # build + test + coverage + binaries
  asterisk-mqtt.example.yaml     # example config
  Makefile
  go.mod
  go.sum
  .gitignore
```

---

## Dependencies

| Library | Purpose |
|---------|---------|
| `eclipse/paho.mqtt.golang` | MQTT client (only imported by `internal/publisher/mqtt.go`) |
| `gopkg.in/yaml.v3` | Config file parsing |
| Standard library only | AMI parser, correlator, wiretap |

No AMI library — the protocol is simple line-based text. Owning the parser gives us full control and zero transitive dependencies for the core logic.

---

## systemd Service

`deploy/asterisk-mqtt.service`:
- `Type=simple`, `Restart=on-failure`
- Config path via `-config` flag (default `/etc/asterisk-mqtt/asterisk-mqtt.yaml`)
- Runs as dedicated `asterisk-mqtt` user (least privilege)
- `After=network-online.target`

---

## GitHub Actions Workflow (`.github/workflows/ci.yml`)

**Triggers:** push to `main`, all PRs

**Jobs:**
1. **test** — `go test -race -coverprofile=coverage.out ./...`
   - Upload coverage to workflow artifacts
   - Coverage summary in PR comment (via `go tool cover`)
2. **lint** — `golangci-lint run`
3. **build** — cross-compile release binaries:
   - `linux/amd64`
   - `linux/arm` (GOARM=6, for armv6l)
   - Upload as workflow artifacts
   - On tagged releases: attach binaries to GitHub release

---

## Test Strategy

| Layer | Input | Fixture format | Assertions |
|-------|-------|---------------|------------|
| Parser | raw bytes | `.raw` files | Correct event count, field extraction, event types |
| Correlator | `[]Event` | `.json` files | State transitions in correct order, stable call IDs |
| Integration | raw bytes | `.raw` files | MockPublisher received correct topics + payloads |

**Non-determinism handling:**
- Tests assert only stable fields (event type, state transitions, extensions)
- Timestamps, Uniqueid, Linkedid, channel counters — ignored or pattern-matched
- Event interleaving: correlator filters by Linkedid, fixtures may include unrelated events

---

## Verification

1. `make test` — all unit + integration tests pass
2. `make build` — produces binaries for both targets
3. `make lint` — no lint issues
4. Wiretap: `./wiretap -host <asterisk> -port 5038 -user admin -secret xxx` captures a session
5. Sanitize: `./wiretap --sanitize testdata/captures/session.raw` produces clean fixture
6. GitHub Actions: push to repo, CI goes green, binaries appear as artifacts
