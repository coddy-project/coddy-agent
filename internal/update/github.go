package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

func downloadURL(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "coddy-agent-update")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s", resp.Status)
	}
	const maxBytes = 256 << 20
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}
