// coddy-browser-host is an optional local input adapter for Firefox. It has
// no network listener and accepts only the registered extension as its caller.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strings"
)

type request struct {
	Action      string  `json:"action"`
	Title       string  `json:"title"`
	Text        string  `json:"text"`
	Key         string  `json:"key"`
	Clear       bool    `json:"clear"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	ToX         float64 `json:"to_x"`
	ToY         float64 `json:"to_y"`
	DX          float64 `json:"dx"`
	DY          float64 `json:"dy"`
	OriginX     float64 `json:"origin_x"`
	OriginY     float64 `json:"origin_y"`
	Scale       float64 `json:"scale"`
	ScreenScale float64 `json:"screen_scale"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
	HasFocus    bool    `json:"has_focus"`
}

// Native events can reach browser chrome and other applications. Admit only
// page coordinates and editing shortcuts, and require the document to own focus.
func validateInput(r request) error {
	point := func(x, y float64) bool {
		return !math.IsNaN(x) && !math.IsNaN(y) && r.Width > 0 && r.Height > 0 && x >= 0 && y >= 0 && x < r.Width && y < r.Height
	}
	switch r.Action {
	case "click", "scroll", "drag":
		if !point(r.X, r.Y) || (r.Action == "drag" && !point(r.ToX, r.ToY)) {
			return fmt.Errorf("native coordinates must be inside the page viewport")
		}
	case "type", "press":
		if !r.HasFocus {
			return fmt.Errorf("click the page before keyboard input")
		}
	default:
		return fmt.Errorf("unsupported native input action")
	}
	if r.Action != "press" {
		return nil
	}
	parts := strings.Split(r.Key, "+")
	name := parts[len(parts)-1]
	ctrl, shift, alt := false, false, false
	for _, modifier := range parts[:len(parts)-1] {
		switch modifier {
		case "Ctrl", "Control":
			ctrl = true
		case "Shift":
			shift = true
		case "Alt":
			alt = true
		default:
			return fmt.Errorf("unsupported native editing shortcut")
		}
	}
	if alt {
		if ctrl && !shift && len(name) == 1 && name[0] >= '0' && name[0] <= '6' {
			return nil
		}
		return fmt.Errorf("unsupported native editing shortcut")
	}
	if ctrl && len(name) == 1 && strings.Contains("ABCIKUVXYZ", strings.ToUpper(name)) {
		return nil
	}
	switch name {
	case "Enter", "Backspace", "Delete", "ArrowLeft", "ArrowUp", "ArrowRight", "ArrowDown", "Home", "End":
		return nil
	case "Tab", "Escape", "Space":
		if !ctrl {
			return nil
		}
	}
	return fmt.Errorf("unsupported native editing shortcut")
}

func readRequest(r io.Reader) (request, error) {
	var size uint32
	if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
		return request{}, err
	}
	if size == 0 || size > 128<<10 {
		return request{}, fmt.Errorf("invalid native message length")
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return request{}, err
	}
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		return request{}, err
	}
	return req, nil
}

func writeReply(w io.Writer, result map[string]any) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(data))); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func main() {
	// Firefox passes its stable extension ID as the second argument after the
	// native-host manifest path. Do not accept an arbitrary native caller.
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "browser@coddy.dev" {
		os.Exit(1)
	}
	req, err := readRequest(os.Stdin)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Disconnecting the native port closes stdin. Cancel input in progress,
	// including long text and drags, before accepting another input event.
	go func() { _, _ = readRequest(os.Stdin); cancel() }()
	result := map[string]any{"ok": true, "input": "native"}
	if runtime.GOOS != "windows" {
		result = map[string]any{"error": "native Firefox input currently requires Windows"}
	} else if req.Action != "ping" {
		if err := execute(ctx, req); err != nil {
			result = map[string]any{"error": err.Error()}
		}
	}
	_ = writeReply(os.Stdout, result)
}
