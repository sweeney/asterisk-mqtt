package config

import (
	"fmt"
	"net"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

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

// Duration is a time.Duration that unmarshals from either a bare numeric value
// (interpreted as nanoseconds, so use 0 to disable) or a Go-style duration
// string like "30s" or "5m".
type Duration time.Duration

func (d Duration) String() string         { return time.Duration(d).String() }
func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	switch value.Tag {
	case "!!int":
		var n int64
		if err := value.Decode(&n); err != nil {
			return err
		}
		*d = Duration(time.Duration(n))
		return nil
	case "!!str":
		parsed, err := time.ParseDuration(value.Value)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", value.Value, err)
		}
		*d = Duration(parsed)
		return nil
	default:
		return fmt.Errorf("duration must be an integer or duration string, got %s", value.Tag)
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
	if c.Heartbeat.Interval < 0 {
		return fmt.Errorf("heartbeat.interval must not be negative, got %s", c.Heartbeat.Interval)
	}
	if c.Heartbeat.Interval > 0 && c.Heartbeat.Topic == "" {
		return fmt.Errorf("heartbeat.topic is required when heartbeat.interval > 0")
	}
	return nil
}
