package web

import (
	"net/url"
	"strings"
)

// trackingPrefixes are query-key prefixes that only ever identify a campaign.
// They are matched as prefixes because they are namespaces: "utm_source",
// "utm_medium" and the rest all name the same referral.
var trackingPrefixes = []string{"utm_", "ref_", "mc_", "pk_", "piwik_"}

// trackingExact are whole query keys that identify a referral rather than a
// page. They are matched exactly, never as a prefix: "ref" is a tracking tag,
// but "refresh" is not, and on a source host "ref=v1" and "ref=v2" are two
// different revisions of one file. A prefix match here merged pages that are
// genuinely different, which costs a result rather than saving one.
var trackingExact = map[string]bool{
	"fbclid": true, "gclid": true, "msclkid": true, "yclid": true,
	"dclid": true, "twclid": true, "igshid": true, "ttclid": true,
	"mc_cid": true, "mc_eid": true, "_hsenc": true, "_hsmi": true,
	"referrer": true, "spm": true, "scm": true,
}

// isTrackingParam reports whether a query key identifies a referral. Note what
// is deliberately absent: "ref", "source" and "id" all select content on some
// host, and dropping them merges pages a reader would want to see separately.
func isTrackingParam(key string) bool {
	lower := strings.ToLower(key)
	if trackingExact[lower] {
		return true
	}
	for _, p := range trackingPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
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
		if isTrackingParam(key) {
			q.Del(key)
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
