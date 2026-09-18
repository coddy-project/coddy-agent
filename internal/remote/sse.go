package remote

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// sseFrame is one server-sent event: an optional event name and its data
// payload (multi-line data joined with newlines, per the SSE spec), and the id
// a relay numbers its frames with (0 when the frame carries none), which is
// what a reader resumes after.
type sseFrame struct {
	event string
	data  string
	id    uint64
}

// errStopStream tells readSSE to stop consuming without reporting an error.
var errStopStream = errors.New("stop stream")

// readSSE parses a text/event-stream body and invokes onFrame per event.
// Comment lines and unknown fields are skipped. It returns nil on EOF and
// propagates onFrame errors (except errStopStream, which reads as a clean
// stop).
func readSSE(r io.Reader, onFrame func(sseFrame) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var event string
	var data []string
	var id uint64
	flush := func() error {
		if len(data) == 0 {
			event, id = "", 0
			return nil
		}
		frame := sseFrame{event: event, data: strings.Join(data, "\n"), id: id}
		event, id = "", 0
		data = nil
		return onFrame(frame)
	}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if ferr := flush(); ferr != nil {
					if errors.Is(ferr, errStopStream) {
						return nil
					}
					return ferr
				}
			case strings.HasPrefix(line, ":"):
				// keepalive comment
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			case strings.HasPrefix(line, "id:"):
				// The relay's frame sequence; anything else in the field is no id.
				id, _ = strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64)
			default:
				// unknown fields (age:, retry:) are skipped
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Per the SSE spec an event not yet terminated by a blank
				// line is discarded at EOF; flushing it would let a
				// truncated [DONE] pass for a clean completion.
				return nil
			}
			return err
		}
	}
}
