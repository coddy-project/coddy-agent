package localipc

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxyForwardsBearerAuthAndFlushesStreamingResponses(t *testing.T) {
	home := t.TempDir()
	owner, err := Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- owner.Serve(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer public-token" {
				t.Errorf("Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			_, _ = io.WriteString(w, "data: first\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, "data: second\n\n")
			flusher.Flush()
		}))
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client, err := Connect(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	bridge := httptest.NewServer(client.ProxyHandler())
	defer bridge.Close()
	req, _ := http.NewRequest(http.MethodGet, bridge.URL+"/events", nil)
	req.Header.Set("Authorization", "Bearer public-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	reader := bufio.NewReader(res.Body)
	for _, want := range []string{"data: first", "data: second"} {
		line := make(chan string, 1)
		go func() {
			for {
				text, _ := reader.ReadString('\n')
				text = strings.TrimSpace(text)
				if text != "" {
					line <- text
					return
				}
			}
		}()
		select {
		case got := <-line:
			if got != want {
				t.Fatalf("frame = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s was buffered", want)
		}
	}
}
