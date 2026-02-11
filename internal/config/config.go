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
	return nil
}
