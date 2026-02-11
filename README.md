# asterisk-mqtt

A lightweight Go daemon that connects to an Asterisk PBX via AMI (Asterisk Manager Interface), correlates raw telephony events into human-readable call lifecycle states, and publishes them to MQTT as self-describing JSON.

Consumers subscribe to MQTT topics and receive clean, structured events — no knowledge of Asterisk internals required.

## What it does

Asterisk emits dozens of low-level AMI events per call — `Newchannel`, `Newstate`, `DialBegin`, `DialEnd`, `Hangup`, and more. Most consumers don't care about any of that. They want to know: *is the phone ringing? did someone answer? when did the call end?*

asterisk-mqtt watches the raw event stream, correlates events by call ID, and emits three lifecycle events:

| MQTT Topic | When |
|------------|------|
| `{prefix}/call/{id}/ringing` | A call begins ringing |
| `{prefix}/call/{id}/answered` | The call is picked up |
| `{prefix}/call/{id}/hungup` | The call ends (for any reason) |

Every payload is self-describing JSON with plain-English descriptions, caller/callee identity, durations, and hangup cause translation:

```json
{
  "event": "hungup",
  "description": "The call has ended",
  "call_id": "1770888509.40",
  "from": { "extension": "1986", "name": "Martin" },
  "to": { "extension": "21", "name": "Kitchen" },
  "cause": "normal_clearing",
  "cause_description": "The call was hung up normally by one of the parties",
  "cause_code": 16,
  "talk_duration_seconds": 32.0,
  "total_duration_seconds": 36.5,
  "timestamp": "2026-02-12T10:30:36Z"
}
```

The correlator also detects when a caller cancels a ringing call (via `DialEnd` / `DialStatus: CANCEL`) and reports it as `"cause": "cancelled"` rather than Asterisk's misleading default of `"cause": "interworking"`.

## Installation

### Build from source

```bash
make build
```

This produces `bin/asterisk-mqtt` and `bin/wiretap` for the host platform.

### Cross-compile for deployment

```bash
make build-linux-amd64   # x86_64 servers
make build-linux-arm     # ARM v6 (Pi Zero, etc.)
```

### Deploy

Copy the binary to your Asterisk host and create a config file at `/etc/asterisk-mqtt/asterisk-mqtt.yaml`:

```yaml
ami:
  host: 127.0.0.1
  port: 5038
  username: wiretap
  secret: changeme

mqtt:
  broker: tcp://localhost:1883
  client_id: asterisk-mqtt
  topic_prefix: asterisk
```

Install as a systemd service:

```bash
sudo deploy/install.sh /path/to/asterisk-mqtt /path/to/config.yaml
```

For subsequent updates, use the deploy script from the development machine:

```bash
./deploy/deploy.sh user@asterisk-host
```

This builds, copies, and restarts the service. First-time install vs update is detected automatically.

## Configuration

All fields have sensible defaults. Only `ami.username` and `ami.secret` are required:

| Field | Default | Description |
|-------|---------|-------------|
| `ami.host` | `127.0.0.1` | Asterisk AMI host |
| `ami.port` | `5038` | Asterisk AMI port |
| `ami.username` | *(required)* | AMI manager username |
| `ami.secret` | *(required)* | AMI manager secret |
| `mqtt.broker` | `tcp://localhost:1883` | MQTT broker URL |
| `mqtt.client_id` | `asterisk-mqtt` | MQTT client identifier |
| `mqtt.topic_prefix` | `asterisk` | Prefix for all MQTT topics |

The daemon validates all config fields at startup and will refuse to start with an invalid configuration.

## MQTT event reference

All events share a common shape:

```json
{
  "event": "ringing|answered|hungup",
  "description": "Human-readable description of this event",
  "call_id": "Asterisk Linkedid (stable across all events for a call)",
  "from": { "extension": "1986", "name": "Martin" },
  "to": { "extension": "21", "name": "Kitchen" },
  "timestamp": "RFC 3339 UTC"
}
```

### `ringing`

Published when a call begins ringing at the destination.

### `answered`

Published when the call is picked up. Adds:

- `ring_duration_seconds` — how long the phone rang before being answered

### `hungup`

Published when the call ends for any reason. Adds:

- `cause` — machine-readable cause (`normal_clearing`, `user_busy`, `no_answer`, `cancelled`, etc.)
- `cause_description` — human-readable explanation
- `cause_code` — raw Asterisk/Q.850 cause code
- `talk_duration_seconds` — time spent connected (0 if never answered)
- `total_duration_seconds` — total time from first ring to hangup

### Subscribing

```bash
# All events for all calls
mosquitto_sub -t 'asterisk/call/#' -v

# Only hungup events
mosquitto_sub -t 'asterisk/call/+/hungup' -v

# Events for a specific call
mosquitto_sub -t 'asterisk/call/1770888509.40/+' -v
```

## Wiretap tool

The `wiretap` binary is a standalone AMI capture tool for recording raw event streams:

```bash
# Capture a live session
./wiretap -host 192.168.1.200 -port 5038 -user admin -secret xxx

# Sanitize a capture (redacts IPs, secrets; keeps .bak)
./wiretap --sanitize testdata/captures/session.raw
```

Sanitized captures become test fixtures. This is how all the fixture data in this project was produced.

## Project structure

