package httpx

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewServerAppliesBounds(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout != ReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, ReadHeaderTimeout)
	}
	if srv.IdleTimeout != IdleTimeout {
		t.Fatalf("IdleTimeout = %v, want %v", srv.IdleTimeout, IdleTimeout)
	}
	if srv.MaxHeaderBytes != MaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, MaxHeaderBytes)
	}
}

// A whole-request deadline would truncate the streaming responses coddy exists
// to serve, so their absence is a decision worth pinning down.
func TestNewServerLeavesStreamingDeadlinesUnset(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler())
	if srv.ReadTimeout != 0 {
		t.Fatalf("ReadTimeout = %v, want it unset so long uploads survive", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v, want it unset so SSE and permission waits survive", srv.WriteTimeout)
	}
}

// The point of the header bound is that a peer which opens a connection and
// then says nothing is eventually dropped instead of pinning a goroutine.
func TestServerDropsAClientThatNeverFinishesItsHeaders(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ln.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	srv.ReadHeaderTimeout = 150 * time.Millisecond
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// A request line and one header, then silence: the headers never end.
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(conn)
	if err != nil && !strings.Contains(err.Error(), "reset by peer") {
		t.Fatalf("expected the server to hang up, got %v", err)
	}
	if len(data) > 0 && !strings.Contains(string(data), "408") {
		t.Fatalf("expected a timeout response or a closed connection, got %q", data)
	}
}

// A normal request still works, and a streaming handler is still able to flush
// chunks over a long period without the server cutting it short.
func TestServerKeepsStreamingResponsesAlive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ln.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("no flusher")
			return
		}
		for i := 0; i < 3; i++ {
			_, _ = fmt.Fprintf(w, "data: %d\n\n", i)
			fl.Flush()
			time.Sleep(120 * time.Millisecond)
		}
	}))
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("GET /stream HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	var seen int
	for seen < 3 {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("stream cut short after %d chunks: %v", seen, err)
		}
		if strings.HasPrefix(line, "data: ") {
			seen++
		}
	}
}
