package ami

import (
	"strconv"
	"time"
)

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
