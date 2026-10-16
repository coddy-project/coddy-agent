//go:build swarm

package swarm

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// A labelled node's 401 is silenced; a labelled node that sends the 401 header and then stalls its body is a failure, not a refusal,
// and the read that did not finish is reported like any other.
func TestALabelledNodeThatStallsAfterItsRefusalStillWarns(t *testing.T) {
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer stall.Close()
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer refuse.Close()

	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = fullToken
	cfg.Swarm.FanoutTimeoutSeconds = 1
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{swarmdto.LabelTokenClass: swarmdto.TokenClassSharedModels}
	for name, url := range map[string]string{"stalled": stall.URL, "refused": refuse.URL} {
		if _, err := srv.registry.Register(swarmdto.RegisterRequest{
			Name: name, Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
			AdvertiseURL: url, InstanceUUID: "uuid-" + name, Token: "node-secret", Labels: labels,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	res, body := do(t, http.MethodGet, ts.URL+"/swarm/sessions", fullToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	var out struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	has := func(prefix string) bool {
		for _, w := range out.Warnings {
			if strings.HasPrefix(w, prefix) {
				return true
			}
		}
		return false
	}
	if !has("stalled:") {
		t.Errorf("a labelled node that stalled after its 401 raised no warning: %v", out.Warnings)
	}
	if has("refused:") {
		t.Errorf("a labelled node's plain 401 warned: %v", out.Warnings)
	}
}
