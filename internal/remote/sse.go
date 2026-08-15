package remote

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// sseFrame is one server-sent event: an optional event name and its data
// payload (multi-line data joined with newlines, per the SSE spec).
type sseFrame struct {
	event string
	data  string
}

// errStopStream tells readSSE to stop consuming without reporting an error.
var errStopStream = errors.New("stop stream")

// readSSE parses a text/event-stream body and invokes onFrame per event.
// Comment lines and id fields are skipped. It returns nil on EOF and
// propagates onFrame errors (except errStopStream, which reads as a clean
// stop).
func readSSE(r io.Reader, onFrame func(sseFrame) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var event string
	var data []string
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		frame := sseFrame{event: event, data: strings.Join(data, "\n")}
		event = ""
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
			default:
				// id: fields (watch-endpoint resume) and unknown fields are skipped
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
