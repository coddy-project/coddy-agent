package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultRepo   = "coddy-project/coddy-agent"
	DefaultAPIURL = "https://api.github.com"
)

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func fetchRelease(ctx context.Context, client *http.Client, apiBase, repo, tag string) (*ghRelease, error) {
	apiBase = strings.TrimRight(apiBase, "/")
	path := fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, repo)
	if tag != "" {
		path = fmt.Sprintf("%s/repos/%s/releases/tags/%s", apiBase, repo, tag)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "coddy-agent-update")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github api %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, fmt.Errorf("release has empty tag_name")
	}
	return &rel, nil
}

func pickAsset(rel *ghRelease, assetName string) (*releaseAsset, error) {
	for i := range rel.Assets {
		if rel.Assets[i].Name == assetName {
			if rel.Assets[i].BrowserDownloadURL == "" {
				return nil, fmt.Errorf("asset %q has no download url", assetName)
			}
			return &rel.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("release %s has no asset %q", rel.TagName, assetName)
}

func downloadURL(ctx context.Context, client *http.Client, url string, reporter downloadReporter) ([]byte, error) {
	const (
		maxAttempts = 3
		maxBytes    = 256 << 20
	)

	var (
		data    []byte
		lastErr error
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if reporter != nil {
				reporter.Retry(attempt, maxAttempts, lastErr)
			}
			if err := waitForDownloadRetry(ctx, time.Duration(attempt-1)*200*time.Millisecond); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "coddy-agent-update")
		if len(data) > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", len(data)))
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("download %s", resp.Status)
		}
		if len(data) > 0 && resp.StatusCode == http.StatusOK {
			// The server ignored our Range request. Restart from the complete
			// response instead of appending a duplicate prefix.
			data = nil
		}
		if resp.ContentLength >= 0 && int64(len(data))+resp.ContentLength > maxBytes {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("download exceeds %d MiB limit", maxBytes>>20)
		}

		total := int64(-1)
		if resp.ContentLength >= 0 {
			total = int64(len(data)) + resp.ContentLength
		}
		readErr := appendResponse(&data, resp.Body, maxBytes, total, reporter)
		closeErr := resp.Body.Close()
		if readErr == nil && closeErr == nil {
			if reporter != nil {
				reporter.Complete(int64(len(data)))
			}
			return data, nil
		}
		if readErr != nil {
			lastErr = readErr
		} else {
			lastErr = closeErr
		}
	}
	return nil, fmt.Errorf("download failed after %d attempts: %w", 3, lastErr)
}

func appendResponse(data *[]byte, body io.Reader, maxBytes, total int64, reporter downloadReporter) error {
	buf := make([]byte, 64<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if int64(len(*data)+n) > maxBytes {
				return fmt.Errorf("download exceeds %d MiB limit", maxBytes>>20)
			}
			*data = append(*data, buf[:n]...)
			if reporter != nil {
				reporter.Progress(int64(len(*data)), total)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func waitForDownloadRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
