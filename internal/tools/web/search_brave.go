package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// braveSearchFunc is swapped in tests to avoid live Brave calls.
var braveSearchFunc func(ctx context.Context, q Query, s Settings) ([]Result, error)

// Endpoints the Brave backend talks to. They are variables so a test can point
// them at a local server and exercise the real request, parsing and
// classification rather than a stand-in for them.
var (
	braveHTMLEndpoint = "https://search.brave.com/search"
	braveAPIEndpoint  = "https://api.search.brave.com/res/v1/web/search"
)

// runBrave asks Brave Search. With an API key it uses the official JSON API,
// whose contract is stable and carries no parser; without one it reads the
// public result page, which was the only keyless backend to answer every probe
// - English and Cyrillic queries alike, paginated, through a proxy and direct.
func runBrave(ctx context.Context, q Query, s Settings) ([]Result, error) {
	if braveSearchFunc != nil {
		return braveSearchFunc(ctx, q, s)
	}
	if strings.TrimSpace(s.BraveAPIKey) != "" {
		return braveAPISearch(ctx, q, s)
	}
	return braveHTMLSearch(ctx, q, s)
}

func braveHTMLSearch(ctx context.Context, q Query, _ Settings) ([]Result, error) {
	// Brave paginates by result page, zero-based, not by result offset.
	offset := q.Page - 1
	if offset < 0 {
		offset = 0
	}
	searchURL := fmt.Sprintf("%s?q=%s&offset=%d&spellcheck=0",
		braveHTMLEndpoint, url.QueryEscape(engineQuery(q)), offset)
	body, err := httpGet(ctx, searchURL, nil)
	if err != nil {
		return nil, err
	}
	rows, err := parseBraveResults(body, q.MaxResults)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if reason, ok := challengeReason(body); ok {
			return nil, blocked("%s", reason)
		}
		// Brave answers a query with no matches with a page that still carries
		// its result scaffolding. A 200 with neither results nor scaffolding is
		// a layout this parser no longer understands, and saying so is the
		// difference between a visible break and silent nonsense.
		if !bytes.Contains(body, []byte("data-type=")) {
			return nil, blocked("no result markup in the page (layout changed?)")
		}
	}
	return rows, nil
}

// parseBraveResults extracts organic rows from Brave's result page. Brave marks
// each one with data-type="web" and carries the destination as a real href
// rather than a tracking redirect. The class names next to those attributes
// carry a per-build Svelte hash (svelte-jmfu5f), so nothing here matches a
// whole class: the anchors are the attribute and the stable class prefix.
func parseBraveResults(body []byte, maxResults int) ([]Result, error) {
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
		if n.Type == html.ElementNode && n.Data == "div" && htmlAttr(n, "data-type") == "web" {
			if r, ok := extractBraveResult(n); ok {
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

func extractBraveResult(node *html.Node) (Result, bool) {
	a := findAnchorWithHref(node)
	if a == nil {
		return Result{}, false
	}
	href := strings.TrimSpace(htmlAttr(a, "href"))
	if !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
		return Result{}, false
	}
	title := ""
	if t := findElementByClassPrefix(node, "div", "title search-snippet-title"); t != nil {
		// The full title is duplicated into the title attribute, which survives
		// the line clamp the visible text is truncated by.
		if attr := strings.TrimSpace(htmlAttr(t, "title")); attr != "" {
			title = attr
		} else {
			title = strings.TrimSpace(htmlText(t))
		}
	}
	if title == "" {
		return Result{}, false
	}
	snippet := ""
	if d := findElementByClassPrefix(node, "div", "content"); d != nil {
		snippet = strings.TrimSpace(htmlText(d))
	}
	return Result{Title: title, URL: href, Snippet: snippet}, true
}

// braveAPIResponse is the shape of the official Search API answer.
type braveAPIResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

func braveAPISearch(ctx context.Context, q Query, s Settings) ([]Result, error) {
	count := q.MaxResults
	if count <= 0 || count > 20 {
		count = 20
	}
	offset := q.Page - 1
	if offset < 0 {
		offset = 0
	}
	endpoint := fmt.Sprintf("%s?q=%s&count=%d&offset=%d",
		braveAPIEndpoint, url.QueryEscape(engineQuery(q)), count, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", strings.TrimSpace(s.BraveAPIKey))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, blocked("brave api rejected the key (http %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, blocked("brave api http %d", resp.StatusCode)
	}
	var parsed braveAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, Result{
			Title:   strings.TrimSpace(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: stripHTMLTags(r.Description),
		})
	}
	return out, nil
}

// engineQuery renders the query as an engine receives it, folding the optional
// site restriction into the syntax every backend here understands.
func engineQuery(q Query) string {
	text := strings.TrimSpace(q.Text)
	if site := strings.TrimSpace(q.Site); site != "" {
		site = strings.TrimPrefix(strings.TrimPrefix(site, "https://"), "http://")
		site = strings.Trim(site, "/")
		if site != "" {
			text += " site:" + site
		}
	}
	return text
}

// findAnchorWithHref returns the first descendant anchor carrying an href.
func findAnchorWithHref(root *html.Node) *html.Node {
	if root.Type == html.ElementNode && root.Data == "a" && htmlAttr(root, "href") != "" {
		return root
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if found := findAnchorWithHref(c); found != nil {
			return found
		}
	}
	return nil
}

// findElementByClassPrefix finds the first descendant of the given tag whose
// class list starts with prefix. Matching the prefix rather than the whole
// attribute is what survives the build hash Brave appends to every class.
func findElementByClassPrefix(root *html.Node, tag, prefix string) *html.Node {
	if root.Type == html.ElementNode && root.Data == tag {
		if strings.HasPrefix(strings.TrimSpace(htmlAttr(root, "class")), prefix) {
			return root
		}
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if found := findElementByClassPrefix(c, tag, prefix); found != nil {
			return found
		}
	}
	return nil
}

// stripHTMLTags removes the markup an API embeds in a description.
func stripHTMLTags(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(htmlText(doc))
}