```
cmd/
  asterisk-mqtt/         Bridge daemon
  wiretap/               AMI capture tool
internal/
  ami/                   AMI protocol parser
  correlator/            Call state machine
  publisher/             MQTT publisher interface + mock
  config/                YAML config with validation
testdata/
  fixtures/              Sanitized per-call fixtures (.raw + .json)
  captures/              Full session captures (gitignored)
deploy/
  deploy.sh              Build + ship + restart
  remote-install.sh      First-install / update logic (runs on target)
  asterisk-mqtt.service  systemd unit
```

## Testing

### Methodology: real-world fixtures

Every test in this project is driven by real AMI data captured from a live Asterisk system using the wiretap tool. The approach:

1. **Capture** — `wiretap` connects to AMI and records the raw byte stream during live calls
2. **Sanitize** — the `--sanitize` flag redacts IP addresses, SIP credentials, and other sensitive fields while preserving event structure and timing relationships
3. **Split** — full session captures are split into per-call fixtures, each representing a specific scenario
4. **Test** — fixtures feed the parser, correlator, and integration tests

This means the test suite exercises the exact byte sequences, field orderings, and event interleavings that a real Asterisk system produces — not hand-crafted approximations. When a bug is found in production, the raw capture *is* the regression test.

The fixture library covers four real call scenarios:

| Fixture | Scenario | Events |
|---------|----------|--------|
| `answered-outbound` | Extension 1986 calls 21, answered, both hang up | 23 |
| `answered-internal` | Extension 21 calls 1986, answered, both hang up | 27 |
| `unanswered-cancel` | Extension 1986 calls 21, caller cancels while ringing | 15 |
| `unanswered-huntgroup` | Extension 1986 calls hunt group 666 (6 destinations), no answer | 53 |

A full interleaved session capture (`live-session.raw`) containing all four calls is used for end-to-end integration tests.

Each `.raw` fixture has a corresponding `.json` fixture — the same events as structured JSON objects — allowing the correlator to be tested independently of the AMI parser.

### Running the tests

```bash
make test         # Run all tests with race detector + coverage
make lint         # Run go vet
```

### Test coverage by layer

| Layer | Tests | What it covers |
|-------|-------|----------------|
| AMI parser | 10 | Raw byte parsing, event extraction, edge cases |
| Correlator | 19 | State transitions, durations, cancellation detection, edge cases |
| Config | 13 | Validation, defaults, error handling |
| Integration | 6 | Full pipeline: raw bytes → correlator → MQTT payloads |
| **Total** | **48** | |

The correlator supports an injectable clock (`WithClock` option) so duration calculations are tested deterministically — no flaky timing assertions.

### Code metrics

| | Lines |
|---|------:|
| Source code | 985 |
| Test code | 1,283 |
| **Test:source ratio** | **1.3:1** |

More test code than production code. The test suite is designed so that adding a new call state or modifying the correlator logic will immediately surface regressions through fixture-driven assertions.

## Dependencies

| Library | Purpose |
|---------|---------|
| [paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) | MQTT client (isolated to `internal/publisher/mqtt.go`) |
| [yaml.v3](https://gopkg.in/yaml.v3) | Config file parsing |

The AMI parser and correlator use only the Go standard library. No external AMI library — the protocol is simple line-based text and owning the parser gives full control with zero transitive dependencies for the core logic.

## CI

GitHub Actions runs on every push to `main` and on all PRs:

- **test** — `go vet`, race-detected tests, coverage report with per-package heatmap, slowest tests
- **build** — cross-compiles `asterisk-mqtt` and `wiretap` for `linux/amd64` and `linux/arm` (GOARM=6)
- **release** — on version tags (`v*`), attaches all binaries to a GitHub release

## How this project was built

This project was built collaboratively with [Claude Code](https://claude.ai/claude-code) (Claude Opus 4.6) in a single extended session. The process:

1. **Planning** — a detailed implementation plan was written covering architecture, event spec, test strategy, and deployment. Claude reviewed and refined it before any code was written.

2. **Implementation** — Claude wrote all code following the plan: AMI parser, correlator state machine, MQTT publisher, bridge daemon, wiretap tool, systemd service, CI workflow.

3. **Live testing** — the wiretap tool was deployed to a real Asterisk system. Four types of calls were made (answered, internal, cancelled, hunt group). The raw AMI captures were sanitized and became the project's test fixtures.

4. **Iteration from real data** — observing actual MQTT output revealed issues invisible in synthetic tests:
   - Destination extension names were missing (the `to` field had no `name`) — fixed by extracting `DestCallerIDName` from `DialBegin` events
   - Caller-cancelled calls reported `"cause": "interworking"` (Asterisk's catch-all cause code 127) — fixed by detecting `DialEnd` with `DialStatus: CANCEL`
   - The inbound/outbound direction classification was unnecessary for an internal-only network — removed entirely, simplifying the data model

5. **Hardening** — a critical review of the codebase identified structural gaps: hard-coded `time.Now()` preventing deterministic duration tests, no config validation, no mock error injection, missing state machine edge case tests. All four were fixed.

The entire codebase — 985 lines of source, 1,283 lines of tests, CI, deployment scripts, and documentation — was produced in this workflow. The real-world fixture methodology means the test suite exercises actual Asterisk behaviour, not assumptions about it.

## License

MIT
