package web

import (
	"bytes"
	"context"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// ddgSearchFunc is swapped in tests to avoid live DuckDuckGo calls.
var ddgSearchFunc func(ctx context.Context, q Query, s Settings) ([]Result, error)

// ddgEndpoint is the no-JavaScript result page. It is a variable so a test can
// point it at a local server.
var ddgEndpoint = "https://html.duckduckgo.com/html/"

// runDDG asks DuckDuckGo. It is not in the default engine set: measured from a
// server, both the html and the lite endpoint answer every query with HTTP 202
// and a challenge page. The backend stays for an operator whose egress
// DuckDuckGo still serves.
//
// The page is read here rather than through a search library on purpose. A
// library reports the interstitial and a genuinely empty index with the same
// "no results found" message, and collapsing those two is the bug this whole
// tool was rewritten to stop making: the status code is the only thing that
// separates "DuckDuckGo turned us away" from "DuckDuckGo has nothing", and it
// is visible only to whoever makes the request.
func runDDG(ctx context.Context, q Query, s Settings) ([]Result, error) {
	if ddgSearchFunc != nil {
		return ddgSearchFunc(ctx, q, s)
	}
	max := q.MaxResults
	if max <= 0 {
		max = 15
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	// DuckDuckGo pages by result offset, in steps of roughly thirty.
	endpoint := ddgEndpoint + "?q=" + url.QueryEscape(engineQuery(q))
	if page > 1 {
		endpoint += "&s=" + itoa((page-1)*30)
	}
	body, err := httpGet(ctx, endpoint, map[string]string{"Accept-Language": "en-US,en;q=0.9"})
	if err != nil {
		return nil, err
	}
	rows, err := parseDDGResults(body, max)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if reason, ok := challengeReason(body); ok {
			return nil, blocked("%s", reason)
		}
		if !bytes.Contains(body, []byte("result")) {
			return nil, blocked("no result markup in the page (layout changed?)")
		}
	}
	return rows, nil
}

// parseDDGResults extracts organic rows from the no-JavaScript result page.
// Each result is an anchor of class result__a, with the description in a
// sibling of class result__snippet.
func parseDDGResults(body []byte, maxResults int) ([]Result, error) {
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
		if n.Type == html.ElementNode && n.Data == "div" &&
			strings.Contains(htmlAttr(n, "class"), "result__body") {
			if r, ok := extractDDGResult(n); ok {
				results = append(results, r)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results, nil
}

func extractDDGResult(node *html.Node) (Result, bool) {
	a := findElementByClassPrefixContains(node, "a", "result__a")
	if a == nil {
		return Result{}, false
	}
	href := decodeDDGHref(htmlAttr(a, "href"))
	if href == "" {
		return Result{}, false
	}
	title := strings.TrimSpace(htmlText(a))
	if title == "" {
		return Result{}, false
	}
	snippet := ""
	if sn := findElementByClassPrefixContains(node, "a", "result__snippet"); sn != nil {
		snippet = strings.TrimSpace(htmlText(sn))
	}
	return Result{Title: title, URL: href, Snippet: snippet}, true
}

// decodeDDGHref unwraps the redirect DuckDuckGo puts in front of a result:
// //duckduckgo.com/l/?uddg=<encoded>&rut=...
func decodeDDGHref(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if strings.HasSuffix(u.Hostname(), "duckduckgo.com") && strings.HasPrefix(u.Path, "/l/") {
		target := u.Query().Get("uddg")
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			return target
		}
		return ""
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.String()
	}
	return ""
}

// findElementByClassPrefixContains finds the first descendant of the given tag
// whose class list contains the given class name.
func findElementByClassPrefixContains(root *html.Node, tag, class string) *html.Node {
	if root.Type == html.ElementNode && root.Data == tag &&
		strings.Contains(htmlAttr(root, "class"), class) {
		return root
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if found := findElementByClassPrefixContains(c, tag, class); found != nil {
			return found
		}
	}
	return nil
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
