package web

import (
	"net/url"
	"strings"
)

// trackingParams are query keys that identify a referral rather than a page.
// Two engines linking the same article often differ only by these, and a merged
// answer that lists the page twice wastes a row and a slot in the model's
// attention.
var trackingParams = []string{
	"utm_", "ref_", "fbclid", "gclid", "msclkid", "yclid", "mc_cid", "mc_eid",
	"igshid", "ref", "referrer", "source", "spm",
}

// dedupKey is the identity of a page across engines: scheme folded away, host
// lowercased without a leading www, tracking parameters and the fragment
// dropped, and a trailing slash removed. It is used only for deduplication -
// the URL handed to the model is always the one its engine returned.
func dedupKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		host += ":" + port
	}
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		for _, p := range trackingParams {
			if lower == p || strings.HasPrefix(lower, p) {
				q.Del(key)
				break
			}
		}
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	key := host + path
	if enc := q.Encode(); enc != "" {
		key += "?" + enc
	}
	return key
}

// clipSnippet trims a description to the cap without cutting a word in half.
func clipSnippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-") + "..."
}
