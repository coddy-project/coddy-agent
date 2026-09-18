package mention

import (
	"context"
	"sync"
)

// KindURL marks the attachment of a web page an "@https://..." mention read.
const KindURL = "url"

// URLFetcher reads the page an "@https://..." mention names and returns its
// text. The web tools register one (internal/tools/web), so the mention is
// held to the same address guard as the webfetch tool; a process without one
// leaves a URL mention as prose.
type URLFetcher func(ctx context.Context, rawURL string) (string, error)

var (
	urlFetcherMu sync.RWMutex
	urlFetcher   URLFetcher
)

// RegisterURLFetcher installs the fetcher URL mentions use; nil removes it.
func RegisterURLFetcher(f URLFetcher) {
	urlFetcherMu.Lock()
	urlFetcher = f
	urlFetcherMu.Unlock()
}

// FetchURL reads rawURL with the registered fetcher. ok is false when no
// fetcher is registered.
func FetchURL(ctx context.Context, rawURL string) (text string, ok bool, err error) {
	urlFetcherMu.RLock()
	f := urlFetcher
	urlFetcherMu.RUnlock()
	if f == nil {
		return "", false, nil
	}
	text, err = f(ctx, rawURL)
	return text, true, err
}
