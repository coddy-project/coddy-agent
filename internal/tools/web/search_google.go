package web

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"context"

	"golang.org/x/net/html"
)

// googleSearchFunc is swapped in tests to avoid live Google calls.
var googleSearchFunc func(ctx context.Context, q Query, s Settings) ([]Result, error)

// runGoogle asks Google. It is not in the default engine set: measured from a
// server, the page Google serves an unapproved client carries no organic
// results at all - no <h3>, no /url?q= href - for any user agent and with the
// basic-HTML switch, because the results are rendered in the browser. The
// backend stays available for an operator whose egress Google still serves, and
// an answer with no result markup is reported as blocked rather than as an
// empty index.
func runGoogle(ctx context.Context, q Query, s Settings) ([]Result, error) {
	if googleSearchFunc != nil {
		return googleSearchFunc(ctx, q, s)
	}
	count := q.MaxResults
	if count <= 0 {
		count = 15
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	start := (page - 1) * count
	searchURL := fmt.Sprintf(
		"https://www.google.com/search?q=%s&num=%d&start=%d&hl=en&safe=moderate",
		url.QueryEscape(engineQuery(q)), count+2, start,
	)
	body, err := httpGet(ctx, searchURL, nil)
	if err != nil {
		return nil, err
	}
	rows, err := parseGoogleResults(body, count)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if reason, ok := challengeReason(body); ok {
			return nil, blocked("%s", reason)
		}
		return nil, blocked("no organic results in the static page (client-rendered)")
	}
	return rows, nil
}

// parseGoogleResults extracts organic results from Google's HTML response.
// Locates <a> elements containing an <h3> — Google's stable pattern for result titles.
func parseGoogleResults(body []byte, maxResults int) ([]Result, error) {
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
		if n.Type == html.ElementNode && n.Data == "a" {
			href := htmlAttr(n, "href")
			if h3 := findElement(n, "h3"); h3 != nil {
				actualURL := decodeGoogleHref(href)
				if actualURL != "" && !isGoogleDomain(actualURL) {
					title := strings.TrimSpace(htmlText(h3))
					if title != "" {
						// The snippet sits in a sibling container of the
						// anchor, not inside it: without this a Google row
						// reached the model as a bare title and URL.
						results = append(results, Result{
							Title:   title,
							URL:     actualURL,
							Snippet: googleSnippetNear(n),
						})
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

// googleSnippetNear recovers the description that belongs to a result anchor.
// Google keeps it outside the anchor, in a later sibling of one of the
// anchor's ancestors, so the search walks up a few levels and takes the first
// following block with enough text to be a description rather than a label.
func googleSnippetNear(anchor *html.Node) string {
	for node, depth := anchor, 0; node != nil && depth < 4; node, depth = node.Parent, depth+1 {
		for sib := node.NextSibling; sib != nil; sib = sib.NextSibling {
			if sib.Type != html.ElementNode {
				continue
			}
			text := strings.TrimSpace(htmlText(sib))
			if len([]rune(text)) >= 40 {
				return strings.Join(strings.Fields(text), " ")
			}
		}
	}
	return ""
}
