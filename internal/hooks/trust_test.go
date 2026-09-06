package hooks_test

// Trust receipts and the catalog for project-scope hook files.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
)

func projectSource(t *testing.T, home, cwd string) (*hooks.Source, *hooks.TrustStore) {
	t.Helper()
	entry := hooktest.Entry{Event: hooks.EventPreToolUse, Matcher: "run_command", Handlers: []hooks.Handler{hooktest.Handler("allow")}}
	writeHooks(t, filepath.Join(cwd, ".coddy", "hooks.json"), entry)
	store := hooks.NewTrustStore(home)
	sources := hooks.NewLoader([]string{"${CWD}/.coddy/hooks.json"}, "ask").WithStore(store).Load(cwd, home)
	if len(sources) != 1 || sources[0].Scope != hooks.ScopeProject {
		t.Fatalf("sources = %+v", sources)
	}
	return sources[0], store
}

func TestTrustStoreRoundTripAndDigestBinding(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	src, store := projectSource(t, home, cwd)
	workspace := hooks.CanonicalWorkspace(cwd)

	if src.Trust != hooks.TrustNeedsApproval || src.Runnable() {
		t.Fatalf("a fresh project file needs approval, got %+v", src)
	}
	if err := store.Approve(workspace, src); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !store.Approved(workspace, src.Display, src.Digest) {
		t.Fatal("receipt must be found for the same workspace, file and digest")
	}
	if store.Approved(filepath.Join(root, "elsewhere"), src.Display, src.Digest) {
		t.Fatal("a receipt is bound to its workspace")
	}
	if store.Approved(workspace, src.Display, "sha256:other") {
		t.Fatal("a receipt is bound to the file digest")
	}
	records := store.Records(workspace)
	if len(records) != 1 || records[0].File != ".coddy/hooks.json" || records[0].Digest != src.Digest || records[0].ApprovedAt == "" {
		t.Fatalf("records = %+v", records)
	}
	if _, err := os.Stat(filepath.Join(home, hooks.TrustFileName)); err != nil {
		t.Fatalf("receipts file must live in the coddy home: %v", err)
	}

	// The loader consults the store: the same file now runs.
	reloaded := hooks.NewLoader([]string{"${CWD}/.coddy/hooks.json"}, "ask").WithStore(store).Load(cwd, home)
	if len(reloaded) != 1 || !reloaded[0].Runnable() || reloaded[0].Trust != hooks.TrustTrusted {
		t.Fatalf("approved file must run, got %+v", reloaded)
	}

	// Rewriting the file changes the digest and withdraws the approval.
	writeHooks(t, filepath.Join(cwd, ".coddy", "hooks.json"), hooktest.Entry{Event: hooks.EventPreToolUse, Matcher: "*", Handlers: []hooks.Handler{hooktest.Handler("allow")}})
	rewritten := hooks.NewLoader([]string{"${CWD}/.coddy/hooks.json"}, "ask").WithStore(store).Load(cwd, home)
	if len(rewritten) != 1 || rewritten[0].Runnable() {
		t.Fatalf("a rewritten file needs approval again, got %+v", rewritten)
	}

	removed, err := store.Revoke(workspace, src.Display)
	if err != nil || !removed {
		t.Fatalf("revoke = %v, %v", removed, err)
	}
	if removed, _ := store.Revoke(workspace, src.Display); removed {
		t.Fatal("revoking twice must report nothing removed")
	}
	if len(store.Records(workspace)) != 0 {
		t.Fatal("records must be empty after revoke")
	}
}

