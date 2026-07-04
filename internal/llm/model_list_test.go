package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModelsOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("openai path = %q, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want Bearer sk-test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// Includes a duplicate id and an out-of-order id to exercise dedupe + sort.
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini","name":"GPT-4o mini"},{"id":"gpt-4o"},{"id":"gpt-4o"}]}`))
	}))
	defer srv.Close()

	got, err := ListModels(context.Background(), ProviderInput{Type: "openai", APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2 (deduped): %+v", len(got), got)
	}
	if got[0].ID != "gpt-4o" || got[1].ID != "gpt-4o-mini" {
		t.Errorf("sorted ids = %+v, want [gpt-4o gpt-4o-mini]", got)
	}
	if got[1].Name != "GPT-4o mini" {
		t.Errorf("name = %q, want GPT-4o mini", got[1].Name)
	}
}

func TestListModelsAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("anthropic path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "ant-key" {
			t.Errorf("x-api-key = %q, want ant-key", got)
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-3-5-sonnet","display_name":"Claude 3.5 Sonnet"}]}`))
	}))
	defer srv.Close()

	got, err := ListModels(context.Background(), ProviderInput{Type: "anthropic", APIKey: "ant-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 1 || got[0].ID != "claude-3-5-sonnet" {
		t.Fatalf("got %+v, want one claude-3-5-sonnet", got)
	}
	if got[0].Name != "Claude 3.5 Sonnet" {
		t.Errorf("display_name not used as name: %q", got[0].Name)
	}
}

func TestListModelsNon2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	if _, err := ListModels(context.Background(), ProviderInput{Type: "openai", APIKey: "x", BaseURL: srv.URL}); err == nil {
		t.Fatal("expected error on HTTP 401")
	}
}

func TestListModelsNonJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	if _, err := ListModels(context.Background(), ProviderInput{Type: "openai", BaseURL: srv.URL}); err == nil {
		t.Fatal("expected decode error on non-JSON body")
	}
}

func TestListModelsUnsupportedType(t *testing.T) {
	_, err := ListModels(context.Background(), ProviderInput{Type: "cohere", BaseURL: "http://example.invalid"})
	var ue *UnsupportedProviderError
	if !errors.As(err, &ue) {
		t.Fatalf("want UnsupportedProviderError, got %v", err)
	}
}
