package preview

// Edge cases of the published-preview channel. The happy path is
// features/preview_publish.feature; what is here is the behaviour that
// matters when something goes wrong or somebody asks for something they
// should not get.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// publishEnv is a session pointed at root, the way the agent points one.
func publishEnv(cwd, root, sessionID string, maxBytes int64) *tooling.Env {
	return &tooling.Env{
		CWD: cwd, SessionID: sessionID,
		PreviewPublish: &tooling.PreviewPublishSettings{
			Enabled: true, Root: root, PublicURL: "https://coddy.example/preview/",
			TTLSeconds: 3600, MaxBytes: maxBytes,
		},
	}
}

// publishCall runs the tool and returns its result.
func publishCall(t *testing.T, env *tooling.Env, args map[string]interface{}) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return PreviewPublishTool().Execute(context.Background(), string(raw), env)
}

// servePublished runs the origin's handler over root, as the subsystem does.
func servePublished(t *testing.T, root string, opts publishHandlerOptions) *httptest.Server {
	t.Helper()
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(newPublishHandler(store{root: root}, opened, opts))
	t.Cleanup(func() {
		ts.Close()
		_ = opened.Close()
	})
	return ts
}

// publishedID is the artifact id out of a tool result's address.
func publishedID(t *testing.T, result string) string {
	t.Helper()
	address := urlInResult.FindString(strings.ReplaceAll(result, "https://", "http://"))
	if address == "" {
		t.Fatalf("no address in %q", result)
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(address, "http://coddy.example/preview/"), "/")
	if trimmed == "" || strings.Contains(trimmed, "/") {
		t.Fatalf("address %q does not name one artifact", address)
	}
	return trimmed
}

func TestPublishRefusesWhenTheChannelIsOff(t *testing.T) {
	dir := t.TempDir()
	for _, env := range []*tooling.Env{
		{CWD: dir, SessionID: "s"},
		{CWD: dir, SessionID: "s", PreviewPublish: &tooling.PreviewPublishSettings{Enabled: false, Root: t.TempDir()}},
	} {
		if _, err := publishCall(t, env, map[string]interface{}{}); err == nil {
			t.Fatal("publishing should be refused where the operator did not turn the channel on")
		} else if !strings.Contains(err.Error(), "tools.preview_publish.enable") {
			t.Fatalf("the refusal should name the key that turns it on, got %v", err)
		}
	}
}

func TestPublishOnlyTakesTheSessionsOwnDirectory(t *testing.T) {
	dir, root, outside := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(outside, "index.html"), "elsewhere")
	env := publishEnv(dir, root, "s", 1<<20)
	if _, err := publishCall(t, env, map[string]interface{}{"path": outside}); err == nil {
		t.Fatal("a directory outside the working directory should not be publishable")
	}
}

func TestPublishRefusesTheArtifactRootItself(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "previews")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "index.html"), "self")
	env := publishEnv(dir, root, "s", 1<<20)
	if _, err := publishCall(t, env, map[string]interface{}{"path": "previews"}); err == nil {
		t.Fatal("the artifact root should not be publishable into itself")
	}
}

func TestPublishLeavesNothingBehindWhenTheSourceIsTooLarge(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), strings.Repeat("x", 4096))
	env := publishEnv(dir, root, "s", 1024)
	if _, err := publishCall(t, env, map[string]interface{}{}); err == nil {
		t.Fatal("a source over max_bytes should be refused")
	} else if !strings.Contains(err.Error(), "max_bytes") {
		t.Fatalf("the refusal should name the budget, got %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("a refused publish left %s behind", e.Name())
		}
	}
	// Even the staging it used is gone, so a half-copied tree is never on disk.
	staging, err := os.ReadDir(filepath.Join(root, stagingDir))
	if err == nil && len(staging) > 0 {
		t.Fatalf("staging still holds %d entries", len(staging))
	}
}

