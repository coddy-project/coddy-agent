package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// bingSearchFunc is swapped in tests to avoid live Bing calls.
var bingSearchFunc func(ctx context.Context, query string, page, maxResults int) ([]bingResult, error)

type bingResult struct {
	Title   string
	URL     string
	Snippet string
}

func defaultBingSearch(ctx context.Context, query string, page, maxResults int) ([]bingResult, error) {
	if page < 1 {
		page = 1
	}
	// Bing's first parameter is 1-based offset: page 1 → first=1, page 2 → first=count+1, etc.
	first := (page-1)*maxResults + 1
	searchURL := fmt.Sprintf(
		"https://www.bing.com/search?q=%s&count=%d&first=%d&setlang=en",
		url.QueryEscape(query), maxResults+2, first,
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
		return nil, fmt.Errorf("bing: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseBingResults(body, maxResults)
}

// parseBingResults extracts organic results from Bing's HTML response.
// Bing marks each result with <li class="b_algo">, title in <h2><a>, snippet in <div class="b_caption"><p>.
func parseBingResults(body []byte, maxResults int) ([]bingResult, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var results []bingResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}
		if n.Type == html.ElementNode && n.Data == "li" {
			if strings.Contains(htmlAttr(n, "class"), "b_algo") {
				if r, ok := extractBingResult(n); ok {
					results = append(results, r)
					return
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

func extractBingResult(li *html.Node) (bingResult, bool) {
	h2 := findElement(li, "h2")
	if h2 == nil {
		return bingResult{}, false
	}
	a := findElement(h2, "a")
	if a == nil {
		return bingResult{}, false
	}
	href := htmlAttr(a, "href")
	actualURL := decodeBingURL(href)
	if actualURL == "" {
		return bingResult{}, false
	}
	title := strings.TrimSpace(htmlText(a))
	if title == "" {
		return bingResult{}, false
	}
	snippet := ""
	if cap := findElementByClass(li, "div", "b_caption"); cap != nil {
		if p := findElement(cap, "p"); p != nil {
			snippet = strings.TrimSpace(htmlText(p))
		}
	}
	return bingResult{Title: title, URL: actualURL, Snippet: snippet}, true
}

// decodeBingURL extracts the real destination from Bing's tracking redirect.
// Bing wraps organic result hrefs as: https://www.bing.com/ck/a?...&u=a1<base64RawStd>&ntb=1
func decodeBingURL(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if u.Host == "www.bing.com" && strings.HasPrefix(u.Path, "/ck/") {
		raw := u.Query().Get("u")
		if !strings.HasPrefix(raw, "a1") {
			return ""
		}
		decoded, err := base64.RawStdEncoding.DecodeString(raw[2:])
		if err != nil {
			return ""
		}
		result := string(decoded)
		if strings.HasPrefix(result, "http://") || strings.HasPrefix(result, "https://") {
			return result
		}
		return ""
	}
	if strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://") {
		return href
	}
	return ""
}

// findElementByClass finds the first descendant element with the given tag containing the given class substring.
func findElementByClass(root *html.Node, tag, class string) *html.Node {
	if root.Type == html.ElementNode && root.Data == tag {
		if strings.Contains(htmlAttr(root, "class"), class) {
			return root
		}
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if found := findElementByClass(c, tag, class); found != nil {
			return found
		}
	}
	return nil
}

// findElement returns the first descendant (or self) with the given tag name.
func findElement(root *html.Node, tag string) *html.Node {
	if root.Type == html.ElementNode && root.Data == tag {
		return root
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, tag); found != nil {
			return found
		}
	}
	return nil
}

// htmlText returns the concatenated text content of a node and all its descendants.
func htmlText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// htmlAttr returns the value of the named attribute, or "" if absent.
func htmlAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
