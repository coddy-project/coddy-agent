package modelcatalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// catalogServer serves testdata/api.json with an ETag and answers a request
// that names it with 304.
type catalogServer struct {
	*httptest.Server
	requests atomic.Int32
	// conditional counts the requests that carried If-None-Match.
	conditional atomic.Int32
	// status, when set, is answered instead of the catalog.
	status atomic.Int32
	// gate, when set, holds every answer until it is closed.
	gate chan struct{}
	// body, when set, is served instead of the fixture.
	body func(w io.Writer)
}

const fixtureETag = `"fixture-1"`

func newCatalogServer(t *testing.T) *catalogServer {
	t.Helper()
	data, err := os.ReadFile("testdata/api.json")
	if err != nil {
		t.Fatal(err)
	}
	s := &catalogServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		if s.gate != nil {
			<-s.gate
		}
		if code := s.status.Load(); code != 0 {
			w.WriteHeader(int(code))
			return
		}
		if inm := r.Header.Get("If-None-Match"); inm != "" {
			s.conditional.Add(1)
			if inm == fixtureETag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Set("ETag", fixtureETag)
		w.Header().Set("Content-Type", "application/json")
		if s.body != nil {
			s.body(w)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

// clock is a settable time.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func testCatalog(t *testing.T, home string) (*Catalog, *clock, *[]string) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	proxies := []string{}
	c := New(home, nil)
	c.SetTransport(func(proxy string) (*http.Client, error) {
		mu.Lock()
		proxies = append(proxies, proxy)
		mu.Unlock()
		return &http.Client{}, nil
	}, clk.now)
	return c, clk, &proxies
}

func cfgFor(url string) config.ModelsCatalogConfig {
	return config.ModelsCatalogConfig{URL: url}
}

// waitIdle blocks until no download runs.
func waitIdle(t *testing.T, c *Catalog) {
	t.Helper()
	c.mu.Lock()
	ch := c.inflight
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("the download did not end")
	}
	// The goroutine installs the copy after closing; take the lock once more.
	c.mu.Lock()
	//nolint:staticcheck // SA2001: an empty critical section waits for the installer.
	c.mu.Unlock()
}

func TestLookupDownloadsOnceAndKeepsACopy(t *testing.T) {
	srv := newCatalogServer(t)
	home := t.TempDir()
	c, _, proxies := testCatalog(t, home)
	cfg := config.ModelsCatalogConfig{URL: srv.URL, Proxy: "none"}

	snap, err := c.Lookup(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Index.ProviderFor("anthropic", "") != "anthropic" || snap.URL != srv.URL {
		t.Fatalf("snapshot = %+v", snap)
	}
	waitIdle(t, c)
	if _, err := c.Lookup(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if n := srv.requests.Load(); n != 1 {
		t.Errorf("%d downloads, want one: a fresh copy answers", n)
	}
	if len(*proxies) != 1 || (*proxies)[0] != "none" {
		t.Errorf("the download was given proxy %q, want the configured none", *proxies)
	}
	info, err := os.Stat(filepath.Join(home, CacheFileName))
	if err != nil {
		t.Fatalf("no copy kept in the agent home: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 && runtimeIsUnix() {
		t.Errorf("the copy is readable by others: %v", info.Mode().Perm())
	}

	// Another process reads the copy and asks nothing.
	again, _, _ := testCatalog(t, home)
	if _, err := again.Lookup(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if n := srv.requests.Load(); n != 1 {
		t.Errorf("a restart downloaded again (%d requests)", n)
	}
}

// models_catalog.enable: false makes no request, and reads no copy either.
func TestLookupTurnedOffAsksNothing(t *testing.T) {
	srv := newCatalogServer(t)
	c, _, _ := testCatalog(t, t.TempDir())
	off := false
	_, err := c.Lookup(context.Background(), config.ModelsCatalogConfig{Enabled: &off, URL: srv.URL})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if n := srv.requests.Load(); n != 0 {
		t.Errorf("a disabled catalog sent %d request(s)", n)
	}
	if c.read {
		t.Error("a disabled catalog read its copy")
	}
}

// A day-old copy answers at once and is revalidated in the background with
// its ETag; an unchanged catalog costs a 304.
func TestLookupRevalidatesAStaleCopy(t *testing.T) {
	srv := newCatalogServer(t)
	c, clk, _ := testCatalog(t, t.TempDir())
	cfg := cfgFor(srv.URL)
	first, err := c.Lookup(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)

	clk.advance(25 * time.Hour)
	srv.gate = make(chan struct{})
	stale, err := c.Lookup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("a stale copy did not answer at once: %v", err)
	}
	if !stale.FetchedAt.Equal(first.FetchedAt) {
		t.Errorf("the stale lookup waited for the revalidation")
	}
	close(srv.gate)
	waitIdle(t, c)
	if srv.conditional.Load() != 1 {
		t.Fatalf("the revalidation carried no If-None-Match")
	}
	fresh, err := c.Lookup(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.FetchedAt.Equal(clk.now()) || fresh.Index.Models() != first.Index.Models() {
		t.Errorf("after a 304 the copy is %v with %d models, want refreshed at %v and unchanged", fresh.FetchedAt, fresh.Index.Models(), clk.now())
	}
	if n := srv.requests.Load(); n != 2 {
		t.Errorf("%d requests, want the download and one revalidation", n)
	}
}

// A copy of another address is not the catalog the configuration names.
func TestLookupIgnoresACopyOfAnotherURL(t *testing.T) {
	srv := newCatalogServer(t)
	home := t.TempDir()
	if err := writeCache(filepath.Join(home, CacheFileName), &cacheFile{
		URL:       "https://old-mirror.example/api.json",
		FetchedAt: time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC),
		Providers: Index{"stale": {Models: map[string]Entry{"m": {Context: 1}}}},
	}); err != nil {
		t.Fatal(err)
	}
	c, _, _ := testCatalog(t, home)
	snap, err := c.Lookup(context.Background(), cfgFor(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Index["stale"]; ok || srv.requests.Load() != 1 {
		t.Fatalf("the copy of another url answered (requests %d)", srv.requests.Load())
	}
}

// A download that failed is not tried again for five minutes.
func TestLookupBacksOffAfterAFailure(t *testing.T) {
	srv := newCatalogServer(t)
	srv.status.Store(http.StatusBadGateway)
	c, clk, _ := testCatalog(t, t.TempDir())
	cfg := cfgFor(srv.URL)
	if _, err := c.Lookup(context.Background(), cfg); err == nil {
		t.Fatal("a 502 gave a catalog")
	}
	if _, err := c.Lookup(context.Background(), cfg); err == nil || srv.requests.Load() != 1 {
		t.Fatalf("a lookup right after a failure asked again (requests %d, err %v)", srv.requests.Load(), err)
	}
	srv.status.Store(0)
	clk.advance(6 * time.Minute)
	if _, err := c.Lookup(context.Background(), cfg); err != nil {
		t.Fatalf("after the pause: %v", err)
	}
	if n := srv.requests.Load(); n != 2 {
		t.Errorf("%d requests, want a second try after the pause", n)
	}
}

// A mirror answering with more than the catalog could hold is refused, and
// the copy already held keeps serving.
func TestLookupRefusesAnOversizedAnswer(t *testing.T) {
	srv := newCatalogServer(t)
	c, clk, _ := testCatalog(t, t.TempDir())
	cfg := cfgFor(srv.URL)
	if _, err := c.Lookup(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)
	srv.body = func(w io.Writer) {
		_, _ = io.WriteString(w, `{"x":"`)
		_, _ = io.CopyN(w, zeros{}, maxDocumentBytes)
		_, _ = io.WriteString(w, `"}`)
	}
	clk.advance(25 * time.Hour)
	// The If-None-Match would get a 304: drop the ETag so the body is sent.
	c.mu.Lock()
	c.cache.ETag = ""
	c.mu.Unlock()
	if _, err := c.Lookup(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)
	snap, err := c.Lookup(context.Background(), cfg)
	if err != nil || snap.Index.ProviderFor("openai", "") != "openai" {
		t.Fatalf("the old copy did not survive an oversized answer: %+v, %v", snap, err)
	}
	if c.failedURL != srv.URL {
		t.Error("an oversized answer was not counted as a failure")
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// Lookups that arrive together share one download.
func TestLookupsShareOneDownload(t *testing.T) {
	srv := newCatalogServer(t)
	srv.gate = make(chan struct{})
	c, _, _ := testCatalog(t, t.TempDir())
	cfg := cfgFor(srv.URL)
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Lookup(context.Background(), cfg)
			errs <- err
		}()
	}
	// Let every lookup join before the answer goes out.
	for srv.requests.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(srv.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := srv.requests.Load(); n != 1 {
		t.Errorf("%d downloads for lookups that arrived together", n)
	}
}

// A lookup waits for a first download no longer than its caller does.
func TestLookupWaitsNoLongerThanItsCaller(t *testing.T) {
	srv := newCatalogServer(t)
	srv.gate = make(chan struct{})
	defer close(srv.gate)
	c, _, _ := testCatalog(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Lookup(ctx, cfgFor(srv.URL)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the lookup took %s", d)
	}
}

func TestReadCacheRefusesWhatIsNotACatalog(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"garbage.json": "not json",
		"empty.json":   `{"url":"https://models.dev/api.json","providers":{}}`,
		"no-url.json":  `{"providers":{"p":{"models":{"m":{"context":1}}}}}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCache(path); err == nil {
			t.Errorf("%s read as a catalog", name)
		}
	}
	if _, err := readCache(dir); err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Errorf("a directory read as a catalog: %v", err)
	}
}

// A lookup of a new address does not wait on the download of the old one
// (models_catalog.url changed while it ran): it is answered "not yet", and
// the next lookup downloads the new address.
func TestLookupDoesNotJoinADownloadOfAnotherURL(t *testing.T) {
	old := newCatalogServer(t)
	old.gate = make(chan struct{})
	next := newCatalogServer(t)
	c, _, _ := testCatalog(t, t.TempDir())

	go func() { _, _ = c.Lookup(context.Background(), cfgFor(old.URL)) }()
	for old.requests.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	if _, err := c.Lookup(context.Background(), cfgFor(next.URL)); err == nil || !strings.Contains(err.Error(), "not answered yet") {
		t.Fatalf("a lookup of the new address: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the new address waited on the old one's download")
	}
	close(old.gate)
	waitIdle(t, c)
	snap, err := c.Lookup(context.Background(), cfgFor(next.URL))
	if err != nil || snap.URL != next.URL || next.requests.Load() != 1 {
		t.Fatalf("the next lookup: %+v, %v, %d request(s) to the new address", snap, err, next.requests.Load())
	}
}
