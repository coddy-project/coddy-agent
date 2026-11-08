package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
)

func TestModelFixtureServesAndShutsDown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { _ = listener.Close() }()
	stub := &llmstub.Server{Model: "coddy-demo"}
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- serveModel(ctx, listener, stub, &output) }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	origin := "http://" + listener.Addr().String()
	response, err := client.Get(origin + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"id":"coddy-demo"`) {
		t.Fatal("fixture model listing missing")
	}
	response, err = client.Post(origin+"/v1/chat/completions", "application/json", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "You said: hello") {
		t.Fatal("original greeting changed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not shut down")
	}
	if output.String() != "offline model fixture: 1 physical requests\n" {
		t.Fatalf("physical count missing: %q", output.String())
	}
	if _, err = client.Get(origin + "/v1/models"); err == nil {
		t.Fatal("fixture listener survived shutdown")
	}
}