func TestPublishReplacesOnlyWhatTheSameSessionNamed(t *testing.T) {
	dirA, dirB, root := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dirA, "index.html"), "first session")
	writeFile(t, filepath.Join(dirB, "index.html"), "second session")

	first, err := publishCall(t, publishEnv(dirA, root, "sess-a", 1<<20), map[string]interface{}{"name": "site"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := publishCall(t, publishEnv(dirB, root, "sess-b", 1<<20), map[string]interface{}{"name": "site"})
	if err != nil {
		t.Fatal(err)
	}
	idA, idB := publishedID(t, first), publishedID(t, second)
	if idA == idB {
		t.Fatalf("two sessions publishing %q both got %s", "site", idA)
	}
	body, err := os.ReadFile(filepath.Join(root, idA, "index.html"))
	if err != nil || !strings.Contains(string(body), "first session") {
		t.Fatalf("the first session's artifact reads %q (%v)", body, err)
	}
}

func TestUnpublishRefusesAnotherSessionsArtifact(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "mine")
	result, err := publishCall(t, publishEnv(dir, root, "sess-a", 1<<20), map[string]interface{}{"name": "site"})
	if err != nil {
		t.Fatal(err)
	}
	id := publishedID(t, result)

	intruder := publishEnv(t.TempDir(), root, "sess-b", 1<<20)
	if _, err := PreviewUnpublishTool().Execute(context.Background(),
		fmt.Sprintf(`{"id":%q}`, id), intruder); err == nil {
		t.Fatal("a session should not be able to take down another session's artifact")
	}
	if _, err := os.Stat(filepath.Join(root, id, "index.html")); err != nil {
		t.Fatalf("the artifact was removed anyway: %v", err)
	}
}

func TestUnpublishNamesWhatItCannotFind(t *testing.T) {
	env := publishEnv(t.TempDir(), t.TempDir(), "s", 1<<20)
	if _, err := PreviewUnpublishTool().Execute(context.Background(), `{"name":"gone"}`, env); err == nil {
		t.Fatal("unpublishing what was never published should fail")
	}
	if _, err := PreviewUnpublishTool().Execute(context.Background(), `{}`, env); err == nil {
		t.Fatal("unpublishing without a name or an id should fail")
	}
}

func TestPublishCopiesNoDotFileAndNoLinkOutOfTheSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege this test does not ask for on Windows")
	}
	dir, root, secrets := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "page")
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=1")
	writeFile(t, filepath.Join(dir, ".hidden", "note.txt"), "hidden")
	writeFile(t, filepath.Join(secrets, "id_rsa"), "PRIVATE KEY")
	if err := os.Symlink(filepath.Join(secrets, "id_rsa"), filepath.Join(dir, "key")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "index.html"), filepath.Join(dir, "copy.html")); err != nil {
		t.Fatal(err)
	}

	result, err := publishCall(t, publishEnv(dir, root, "s", 1<<20), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, publishedID(t, result))
	for _, gone := range []string{".env", ".hidden", "key"} {
		if _, err := os.Lstat(filepath.Join(artifact, gone)); err == nil {
			t.Fatalf("%s was published", gone)
		}
	}
	// A link that stays inside the source is published as the bytes it names.
	body, err := os.ReadFile(filepath.Join(artifact, "copy.html"))
	if err != nil || !strings.Contains(string(body), "page") {
		t.Fatalf("a link inside the source should be copied as a file, got %q (%v)", body, err)
	}
}

