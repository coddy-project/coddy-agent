//go:build cli

package tui

import (
	"sync"
	"time"
)

// loaderFrames are the braille spinner frames pi uses.
var loaderFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const loaderInterval = 80 * time.Millisecond

// Loader renders a braille spinner and message on one padded line preceded by
// a blank line (port of pi-tui Loader). The animation is stateless: Render
// derives the frame from elapsed time, and a ticker goroutine only requests
// renders, so no cross-goroutine mutation touches the component.
type Loader struct {
	mu sync.Mutex

	spinnerColorFn func(string) string
	messageColorFn func(string) string
	message        string
	startedAt      time.Time

	requestRender func()
	stopCh        chan struct{}
}

// NewLoader creates a Loader; requestRender is invoked on every frame tick.
func NewLoader(requestRender func(), spinnerColorFn, messageColorFn func(string) string, message string) *Loader {
	return &Loader{
		spinnerColorFn: spinnerColorFn,
		messageColorFn: messageColorFn,
		message:        message,
		startedAt:      time.Now(),
		requestRender:  requestRender,
	}
}

// Start begins the 80 ms tick that keeps renders flowing.
func (l *Loader) Start() {
	l.Stop()
	l.mu.Lock()
	l.startedAt = time.Now()
	stop := make(chan struct{})
	l.stopCh = stop
	l.mu.Unlock()
	go func() {
		ticker := time.NewTicker(loaderInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if l.requestRender != nil {
					l.requestRender()
				}
			}
		}
	}()
}

// Stop halts the animation ticker.
func (l *Loader) Stop() {
	l.mu.Lock()
	if l.stopCh != nil {
		close(l.stopCh)
		l.stopCh = nil
	}
	l.mu.Unlock()
}

// SetMessage replaces the loader message (UI goroutine).
func (l *Loader) SetMessage(message string) {
	l.mu.Lock()
	l.message = message
	l.mu.Unlock()
	if l.requestRender != nil {
		l.requestRender()
	}
}

// Invalidate is a no-op; frames are computed per render.
func (l *Loader) Invalidate() {}

// Render emits a leading blank line then the spinner line for the current
// time-derived frame.
func (l *Loader) Render(width int) []string {
	l.mu.Lock()
	frame := loaderFrames[int(time.Since(l.startedAt)/loaderInterval)%len(loaderFrames)]
	text := l.spinnerColorFn(frame) + " " + l.messageColorFn(l.message)
	l.mu.Unlock()
	line := NewText(text, 1, 0, nil)
	return append([]string{""}, line.Render(width)...)
}
