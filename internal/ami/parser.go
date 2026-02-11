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