func TestTrustStoreRefusesUserScopeAndInvalidFiles(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	entry := hooktest.Entry{Event: hooks.EventPreToolUse, Handlers: []hooks.Handler{hooktest.Handler("allow")}}
	writeHooks(t, filepath.Join(home, "hooks.json"), entry)
	store := hooks.NewTrustStore(home)
	sources := hooks.NewLoader([]string{"${CODDY_HOME}/hooks.json"}, "ask").WithStore(store).Load(cwd, home)
	if err := store.Approve(hooks.CanonicalWorkspace(cwd), sources[0]); err == nil {
		t.Fatal("a user-scope file needs no receipt and approving it must be refused")
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".coddy", "hooks.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	sources = hooks.NewLoader([]string{"${CWD}/.coddy/hooks.json"}, "ask").WithStore(store).Load(cwd, home)
	if err := store.Approve(hooks.CanonicalWorkspace(cwd), sources[0]); err == nil {
		t.Fatal("an invalid file cannot be approved")
	}
}

func TestCatalogRowsDescribeEverySource(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	writeHooks(t, filepath.Join(home, "hooks.json"),
		hooktest.Entry{Event: hooks.EventPreToolUse, Matcher: "run_command", Handlers: []hooks.Handler{hooktest.Handler("allow")}},
		hooktest.Entry{Event: hooks.EventPostToolUse, Handlers: []hooks.Handler{{Type: hooks.HandlerCommand, Command: "gofmt -l .", TimeoutSeconds: 5, FailClosed: true}}},
	)
	if err := os.MkdirAll(filepath.Join(cwd, ".coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".coddy", "hooks.json"), []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"prompt","prompt":"safe?"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".claude", "settings.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []string{"${CODDY_HOME}/hooks.json", "${CWD}/.claude/settings.json", "${CWD}/.coddy/hooks.json"}
	sources := hooks.NewLoader(files, "ask").WithStore(hooks.NewTrustStore(home)).Load(cwd, home)
	entries := hooks.BuildCatalog(sources)
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	user := entries[0]
	if user.Scope != hooks.ScopeUser || !user.Trusted || user.NeedsApproval || len(user.Hooks) != 2 {
		t.Fatalf("user entry = %+v", user)
	}
	if user.Hooks[0].Event != hooks.EventPreToolUse || user.Hooks[0].Matcher != "run_command" || user.Hooks[0].Command == "" {
		t.Fatalf("first hook row = %+v", user.Hooks[0])
	}
	if h := user.Hooks[1]; h.Event != hooks.EventPostToolUse || h.TimeoutSeconds != 5 || h.Async || !h.FailClosed {
		t.Fatalf("second hook row = %+v", h)
	}
	claude := entries[1]
	if claude.File != ".claude/settings.json" || claude.Error == "" || claude.Trusted {
		t.Fatalf("invalid file entry = %+v", claude)
	}
	project := entries[2]
	if project.File != ".coddy/hooks.json" || !project.NeedsApproval || project.Trust != hooks.TrustNeedsApproval || project.Digest == "" {
		t.Fatalf("project entry = %+v", project)
	}
	if len(project.Hooks) != 1 || project.Hooks[0].Unsupported == "" {
		t.Fatalf("unsupported handler must be listed with its reason, got %+v", project.Hooks)
	}
	if found := hooks.FindSource(sources, ".coddy/hooks.json"); found == nil || found.Scope != hooks.ScopeProject {
		t.Fatalf("FindSource by display = %+v", found)
	}
	if found := hooks.FindSource(sources, filepath.Join(home, "hooks.json")); found == nil || found.Scope != hooks.ScopeUser {
		t.Fatalf("FindSource by absolute path = %+v", found)
	}

	var listing strings.Builder
	hooks.WriteListing(&listing, entries)
	text := listing.String()
	for _, want := range []string{".coddy/hooks.json", "needs_approval", ".claude/settings.json", "invalid", "PreToolUse", "run_command"} {
		if !strings.Contains(text, want) {
			t.Fatalf("listing lacks %q:\n%s", want, text)
		}
	}
}

func TestTrustStoreSerialisesConcurrentInstances(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	entry := hooktest.Entry{Event: hooks.EventPreToolUse, Handlers: []hooks.Handler{hooktest.Handler("allow")}}
	files := []string{}
	for i := 0; i < 8; i++ {
		rel := filepath.Join(".coddy", "hooks-"+strconv.Itoa(i)+".json")
		writeHooks(t, filepath.Join(cwd, rel), entry)
		files = append(files, "${CWD}/"+filepath.ToSlash(rel))
	}
	sources := hooks.NewLoader(files, "ask").Load(cwd, home)
	if len(sources) != 8 {
		t.Fatalf("sources = %d", len(sources))
	}
	workspace := hooks.CanonicalWorkspace(cwd)
	var wg sync.WaitGroup
	errs := make(chan error, len(sources))
	for _, src := range sources {
		wg.Add(1)
		go func(src *hooks.Source) {
			defer wg.Done()
			// Each goroutine owns a fresh instance, the way HTTP requests do.
			if err := hooks.NewTrustStore(home).Approve(workspace, src); err != nil {
				errs <- err
			}
		}(src)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("approve: %v", err)
	}
	if got := len(hooks.NewTrustStore(home).Records(workspace)); got != 8 {
		t.Fatalf("every concurrent approval must survive, got %d records", got)
	}
	if _, err := os.Stat(filepath.Join(home, hooks.TrustFileName+".tmp")); !os.IsNotExist(err) {
		t.Fatal("the temporary file must not be left behind")
	}
}
