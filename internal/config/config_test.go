package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeConfig(t, `
ami:
  host: 192.168.1.200
  port: 5038
  username: admin
  secret: s3cret
mqtt:
  broker: tcp://localhost:1883
  client_id: test
  topic_prefix: pbx
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AMI.Host != "192.168.1.200" {
		t.Errorf("expected host=192.168.1.200, got %s", cfg.AMI.Host)
	}
	if cfg.AMI.Addr() != "192.168.1.200:5038" {
		t.Errorf("expected addr=192.168.1.200:5038, got %s", cfg.AMI.Addr())
	}
	if cfg.MQTT.TopicPrefix != "pbx" {
		t.Errorf("expected topic_prefix=pbx, got %s", cfg.MQTT.TopicPrefix)
	}
}

func TestLoadDefaults(t *testing.T) {
	path := writeConfig(t, `
ami:
  username: admin
  secret: s3cret
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AMI.Host != "127.0.0.1" {
		t.Errorf("expected default host=127.0.0.1, got %s", cfg.AMI.Host)
	}
	if cfg.AMI.Port != 5038 {
		t.Errorf("expected default port=5038, got %d", cfg.AMI.Port)
	}
	if cfg.MQTT.Broker != "tcp://localhost:1883" {
		t.Errorf("expected default broker, got %s", cfg.MQTT.Broker)
	}
	if cfg.MQTT.ClientID != "asterisk-mqtt" {
		t.Errorf("expected default client_id, got %s", cfg.MQTT.ClientID)
	}
	if cfg.MQTT.TopicPrefix != "asterisk" {
		t.Errorf("expected default topic_prefix=asterisk, got %s", cfg.MQTT.TopicPrefix)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeConfig(t, `{{{invalid`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name   string
		config string
		errMsg string
	}{
		{"empty username", `
ami:
  secret: s3cret
`, "ami.username is required"},
		{"empty secret", `
ami:
  username: admin
`, "ami.secret is required"},
		{"port zero", `
ami:
  port: 0
  username: admin
  secret: s3cret
`, "ami.port must be between 1 and 65535, got 0"},
		{"port too high", `
ami:
  port: 70000
  username: admin
  secret: s3cret
`, "ami.port must be between 1 and 65535, got 70000"},
		{"empty host", `
ami:
  host: ""
  username: admin
  secret: s3cret
`, "ami.host is required"},
		{"empty broker", `
ami:
  username: admin
  secret: s3cret
mqtt:
  broker: ""
`, "mqtt.broker is required"},
		{"empty client_id", `
ami:
  username: admin
  secret: s3cret
mqtt:
  client_id: ""
`, "mqtt.client_id is required"},
		{"empty topic_prefix", `
ami:
  username: admin
  secret: s3cret
mqtt:
  topic_prefix: ""
`, "mqtt.topic_prefix is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.config)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if err.Error() != tt.errMsg {
				t.Errorf("expected error %q, got %q", tt.errMsg, err.Error())
			}
		})
	}
}
