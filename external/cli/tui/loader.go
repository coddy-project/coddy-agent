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
// a blank line (port of pi-tui Loader).
type Loader struct {
	mu sync.Mutex

	text           *Text
	spinnerColorFn func(string) string
	messageColorFn func(string) string
	message        string
	frame          int

	requestRender func()
	stopCh        chan struct{}
}

// NewLoader creates a Loader; requestRender is invoked on every frame tick.
func NewLoader(requestRender func(), spinnerColorFn, messageColorFn func(string) string, message string) *Loader {
	l := &Loader{
		text:           NewText("", 1, 0, nil),
		spinnerColorFn: spinnerColorFn,
		messageColorFn: messageColorFn,
		message:        message,
		requestRender:  requestRender,
	}
	l.updateDisplay()
	return l
}

// Start begins the 80 ms frame animation.
func (l *Loader) Start() {
	l.Stop()
	l.mu.Lock()
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
				l.mu.Lock()
				l.frame = (l.frame + 1) % len(loaderFrames)
				l.mu.Unlock()
				l.updateDisplay()
			}
		}
	}()
}

// Stop halts the animation.
func (l *Loader) Stop() {
	l.mu.Lock()
	if l.stopCh != nil {
		close(l.stopCh)
		l.stopCh = nil
	}
	l.mu.Unlock()
}

// SetMessage replaces the loader message.
func (l *Loader) SetMessage(message string) {
	l.mu.Lock()
	l.message = message
	l.mu.Unlock()
	l.updateDisplay()
}

func (l *Loader) updateDisplay() {
	l.mu.Lock()
	frame := loaderFrames[l.frame]
	text := l.spinnerColorFn(frame) + " " + l.messageColorFn(l.message)
	l.mu.Unlock()
	l.text.SetText(text)
	if l.requestRender != nil {
		l.requestRender()
	}
}

// Invalidate clears the cached text render.
func (l *Loader) Invalidate() { l.text.Invalidate() }

// Render emits a leading blank line then the spinner line.
func (l *Loader) Render(width int) []string {
	return append([]string{""}, l.text.Render(width)...)
}
