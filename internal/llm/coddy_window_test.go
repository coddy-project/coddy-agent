package llm

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A busy answer that carries a window code is waited out like any busy
// answer; when the budget ends on one, the error says it was a window and not
// a slot.
func TestSpentBusyWaitNamesTheWindowItEndedOn(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"", "no free stream slot"},
		{"unheard_of", "no free stream slot"},
		{WireCodeRateWindow, "calls per minute"},
		{WireCodeClientStreams, "concurrent calls"},
		{WireCodeClientRate, "calls per minute"},
	}
	for _, tc := range cases {
		err := &coddyBusyError{waited: 30 * time.Second, budget: 30 * time.Second, requests: 9, code: tc.code}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("code %q: %q does not say %q", tc.code, err.Error(), tc.want)
		}
		if strings.Contains(err.Error(), "waited 30s") == false {
			t.Errorf("code %q: the wait is not reported: %q", tc.code, err.Error())
		}
	}
	// A slot of a client is a slot: only a window is waited for as a window.
	slot := &coddyBusyError{waited: 30 * time.Second, budget: 30 * time.Second, requests: 3, code: WireCodeClientStreams}
	if strings.Contains(slot.Error(), "window") {
		t.Errorf("a client's slot limit reads as a window: %q", slot.Error())
	}
	for _, code := range []string{WireCodeRateWindow, WireCodeClientRate} {
		w := &coddyBusyError{waited: 30 * time.Second, budget: 30 * time.Second, requests: 3, code: code}
		if !strings.Contains(w.Error(), "free window") {
			t.Errorf("code %q does not read as a window: %q", code, w.Error())
		}
	}
	off := &coddyBusyError{budget: 0, code: WireCodeRateWindow}
	if !strings.Contains(off.Error(), "calls per minute") || !strings.Contains(off.Error(), "busy_wait_ms") {
		t.Errorf("waiting turned off: %q", off.Error())
	}
}

func TestBusyWaitReadsTheCodeOfTheLastRefusal(t *testing.T) {
	fc := newFakeBusyClock()
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy, Code: WireCodeRateWindow, RetryAfterS: 1}, "Retry-After", "1")
	})
	got := streamOnce(busyChain(t, remote, fc, 0, 0, 3), userMsg("Hi"), nil)
	var busy *coddyBusyError
	if !errors.As(got.err, &busy) || busy.code != WireCodeRateWindow {
		t.Fatalf("err %v, want a busy error that carries rate_window", got.err)
	}
	if !strings.Contains(got.err.Error(), "calls per minute") {
		t.Fatalf("message %q does not name the window", got.err)
	}
}
