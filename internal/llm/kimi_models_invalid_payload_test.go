package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKimiModelsRejectsInvalidData(t *testing.T) {
	for _, payload := range []string{`{"data":{}}`, `{"data":null}`, `{"error":"bad"}`} {
		t.Run(payload, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(payload))
			}))
			defer server.Close()
			if _, err := fetchKimiModels(context.Background(), "subscription", "synthetic-token", server.URL, server.Client()); err == nil {
				t.Fatal("invalid catalog data was accepted")
			}
		})
	}
}
