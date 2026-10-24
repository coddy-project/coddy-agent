package modelcatalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
)

const (
	// FirstFetchWait bounds how long a lookup waits for a catalog that has
	// never been downloaded; past it the lookup answers without one and the
	// download goes on.
	FirstFetchWait = 15 * time.Second
	// freshFor is how long a downloaded copy is trusted before the next
	// lookup revalidates it (the copy keeps serving meanwhile).
	freshFor = 24 * time.Hour
	// retryAfter is how long a failed download is not tried again.
	retryAfter = 5 * time.Minute
	// fetchTimeout bounds one download, the proxy included.
	fetchTimeout = 60 * time.Second
)

// ErrDisabled is returned while models_catalog.enable is false: nothing was
// downloaded or read.
var ErrDisabled = errors.New("the models catalog is turned off (models_catalog.enable)")

// Snapshot is the catalog a lookup answers from.
type Snapshot struct {
	Index     Index
	URL       string
	FetchedAt time.Time
}

// Catalog is the process's one copy of the catalog: read from the agent home
// on first use, downloaded when there is none, revalidated once a day with
// the ETag of the last download. Lookups never wait for a revalidation; only
// a catalog that has never been downloaded is waited for, a bounded moment.
type Catalog struct {
	path string
	log  *slog.Logger

	mu sync.Mutex
	// read reports that the copy on disk was tried.
	read  bool
	cache *cacheFile
	// inflight is closed when the running download ends; nil while idle.
	// inflightURL is the address it reads, cancelInflight drops it.
	inflight       chan struct{}
	inflightURL    string
	cancelInflight context.CancelFunc
	// failedURL and failedAt name the last download that failed.
	failedURL string
	failedAt  time.Time

	// client builds the client of one download from the proxy setting; now
	// is the clock freshness is measured with. Tests replace both.
	client func(proxy string) (*http.Client, error)
	now    func() time.Time
}

// New returns the catalog kept in home. With no home the catalog lives in
// memory only: nothing is read from or written to the process's folder.
func New(home string, log *slog.Logger) *Catalog {
	if log == nil {
		log = slog.Default()
	}
	path := ""
	if home != "" {
		path = filepath.Join(home, CacheFileName)
	}
	return &Catalog{
		path:   path,
		log:    logger.Component(log, logger.ComponentModelsCatalog),
		client: downloadClient,
		now:    time.Now,
	}
}

