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
