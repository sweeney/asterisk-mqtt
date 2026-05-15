package config

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// minHeartbeatInterval is the smallest non-zero heartbeat publish interval we
// accept. Smaller values are almost certainly a misconfiguration (e.g. a typo
// that produces a sub-second publish flood); set interval to 0 to disable.
const minHeartbeatInterval = time.Second

type Config struct {
	AMI       AMIConfig       `yaml:"ami"`
	MQTT      MQTTConfig      `yaml:"mqtt"`
	Heartbeat HeartbeatConfig `yaml:"heartbeat"`
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

// HeartbeatConfig controls periodic status/stats publishing.
// An Interval of 0 disables the heartbeat entirely (no goroutine, no LWT).
type HeartbeatConfig struct {
	Interval Duration `yaml:"interval"`
	Topic    string   `yaml:"topic"`
}

// Duration is a time.Duration that unmarshals from a Go-style duration string
// like "30s" or "5m". The bare integer 0 is also accepted (and means "off"),
// but any other bare integer is rejected to avoid the nanosecond-vs-second
// confusion that would otherwise turn `interval: 60` into a 60ns flood.
type Duration time.Duration

func (d Duration) String() string          { return time.Duration(d).String() }
func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	switch value.Tag {
	case "!!int":
		var n int64
		if err := value.Decode(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("duration must be 0 (to disable) or a duration string like \"30s\"; got bare integer %d which would be interpreted as %d nanoseconds", n, n)
		}
		*d = 0
		return nil
	case "!!str":
		parsed, err := time.ParseDuration(value.Value)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", value.Value, err)
		}
		*d = Duration(parsed)
		return nil
	default:
		return fmt.Errorf("duration must be 0 or a duration string, got %s", value.Tag)
	}
}

func (c *AMIConfig) Addr() string {
	return net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
}

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
		Heartbeat: HeartbeatConfig{
			Interval: Duration(60 * time.Second),
			Topic:    "status",
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

func (c *Config) validate() error {
	if c.AMI.Host == "" {
		return fmt.Errorf("ami.host is required")
	}
	if c.AMI.Port < 1 || c.AMI.Port > 65535 {
		return fmt.Errorf("ami.port must be between 1 and 65535, got %d", c.AMI.Port)
	}
	if c.AMI.Username == "" {
		return fmt.Errorf("ami.username is required")
	}
	if c.AMI.Secret == "" {
		return fmt.Errorf("ami.secret is required")
	}
	if c.MQTT.Broker == "" {
		return fmt.Errorf("mqtt.broker is required")
	}
	if c.MQTT.ClientID == "" {
		return fmt.Errorf("mqtt.client_id is required")
	}
	if c.MQTT.TopicPrefix == "" {
		return fmt.Errorf("mqtt.topic_prefix is required")
	}
	if err := validateMQTTTopic("mqtt.topic_prefix", c.MQTT.TopicPrefix); err != nil {
		return err
	}
	if c.Heartbeat.Interval < 0 {
		return fmt.Errorf("heartbeat.interval must not be negative, got %s", c.Heartbeat.Interval)
	}
	if c.Heartbeat.Interval > 0 {
		if c.Heartbeat.Interval.Duration() < minHeartbeatInterval {
			return fmt.Errorf("heartbeat.interval must be at least %s when non-zero, got %s", minHeartbeatInterval, c.Heartbeat.Interval)
		}
		if c.Heartbeat.Topic == "" {
			return fmt.Errorf("heartbeat.topic is required when heartbeat.interval > 0")
		}
		if err := validateMQTTTopic("heartbeat.topic", c.Heartbeat.Topic); err != nil {
			return err
		}
	}
	return nil
}

// validateMQTTTopic rejects topic strings that would produce wildcards,
// reserved-prefix topics, or otherwise surprising routing once concatenated
// into a publish topic. Empty topics are caller-checked.
func validateMQTTTopic(field, topic string) error {
	if strings.ContainsAny(topic, "+#") {
		return fmt.Errorf("%s must not contain MQTT wildcards (+ or #): %q", field, topic)
	}
	if strings.ContainsRune(topic, 0) {
		return fmt.Errorf("%s must not contain NUL bytes: %q", field, topic)
	}
	for _, r := range topic {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("%s must not contain whitespace or control characters: %q", field, topic)
		}
	}
	if strings.HasPrefix(topic, "$") {
		return fmt.Errorf("%s must not begin with '$' (reserved by MQTT brokers): %q", field, topic)
	}
	if strings.HasPrefix(topic, "/") || strings.HasSuffix(topic, "/") {
		return fmt.Errorf("%s must not have a leading or trailing '/': %q", field, topic)
	}
	if strings.Contains(topic, "//") {
		return fmt.Errorf("%s must not contain empty levels (//): %q", field, topic)
	}
	return nil
}
