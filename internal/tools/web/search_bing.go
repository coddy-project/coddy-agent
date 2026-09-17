package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// bingSearchFunc is swapped in tests to avoid live Bing calls.
var bingSearchFunc func(ctx context.Context, q Query, s Settings) ([]Result, error)

// runBing asks Bing. Bing answers a client it dislikes with a result page that
// is structurally perfect and about a different subject entirely, so what it
// returns is only merged after the relevance gate in classify has looked at it.
func runBing(ctx context.Context, q Query, s Settings) ([]Result, error) {
	if bingSearchFunc != nil {
		return bingSearchFunc(ctx, q, s)
	}
	count := q.MaxResults
	if count <= 0 {
		count = 15
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	// Bing's first parameter is a 1-based offset: page 1 -> first=1, page 2 -> first=count+1.
	first := (page-1)*count + 1
	searchURL := fmt.Sprintf(
		"https://www.bing.com/search?q=%s&count=%d&first=%d&setlang=en",
		url.QueryEscape(engineQuery(q)), count+2, first,
	)
	body, err := httpGet(ctx, searchURL, nil)
	if err != nil {
		return nil, err
	}
	rows, err := parseBingResults(body, count)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if reason, ok := challengeReason(body); ok {
			return nil, blocked("%s", reason)
		}
	}
	return rows, nil
}

// parseBingResults extracts organic results from Bing's HTML response.
// Bing marks each result with <li class="b_algo">, title in <h2><a>, snippet in <div class="b_caption"><p>.
func parseBingResults(body []byte, maxResults int) ([]Result, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var results []Result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if maxResults > 0 && len(results) >= maxResults {
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

func extractBingResult(li *html.Node) (Result, bool) {
	h2 := findElement(li, "h2")
	if h2 == nil {
		return Result{}, false
	}
	a := findElement(h2, "a")
	if a == nil {
		return Result{}, false
	}
	href := htmlAttr(a, "href")
	actualURL := decodeBingURL(href)
	if actualURL == "" {
		return Result{}, false
	}
	title := strings.TrimSpace(htmlText(a))
	if title == "" {
		return Result{}, false
	}
	snippet := ""
	if cap := findElementByClass(li, "div", "b_caption"); cap != nil {
		if p := findElement(cap, "p"); p != nil {
			snippet = strings.TrimSpace(htmlText(p))
		}
	}
	return Result{Title: title, URL: actualURL, Snippet: snippet}, true
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
