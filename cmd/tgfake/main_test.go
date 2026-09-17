package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake/llmstub"
)

func TestNewMux_ServesFakeAndModel(t *testing.T) {
	fake := tgfake.New(tgfake.Options{MaxPollWait: 100 * time.Millisecond})
	srv := httptest.NewServer(newMux(fake, &llmstub.Server{}))
	defer func() {
		fake.Close()
		srv.Close()
	}()
	for _, path := range []string{"/", "/bot1/getMe", "/v1/models"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
	bare := httptest.NewServer(newMux(fake, nil))
	defer bare.Close()
	resp, err := http.Get(bare.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("without --llm, /v1/models = %d", resp.StatusCode)
	}
}