// downloadClient is the client of a download: through the proxy the setting
// names, else the environment's proxy (HTTPS_PROXY, HTTP_PROXY, NO_PROXY).
// The catalog is no provider, so no provider row's proxy applies to it.
func downloadClient(proxy string) (*http.Client, error) {
	c, err := llm.HTTPClientForOptionalProxy(proxy)
	if err != nil || c != nil {
		return c, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	return &http.Client{Transport: base.Clone()}, nil
}

// SetTransport points downloads at client and measures freshness with now;
// stands and tests use it in place of the network. nil keeps what was set.
func (c *Catalog) SetTransport(client func(proxy string) (*http.Client, error), now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client != nil {
		c.client = client
	}
	if now != nil {
		c.now = now
	}
}

// Lookup returns the catalog cfg names. A fresh copy answers at once; a stale
// one answers at once and is revalidated in the background; with none, the
// download is started (or joined) and waited for up to FirstFetchWait or
// until ctx ends. A download of another address (models_catalog.url changed
// while it ran) is dropped for the one of cfg. A download that failed is not
// tried again for five minutes. With models_catalog.enable false it returns ErrDisabled and makes
// no request.
func (c *Catalog) Lookup(ctx context.Context, cfg config.ModelsCatalogConfig) (Snapshot, error) {
	if !cfg.IsEnabled() {
		return Snapshot{}, ErrDisabled
	}
	url := cfg.EffectiveURL()
	c.mu.Lock()
	if !c.read && c.path != "" {
		c.read = true
		cached, err := readCache(c.path)
		switch {
		case err == nil:
			c.cache = cached
		case !errors.Is(err, fs.ErrNotExist):
			c.log.Warn("models catalog: the copy in the agent home cannot be read; it will be downloaded again", "error", err)
		}
	}
	cached := c.current(url)
	wait := c.refreshLocked(url, cfg.Proxy, cached)
	failed := c.failedURL == url
	c.mu.Unlock()

	if cached != nil {
		return snapshot(cached), nil
	}
	if wait == nil {
		if failed {
			return Snapshot{}, fmt.Errorf("models catalog: %s could not be downloaded recently; it is tried again in a few minutes", url)
		}
		return Snapshot{}, fmt.Errorf("models catalog: %s has not answered yet", url)
	}
	timer := time.NewTimer(FirstFetchWait)
	defer timer.Stop()
	select {
	case <-wait:
	case <-timer.C:
		return Snapshot{}, fmt.Errorf("models catalog: %s has not answered yet", url)
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
	c.mu.Lock()
	cached = c.current(url)
	c.mu.Unlock()
	if cached == nil {
		return Snapshot{}, fmt.Errorf("models catalog: %s could not be downloaded", url)
	}
	return snapshot(cached), nil
}

func snapshot(c *cacheFile) Snapshot {
	return Snapshot{Index: c.Providers, URL: c.URL, FetchedAt: c.FetchedAt}
}

// current is the copy of url, nil when the copy held is of another address.
func (c *Catalog) current(url string) *cacheFile {
	if c.cache == nil || c.cache.URL != url {
		return nil
	}
	return c.cache
}

// refreshLocked starts a download of url unless one of url is running, the
// copy is fresh, or the last download of url failed recently. It returns a
// channel to wait on while there is no copy of url to answer with, nil
// otherwise.
func (c *Catalog) refreshLocked(url, proxy string, cached *cacheFile) <-chan struct{} {
	if c.inflight != nil {
		if c.inflightURL == url {
			if cached == nil {
				return c.inflight
			}
			return nil
		}
		// models_catalog.url changed while the old address downloaded: no
		// lookup asks for that copy any more, and waiting it out would
		// leave this one unanswered. It is cancelled and url goes next.
		c.cancelInflight()
		c.inflight, c.inflightURL, c.cancelInflight = nil, "", nil
	}
	now := c.now()
	if cached != nil && now.Sub(cached.FetchedAt) < freshFor {
		return nil
	}
	if c.failedURL == url && now.Sub(c.failedAt) < retryAfter {
		return nil
	}
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	c.inflight, c.inflightURL, c.cancelInflight = done, url, cancel
	etag := ""
	if cached != nil {
		etag = cached.ETag
	}
	client := c.client
	go func() {
		defer close(done)
		defer cancel()
		next, err := c.download(ctx, client, url, proxy, etag, cached)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.inflight != done {
			// A download of the address configured now replaced this one.
			return
		}
		c.inflight, c.inflightURL, c.cancelInflight = nil, "", nil
		if err != nil {
			c.failedURL, c.failedAt = url, c.now()
			c.log.Warn("models catalog: download failed; suggestions use the last copy, if any", "url", url, "error", err)
			return
		}
		c.failedURL, c.failedAt = "", time.Time{}
		c.cache = next
		if c.path == "" {
			return
		}
		if werr := writeCache(c.path, next); werr != nil {
			c.log.Warn("models catalog: the copy could not be kept in the agent home", "error", werr)
		}
		c.log.Debug("models catalog: read", "url", url, "providers", len(next.Providers), "models", next.Providers.Models())
	}()
	if cached == nil {
		return done
	}
	return nil
}

// download fetches url, asking only whether it changed when the copy of it
// carries an ETag.
func (c *Catalog) download(ctx context.Context, client func(string) (*http.Client, error), url, proxy, etag string, cached *cacheFile) (*cacheFile, error) {
	hc, err := client(proxy)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	c.mu.Lock()
	now := c.now()
	c.mu.Unlock()
	switch {
	case resp.StatusCode == http.StatusNotModified && cached != nil:
		next := *cached
		next.FetchedAt = now
		return &next, nil
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDocumentBytes {
		return nil, fmt.Errorf("%s answered with more than %d bytes", url, maxDocumentBytes)
	}
	idx, err := Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(idx) == 0 {
		return nil, fmt.Errorf("%s holds no model with a usable window", url)
	}
	return &cacheFile{URL: url, ETag: resp.Header.Get("ETag"), FetchedAt: now, Providers: idx}, nil
}
