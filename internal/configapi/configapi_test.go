package configapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// One live configuration keeps one revision; a revision this process never issued -
// from before a restart, or made up - names nothing, and neither does one older than
// the configurations kept, so the save falls back to the live configuration.
func TestRevisions(t *testing.T) {
	served := NewRevisions()
	first := &config.Config{}
	rev := served.Revision(first)
	if again := served.Revision(first); again != rev {
		t.Fatalf("one configuration got two revisions: %q and %q", rev, again)
	}
	if got := served.Lookup(rev); got != first {
		t.Fatalf("lookup(%q) = %p, want the configuration served under it", rev, got)
	}
	if got := NewRevisions().Lookup(rev); got != nil {
		t.Fatalf("another process resolved %q", rev)
	}
	for _, unknown := range []string{"", "made-up", rev + "0"} {
		if got := served.Lookup(unknown); got != nil {
			t.Fatalf("lookup(%q) resolved to %p", unknown, got)
		}
	}
	for i := 0; i < RevisionsKept; i++ {
		served.Revision(&config.Config{})
	}
	if got := served.Lookup(rev); got != nil {
		t.Fatalf("a revision older than the %d kept still resolves", RevisionsKept)
	}
}

// saveBackend serves a config.yaml in a temporary home through the four routes,
// installing what a save loads back the way an agent does.
func saveBackend(t *testing.T, revisions *Revisions) (*http.ServeMux, func() *config.Config) {
	t.Helper()
	dir := t.TempDir()
	paths := config.Paths{Home: dir, CWD: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	if err := os.WriteFile(paths.ConfigPath, []byte("agent:\n  max_turns: 40\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	get := func() *config.Config {
		mu.Lock()
		defer mu.Unlock()
		return live
	}
	b := &Backend{
		Live: get,
		Install: func(c *config.Config) error {
			mu.Lock()
			defer mu.Unlock()
			live = c
			return nil
		},
		Schema:    config.UISchemaJSON,
		Revisions: revisions,
	}
	mux := http.NewServeMux()
	b.Register(mux)
	return mux, get
}

func readDocument(t *testing.T, mux *http.ServeMux) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/coddy/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /coddy/config: %d %s", rec.Code, rec.Body.String())
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func putDocument(t *testing.T, mux *http.ServeMux, doc interface{}) (int, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/coddy/config", bytes.NewReader(body)))
	var answer map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("PUT answer %q: %v", rec.Body.String(), err)
	}
	return rec.Code, answer
}

// A save answers with a revision that names the configuration as its client
// sent it - not the live one, which may carry what the process adds to the
// file - and only a save that went through answers with one.
func TestSaveAnswersWithTheRevisionOfWhatItsClientSent(t *testing.T) {
	revisions := NewRevisions()
	mux, live := saveBackend(t, revisions)
	doc := readDocument(t, mux)
	agent, _ := doc["agent"].(map[string]interface{})
	agent["max_turns"] = 42
	code, answer := putDocument(t, mux, doc)
	if code != http.StatusOK || answer["ok"] != true {
		t.Fatalf("PUT: %d %v", code, answer)
	}
	rev, _ := answer["revision"].(string)
	if rev == "" {
		t.Fatalf("a save went through and answered without a revision: %v", answer)
	}
	sent := revisions.Lookup(rev)
	if sent == nil {
		t.Fatalf("the answered revision %q names nothing", rev)
	}
	if sent == live() {
		t.Fatal("the answered revision names the live configuration, not what the client sent")
	}
	if sent.Agent.MaxTurns != 42 {
		t.Fatalf("the answered revision holds max_turns %d, want the 42 that was sent", sent.Agent.MaxTurns)
	}

	agent["max_turns"] = "many"
	code, answer = putDocument(t, mux, doc)
	if code != http.StatusBadRequest {
		t.Fatalf("PUT of an unreadable document: %d %v", code, answer)
	}
	if _, ok := answer["revision"]; ok {
		t.Fatalf("a refused save answered with a revision: %v", answer)
	}
}

// A backend that keeps no revisions answers a save with ok alone.
func TestSaveWithoutRevisionsAnswersOkAlone(t *testing.T) {
	mux, _ := saveBackend(t, nil)
	code, answer := putDocument(t, mux, readDocument(t, mux))
	if code != http.StatusOK || answer["ok"] != true {
		t.Fatalf("PUT: %d %v", code, answer)
	}
	if _, ok := answer["revision"]; ok {
		t.Fatalf("a backend without revisions answered with one: %v", answer)
	}
}
