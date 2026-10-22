package browser

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

const testKey = "0123456789abcdef0123456789abcdef"

func TestListenerShutdownCancelsChatRequests(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	_ = probe.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, stopped := make(chan struct{}), make(chan struct{})
	s := NewServer(NewHub(), testKey)
	s.Chat = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	})
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, address, "", "") }()
	client := &http.Client{Timeout: 10 * time.Second}
	requestCtx, requestCancel := context.WithCancel(context.Background())
	defer requestCancel()
	go func() {
		for i := 0; i < 100; i++ {
			r, _ := http.NewRequestWithContext(requestCtx, "GET", "http://"+address+"/browser/v1/chat/coddy/sessions", nil)
			r.Header.Set("Authorization", "Bearer "+testKey)
			response, err := client.Do(r)
			if err == nil {
				_ = response.Body.Close()
				return
			}
			if requestCtx.Err() != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("chat did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("old listener kept a chat request alive")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExtensionWebsocket(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(NewServer(hub, testKey))
	defer srv.Close()
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/browser/v1/connect", "", "chrome-extension://test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	_ = ws.SetDeadline(time.Now().Add(3 * time.Second))
	if err := websocket.JSON.Send(ws, map[string]any{"type": "hello", "version": 1, "token": testKey, "id": "browser-test", "name": "Chrome", "capabilities": []string{"snapshot"}}); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := websocket.JSON.Receive(ws, &ready); err != nil || ready["type"] != "ready" {
		t.Fatalf("registration: %v %+v", err, ready)
	}
	_ = websocket.JSON.Send(ws, map[string]any{"type": "tabs", "tabs": []Tab{{ID: 2, URL: "https://example.com"}}})
	for len(hub.List()[0].Tabs) == 0 {
		time.Sleep(time.Millisecond)
	}
	result := make(chan error, 1)
	go func() {
		_, err := hub.Call(context.Background(), "session", "browser-test", Command{Action: "snapshot", TabID: 2})
		result <- err
	}()
	var cmd Command
	if err := websocket.JSON.Receive(ws, &cmd); err != nil {
		t.Fatal(err)
	}
	_ = websocket.JSON.Send(ws, map[string]any{"type": "reply", "id": cmd.ID, "result": json.RawMessage(`{"text":"hello"}`)})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestListenerAuthAndOrigins(t *testing.T) {
	s := NewServer(NewHub(), testKey)
	for _, tc := range []struct {
		origin, key string
		status      int
	}{
		{"chrome-extension://test", testKey, 200}, {"moz-extension://test", testKey, 200},
		{"https://evil.example", testKey, 403}, {"null", testKey, 403}, {"chrome-extension://test", "wrong", 401},
	} {
		r := httptest.NewRequest("GET", "/browser/v1/status", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Authorization", "Bearer "+tc.key)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("origin %q: %d", tc.origin, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/coddy/config", nil)
	r.Header.Set("Authorization", "Bearer "+testKey)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatal("main API reachable")
	}
}

func TestFileTicketsAreSingleUse(t *testing.T) {
	id, cleanup, err := StageFile("a", []byte("hello"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	s := NewServer(NewHub(), testKey)
	for i, want := range []int{200, 410} {
		r := httptest.NewRequest("GET", "/browser/v1/files/"+id, nil)
		r.Header.Set("Authorization", "Bearer "+testKey)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("read %d: %d", i, w.Code)
		}
	}
}
