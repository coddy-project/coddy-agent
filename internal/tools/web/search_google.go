package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// googleSearchFunc is swapped in tests to avoid live Google calls.
var googleSearchFunc func(ctx context.Context, query string, page, maxResults int) ([]googleResult, error)

type googleResult struct {
	Title   string
	URL     string
	Snippet string
}

func defaultGoogleSearch(ctx context.Context, query string, page, maxResults int) ([]googleResult, error) {
	if page < 1 {
		page = 1
	}
	start := (page - 1) * maxResults
	searchURL := fmt.Sprintf(
		"https://www.google.com/search?q=%s&num=%d&start=%d&hl=en&safe=moderate",
		url.QueryEscape(query), maxResults+2, start,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("google: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseGoogleResults(body, maxResults)
}

// parseGoogleResults extracts organic results from Google's HTML response.
// Locates <a> elements containing an <h3> — Google's stable pattern for result titles.
func parseGoogleResults(body []byte, maxResults int) ([]googleResult, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var results []googleResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			href := htmlAttr(n, "href")
			if h3 := findElement(n, "h3"); h3 != nil {
				actualURL := decodeGoogleHref(href)
				if actualURL != "" && !isGoogleDomain(actualURL) {
					title := strings.TrimSpace(htmlText(h3))
					if title != "" {
						results = append(results, googleResult{Title: title, URL: actualURL})
						return
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results, nil
}

// decodeGoogleHref converts a raw href to an absolute URL.
// Google wraps organic results as /url?q=<encoded-url>&...; this decodes them.
func decodeGoogleHref(href string) string {
	if strings.HasPrefix(href, "/url?") {
		u, err := url.Parse(href)
		if err != nil {
			return ""
		}
		q := u.Query().Get("q")
		if strings.HasPrefix(q, "http://") || strings.HasPrefix(q, "https://") {
			return q
		}
		return ""
	}
	if strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://") {
		return href
	}
	return ""
}

// isGoogleDomain reports whether rawURL belongs to a Google-owned domain.
func isGoogleDomain(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	return host == "google.com" ||
		strings.HasPrefix(host, "google.") ||
		strings.HasSuffix(host, ".google.com")
}
