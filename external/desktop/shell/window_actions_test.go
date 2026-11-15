//go:build desktop

package shell

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type blockingResizeWindow struct {
	started chan struct{}
	release chan struct{}

	mu        sync.Mutex
	active    int
	maxActive int
	once      sync.Once
}

func newBlockingResizeWindow() *blockingResizeWindow {
	return &blockingResizeWindow{started: make(chan struct{}), release: make(chan struct{})}
}

func (*blockingResizeWindow) Supported() bool          { return true }
func (*blockingResizeWindow) Undecorate() error        { return nil }
func (*blockingResizeWindow) Decorate() error          { return nil }
func (*blockingResizeWindow) Minimise() error          { return nil }
func (*blockingResizeWindow) ToggleMaximise() error    { return nil }
func (*blockingResizeWindow) Close() error             { return nil }
func (*blockingResizeWindow) BeginMove(_, _ int) error { return nil }
func (*blockingResizeWindow) MoveBy(_, _ int) error    { return nil }
func (*blockingResizeWindow) BeginResize() error       { return nil }
func (*blockingResizeWindow) WaitForWindow(time.Duration) bool {
	return true
}

func (w *blockingResizeWindow) ResizeBy(_ string, _, _ int) error {
	w.mu.Lock()
	w.active++
	if w.active > w.maxActive {
		w.maxActive = w.active
	}
	w.mu.Unlock()
	w.once.Do(func() { close(w.started) })
	<-w.release
	w.mu.Lock()
	w.active--
	w.mu.Unlock()
	return nil
}

func (w *blockingResizeWindow) peakConcurrency() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.maxActive
}

func windowActionRequest(client *http.Client, url, action string) *http.Response {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(`{"action":"`+action+`","direction":"se","x":20,"y":10}`))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	return res
}

func TestMoveEndWaitsForTheResizeInFlight(t *testing.T) {
	window := newBlockingResizeWindow()
	server := httptest.NewServer(Handler(window))
	defer server.Close()
	released := false
	defer func() {
		if !released {
			close(window.release)
		}
	}()
	url := server.URL + Prefix + windowAction
	if res := windowActionRequest(server.Client(), url, "resize-begin"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("resize-begin answered %s", res.Status)
	}

	firstDone := make(chan *http.Response, 1)
	go func() { firstDone <- windowActionRequest(server.Client(), url, "resize-by") }()
	select {
	case <-window.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the first resize did not reach the window")
	}
	endDone := make(chan *http.Response, 1)
	go func() { endDone <- windowActionRequest(server.Client(), url, "move-end") }()

	select {
	case res := <-endDone:
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("move-end completed while a resize was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(window.release)
	released = true
	for name, done := range map[string]<-chan *http.Response{"resize": firstDone, "move-end": endDone} {
		select {
		case res := <-done:
			if err := res.Body.Close(); err != nil {
				t.Error(err)
			}
			if res.StatusCode != http.StatusNoContent {
				t.Errorf("%s answered %s", name, res.Status)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s did not finish", name)
		}
	}
}

func TestConcurrentResizeRequestsAreSerialized(t *testing.T) {
	window := newBlockingResizeWindow()
	server := httptest.NewServer(Handler(window))
	defer server.Close()
	released := false
	defer func() {
		if !released {
			close(window.release)
		}
	}()
	url := server.URL + Prefix + windowAction
	if res := windowActionRequest(server.Client(), url, "resize-begin"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("resize-begin answered %s", res.Status)
	}
	firstDone := make(chan *http.Response, 1)
	go func() { firstDone <- windowActionRequest(server.Client(), url, "resize-by") }()
	select {
	case <-window.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the first resize did not reach the window")
	}
	secondDone := make(chan *http.Response, 1)
	go func() { secondDone <- windowActionRequest(server.Client(), url, "resize-by") }()
	close(window.release)
	released = true
	for name, done := range map[string]<-chan *http.Response{"first resize": firstDone, "second resize": secondDone} {
		select {
		case res := <-done:
			if err := res.Body.Close(); err != nil {
				t.Error(err)
			}
			if res.StatusCode != http.StatusNoContent {
				t.Errorf("%s answered %s", name, res.Status)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s did not finish", name)
		}
	}
	if got := window.peakConcurrency(); got != 1 {
		t.Fatalf("ResizeBy ran %d times at once; the gesture requests were not serialized", got)
	}
}
