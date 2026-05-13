//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestAdminProviderCRUD(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:              config.Paths{Home: home, CWD: "/tmp"},
		Models:             []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:              config.Agent{Model: "openai/gpt-4o"},
		Providers:          []config.ProviderConfig{{Name: "static", Type: "openai", APIBase: "https://api.openai.com", APIKey: "static-key"}},
		RuntimeOverlay:     &config.RuntimeOverlay{},
		RuntimeOverlayPath: filepath.Join(home, "ui-config.yaml"),
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// POST a new provider runtime1. Expect 201.
	postBody := `{"name":"runtime1","type":"openai","api_base":"https://api.openai.com","api_key":"secret123"}`
	res, err := http.Post(ts.URL+"/coddy/admin/providers", "application/json", strings.NewReader(postBody))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST status %d body %s", res.StatusCode, b)
	}
	var created adminProvider
	if err := json.Unmarshal(b, &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "runtime1" || created.Type != "openai" || created.APIBase != "https://api.openai.com" {
		t.Fatalf("unexpected created provider %+v", created)
	}
	if created.APIKey != "...t123" {
		t.Fatalf("expected masked key ...t123, got %q", created.APIKey)
	}

	// GET list. Expect 1 item, key masked.
	res, err = http.Get(ts.URL + "/coddy/admin/providers")
	if err != nil {
		t.Fatal(err)
	}
	b, err = ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET status %d body %s", res.StatusCode, b)
	}
	var list []adminProvider
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(list))
	}
	if list[0].APIKey != "...t123" {
		t.Fatalf("expected masked key ...t123 in list, got %q", list[0].APIKey)
	}

	// PUT runtime1 with empty api_key. Expect key preserved.
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/coddy/admin/providers/runtime1", strings.NewReader(`{"name":"runtime1","type":"openai","api_base":"https://api.openai.com","api_key":""}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err = ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT empty key status %d body %s", res.StatusCode, b)
	}
	var updated adminProvider
	if err := json.Unmarshal(b, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.APIKey != "...t123" {
		t.Fatalf("expected preserved masked key ...t123, got %q", updated.APIKey)
	}

	// PUT runtime1 with new api_key. Expect key updated (verify via masked response).
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/coddy/admin/providers/runtime1", strings.NewReader(`{"name":"runtime1","type":"openai","api_base":"https://api.openai.com","api_key":"newsecret456"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err = ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT new key status %d body %s", res.StatusCode, b)
	}
	if err := json.Unmarshal(b, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.APIKey != "...t456" {
		t.Fatalf("expected updated masked key ...t456, got %q", updated.APIKey)
	}

	// POST duplicate name static or runtime1. Expect 400/409.
	res, err = http.Post(ts.URL+"/coddy/admin/providers", "application/json", strings.NewReader(`{"name":"static","type":"openai","api_base":"https://api.openai.com","api_key":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusConflict {
		t.Fatalf("expected 400/409 for duplicate, got %d body %s", res.StatusCode, b)
	}

	res, err = http.Post(ts.URL+"/coddy/admin/providers", "application/json", strings.NewReader(`{"name":"runtime1","type":"openai","api_base":"https://api.openai.com","api_key":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusConflict {
		t.Fatalf("expected 400/409 for duplicate runtime1, got %d body %s", res.StatusCode, b)
	}

	// DELETE runtime1. Expect 204.
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/coddy/admin/providers/runtime1", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status %d body %s", res.StatusCode, b)
	}

	// DELETE non-existent provider. Expect 404.
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/coddy/admin/providers/nonexistent", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE non-existent status %d body %s", res.StatusCode, b)
	}

	// GET list. Expect 0 items.
	res, err = http.Get(ts.URL + "/coddy/admin/providers")
	if err != nil {
		t.Fatal(err)
	}
	b, err = ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET after delete status %d body %s", res.StatusCode, b)
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 providers after delete, got %d", len(list))
	}
}