func TestPublishedOriginRefusesWhatIsNotAReadOfAnArtifact(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "page")
	result, err := publishCall(t, publishEnv(dir, root, "s", 1<<20), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	id := publishedID(t, result)
	ts := servePublished(t, root, publishHandlerOptions{})

	cases := []struct {
		name, method, path string
		want               int
	}{
		{"the root is never listed", http.MethodGet, "/", http.StatusNotFound},
		{"an unknown artifact", http.MethodGet, "/nothing/", http.StatusNotFound},
		{"the manifest of an artifact", http.MethodGet, "/" + id + "/" + manifestName, http.StatusNotFound},
		{"the staging directory", http.MethodGet, "/" + stagingDir + "/", http.StatusNotFound},
		// A path with a dot segment is refused rather than resolved, even when
		// it would land back inside the artifact it came from.
		{"a traversal", http.MethodGet, "/" + id + "/../" + id + "/index.html", http.StatusNotFound},
		{"a traversal out of the root", http.MethodGet, "/" + id + "/../../etc/passwd", http.StatusNotFound},
		{"a write", http.MethodPost, "/" + id + "/index.html", http.StatusMethodNotAllowed},
		{"a delete", http.MethodDelete, "/" + id + "/index.html", http.StatusMethodNotAllowed},
		{"the artifact itself", http.MethodGet, "/" + id + "/index.html", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := fetch(t, tc.method, ts.URL+tc.path, "")
			if resp.StatusCode != tc.want {
				t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}

func TestPublishedOriginAnswersOnlyTheHostsItWasGiven(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "page")
	result, err := publishCall(t, publishEnv(dir, root, "s", 1<<20), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	id := publishedID(t, result)
	ts := servePublished(t, root, publishHandlerOptions{
		AllowedHosts: []string{"127.0.0.1", "localhost", "coddy.example"},
	})
	for host, want := range map[string]int{
		"coddy.example": http.StatusOK,
		"localhost":     http.StatusOK,
		"evil.test":     http.StatusForbidden,
	} {
		resp, _ := fetch(t, http.MethodGet, ts.URL+"/"+id+"/index.html", host)
		if resp.StatusCode != want {
			t.Fatalf("Host %q = %d, want %d", host, resp.StatusCode, want)
		}
	}
}

func TestAllowedPublishHostsCarryTheAddressTheEdgeForwards(t *testing.T) {
	loopback := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8081}
	hosts := allowedPublishHosts(loopback, "127.0.0.1:8081", "https://coddy.example/preview/")
	joined := strings.Join(hosts, " ")
	for _, want := range []string{"localhost", "127.0.0.1", "coddy.example"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the allowlist %v does not carry %q", hosts, want)
		}
	}
	// A bind the operator opened to the network has no allowlist: clients
	// arrive under whatever name reaches it.
	open := &net.TCPAddr{IP: net.IPv4zero, Port: 8081}
	if got := allowedPublishHosts(open, "0.0.0.0:8081", ""); got != nil {
		t.Fatalf("a non-loopback bind should answer any Host, got %v", got)
	}
}

func TestExpiredArtifactsStopAnsweringAndAreSweptAway(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "page")
	st := store{root: root}
	a, err := st.publish(publishRequest{
		Source: dir, SessionID: "s", Name: "short", TTL: time.Second, MaxBytes: 1 << 20,
		Now: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := servePublished(t, root, publishHandlerOptions{})
	resp, _ := fetch(t, http.MethodGet, ts.URL+"/"+a.ID+"/index.html", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an expired artifact answered %d before the sweep ran", resp.StatusCode)
	}
	if n := st.sweep(time.Now()); n != 1 {
		t.Fatalf("the sweep removed %d artifacts, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(root, a.ID)); err == nil {
		t.Fatal("the expired artifact is still on disk")
	}
}

func TestAnArtifactWithoutALifetimeIsNeverSweptAway(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "page")
	st := store{root: root}
	a, err := st.publish(publishRequest{Source: dir, SessionID: "s", Name: "pinned", MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if n := st.sweep(time.Now().Add(10 * 365 * 24 * time.Hour)); n != 0 {
		t.Fatalf("a pinned artifact was swept away (%d removed)", n)
	}
	if _, err := os.Stat(filepath.Join(root, a.ID, "index.html")); err != nil {
		t.Fatalf("the pinned artifact is gone: %v", err)
	}
}

func TestArtifactIDIsStablePerSessionAndNameOnly(t *testing.T) {
	first, err := artifactID("sess-a", "Release Notes!")
	if err != nil {
		t.Fatal(err)
	}
	again, err := artifactID("sess-a", "Release Notes!")
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("the same session and name gave %q then %q", first, again)
	}
	if !strings.HasPrefix(first, "release-notes-") {
		t.Fatalf("id %q does not read as the name it was published under", first)
	}
	other, err := artifactID("sess-b", "Release Notes!")
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatalf("two sessions share the id %q", first)
	}
	unnamed, err := artifactID("sess-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if unnamed == first || len(unnamed) != 32 {
		t.Fatalf("an unnamed artifact should get a random id, got %q", unnamed)
	}
	if _, err := artifactID("sess-a", "!!!"); err == nil {
		t.Fatal("a name with nothing to make an address out of should be refused")
	}
}

func TestPublishedAddressEndsInASlashAndEscapesTheArtifact(t *testing.T) {
	cases := []struct{ base, id, open, want string }{
		{"https://coddy.example/preview/", "a1", "", "https://coddy.example/preview/a1/"},
		{"https://coddy.example/preview", "a1", "", "https://coddy.example/preview/a1/"},
		{"https://coddy.example/preview/", "a1", "report.html", "https://coddy.example/preview/a1/report.html"},
	}
	for _, tc := range cases {
		if got := publishedAddress(tc.base, tc.id, tc.open); got != tc.want {
			t.Fatalf("publishedAddress(%q, %q, %q) = %q, want %q", tc.base, tc.id, tc.open, got, tc.want)
		}
	}
}

func TestFirstPathElementReadsTheArtifactOfARequest(t *testing.T) {
	cases := map[string]string{
		"/":                  "",
		"":                   "",
		"/a1":                "a1",
		"/a1/":               "a1",
		"/a1/assets/app.js":  "a1",
		"/a1/../a2/index.js": "a2",
	}
	for in, want := range cases {
		if got := firstPathElement(in); got != want {
			t.Fatalf("firstPathElement(%q) = %q, want %q", in, got, want)
		}
	}
}

// A symlink is the one way a request for an ordinary-looking name reaches a
// file the dot rule exists to keep out, so the rule follows the link.
func TestPublishDoesNotCopyALinkToADotFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege this test does not ask for on Windows")
	}
	dir, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "page")
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=1")
	writeFile(t, filepath.Join(dir, ".git", "config"), "[core]")
	for name, target := range map[string]string{
		"config-link": filepath.Join(dir, ".env"),
		"notes":       filepath.Join(dir, ".git", "config"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	result, err := publishCall(t, publishEnv(dir, root, "s", 1<<20), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, publishedID(t, result))
	for _, name := range []string{"config-link", "notes"} {
		if body, err := os.ReadFile(filepath.Join(artifact, name)); err == nil {
			t.Fatalf("%s published a dot-file through a link: %q", name, body)
		}
	}
	// What was left out is said, not swallowed.
	if !strings.Contains(result, "left out") {
		t.Fatalf("the result does not say that entries were left out: %q", result)
	}
}

// preview_unpublish asks for no permission, so the id it takes may name
// nothing but an artifact of the root.
func TestUnpublishRefusesAnIdThatIsNotOne(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "..", "victim-"+filepath.Base(root))
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	writeFile(t, filepath.Join(outside, "important.txt"), "data")
	writeFile(t, filepath.Join(outside, manifestName), `{"id":"x","session_id":"s"}`)
	writeFile(t, filepath.Join(root, stagingDir, "pub-1", manifestName), `{"id":"y","session_id":"s"}`)

	env := publishEnv(t.TempDir(), root, "s", 1<<20)
	for _, id := range []string{
		"../" + filepath.Base(outside), stagingDir + "/pub-1", ".staging", "", "..", "A1", "x/y",
	} {
		if _, err := PreviewUnpublishTool().Execute(context.Background(),
			fmt.Sprintf(`{"id":%q}`, id), env); err == nil {
			t.Fatalf("id %q was accepted", id)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "important.txt")); err != nil {
		t.Fatalf("a directory outside the artifact root was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, stagingDir, "pub-1")); err != nil {
		t.Fatalf("the staging directory of a publish in flight was removed: %v", err)
	}
}

// An artifact root that happens to lie inside the published directory would
// otherwise put every session's published bytes into one artifact.
func TestPublishLeavesTheArtifactRootOutOfTheCopy(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "previews")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "index.html"), "page")
	writeFile(t, filepath.Join(root, "b-site-123", "secret.txt"), "another session")

	result, err := publishCall(t, publishEnv(dir, root, "sess-a", 1<<20), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, publishedID(t, result))
	if _, err := os.Stat(filepath.Join(artifact, "previews")); err == nil {
		t.Fatal("the artifact root was published inside an artifact")
	}
	if _, err := os.Stat(filepath.Join(artifact, "index.html")); err != nil {
		t.Fatalf("the rest of the source was not published: %v", err)
	}
}

func TestPublishedLifetimeNeverOverflowsIntoForever(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "page")
	result, err := publishCall(t, publishEnv(dir, root, "s", 1<<20),
		map[string]interface{}{"ttl_seconds": 1 << 62})
	if err != nil {
		t.Fatal(err)
	}
	a, err := (store{root: root}).readManifest(publishedID(t, result))
	if err != nil {
		t.Fatal(err)
	}
	if a.ExpiresAt.IsZero() {
		t.Fatal("a lifetime too large to hold became no lifetime at all")
	}
}
