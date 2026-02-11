package correlator

import (
	"time"

	"github.com/sweeney/asterisk-mqtt/internal/ami"
)

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