func TestAdminModelCRUD(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:              config.Paths{Home: home, CWD: "/tmp"},
		Models:             []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:              config.Agent{Model: "openai/gpt-4o"},
		Providers:          []config.ProviderConfig{{Name: "openai", Type: "openai", APIBase: "https://api.openai.com", APIKey: "static-key"}},
		RuntimeOverlay:     &config.RuntimeOverlay{},
		RuntimeOverlayPath: filepath.Join(home, "ui-config.yaml"),
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Create a runtime provider first so the model has a valid provider.
	res, err := http.Post(ts.URL+"/coddy/admin/providers", "application/json", strings.NewReader(`{"name":"runtime","type":"openai","api_base":"https://api.openai.com","api_key":"rt"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST provider status %d body %s", res.StatusCode, b)
	}

	// POST a runtime model runtime/gpt-4o-mini. Expect 201.
	postBody := `{"model":"runtime/gpt-4o-mini","max_tokens":200,"temperature":0.5,"max_context_tokens":4000}`
	res, err = http.Post(ts.URL+"/coddy/admin/models", "application/json", strings.NewReader(postBody))
	if err != nil {
		t.Fatal(err)
	}
	b, err = ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST status %d body %s", res.StatusCode, b)
	}
	var created adminModel
	if err := json.Unmarshal(b, &created); err != nil {
		t.Fatal(err)
	}
	if created.Model != "runtime/gpt-4o-mini" || created.MaxTokens != 200 || created.Temperature != 0.5 || created.MaxContextTokens != 4000 {
		t.Fatalf("unexpected created model %+v", created)
	}

	// POST a model with unknown provider. Expect 400.
	res, err = http.Post(ts.URL+"/coddy/admin/models", "application/json", strings.NewReader(`{"model":"bad/gpt-4","max_tokens":100}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d body %s", res.StatusCode, b)
	}

	// POST duplicate of static model openai/gpt-4o. Expect 409.
	res, err = http.Post(ts.URL+"/coddy/admin/models", "application/json", strings.NewReader(`{"model":"openai/gpt-4o","max_tokens":100}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate, got %d body %s", res.StatusCode, b)
	}

	// POST with empty model field. Expect 400.
	res, err = http.Post(ts.URL+"/coddy/admin/models", "application/json", strings.NewReader(`{"model":"","max_tokens":100}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty model, got %d body %s", res.StatusCode, b)
	}

	// GET list. Expect 1 item.
	res, err = http.Get(ts.URL + "/coddy/admin/models")
	if err != nil {
		t.Fatal(err)
	}
	b, err = ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET status %d body %s", res.StatusCode, b)
	}
	var list []adminModel
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 model, got %d", len(list))
	}
	if list[0].Model != "runtime/gpt-4o-mini" {
		t.Fatalf("unexpected model %q", list[0].Model)
	}

	// PUT runtime/gpt-4o-mini with new max_tokens. Expect 200.
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/coddy/admin/models/runtime/gpt-4o-mini", strings.NewReader(`{"model":"runtime/gpt-4o-mini","max_tokens":999,"temperature":0.7}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err = ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT status %d body %s", res.StatusCode, b)
	}
	var updated adminModel
	if err := json.Unmarshal(b, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.MaxTokens != 999 || updated.Temperature != 0.7 {
		t.Fatalf("unexpected updated model %+v", updated)
	}

	// PUT runtime/gpt-4o-mini renaming to existing static model. Expect 409.
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/coddy/admin/models/runtime/gpt-4o-mini", strings.NewReader(`{"model":"openai/gpt-4o","max_tokens":100}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for rename to existing model, got %d body %s", res.StatusCode, b)
	}

	// PUT runtime/gpt-4o-mini with unknown provider. Expect 400.
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/coddy/admin/models/runtime/gpt-4o-mini", strings.NewReader(`{"model":"bad/gpt-4","max_tokens":100}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider on PUT, got %d body %s", res.StatusCode, b)
	}

	// DELETE runtime/gpt-4o-mini. Expect 204.
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/coddy/admin/models/runtime/gpt-4o-mini", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status %d body %s", res.StatusCode, b)
	}

	// GET list. Expect 0 items.
	res, err = http.Get(ts.URL + "/coddy/admin/models")
	if err != nil {
		t.Fatal(err)
	}
	b, err = ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET after delete status %d body %s", res.StatusCode, b)
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 models after delete, got %d", len(list))
	}

	// DELETE non-existent model. Expect 404.
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/coddy/admin/models/nonexistent/model", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE non-existent status %d body %s", res.StatusCode, b)
	}

	// PUT non-existent model. Expect 404.
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/coddy/admin/models/nonexistent/model", strings.NewReader(`{"model":"nonexistent/model","max_tokens":100}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT non-existent status %d body %s", res.StatusCode, b)
	}
}
