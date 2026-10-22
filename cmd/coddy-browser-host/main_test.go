package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNativeFraming(t *testing.T) {
	var b bytes.Buffer
	if err := writeReply(&b, map[string]any{"action": "ping"}); err != nil {
		t.Fatal(err)
	}
	r, err := readRequest(&b)
	if err != nil || r.Action != "ping" {
		t.Fatalf("round trip: %+v %v", r, err)
	}
	for _, size := range []uint32{0, 1 << 20} {
		b.Reset()
		_ = binary.Write(&b, binary.LittleEndian, size)
		if _, err := readRequest(&b); err == nil {
			t.Fatal("invalid message accepted")
		}
	}
}

func TestNativeInputStaysInsidePage(t *testing.T) {
	base := request{Action: "click", X: 10, Y: 20, Width: 800, Height: 600, HasFocus: true}
	if err := validateInput(base); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*request){
		func(r *request) { r.X = -1 },
		func(r *request) { r.Y = 600 },
		func(r *request) { r.Action, r.ToX, r.ToY = "drag", 900, 20 },
		func(r *request) { r.Action, r.HasFocus = "type", false },
		func(r *request) { r.Action, r.Key = "press", "Ctrl+L" },
		func(r *request) { r.Action, r.Key = "press", "Alt+Tab" },
		func(r *request) { r.Action, r.Key = "press", "Meta+R" },
	} {
		r := base
		change(&r)
		if err := validateInput(r); err == nil {
			t.Errorf("accepted input outside the page: %+v", r)
		}
	}
	for _, key := range []string{"Ctrl+A", "Control+Shift+Z", "Shift+Enter", "ArrowLeft", "Ctrl+Alt+1"} {
		r := base
		r.Action, r.Key = "press", key
		if err := validateInput(r); err != nil {
			t.Errorf("editing key %s: %v", key, err)
		}
	}
}
