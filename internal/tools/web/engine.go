package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Engine names accepted in tools.websearch.engines.
const (
	EngineBrave   = "brave"
	EngineBing    = "bing"
	EngineDDG     = "ddg"
	EngineGoogle  = "google"
	EngineSearXNG = "searxng"
)

// KnownEngines lists every backend the tool can be asked for, in the order the
// listing is printed for an operator who misspelled one.
func KnownEngines() []string {
	return []string{EngineBrave, EngineBing, EngineDDG, EngineGoogle, EngineSearXNG}
}

// Result is one row of a search engine's answer.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Outcome is what one backend did for one query. It is the distinction the old
// implementation could not make: an engine that answered with an anti-bot page
// and an engine that honestly found nothing both used to arrive as an empty
// slice, so the merged answer said "no results" either way.
type Outcome string

const (
	// OutcomeOK means the engine answered and the parser found rows.
	OutcomeOK Outcome = "ok"
	// OutcomeEmpty means the engine answered a page the parser understood and
	// there was nothing on it. This is the only honest "the web has nothing".
	OutcomeEmpty Outcome = "empty"
	// OutcomeBlocked means the engine answered something that is not a result
	// page: a challenge, an interstitial, a layout the parser no longer knows,
	// or a batch of rows that has nothing to do with the query.
	OutcomeBlocked Outcome = "blocked"
	// OutcomeError means the engine could not be reached or refused the call.
	OutcomeError Outcome = "error"
)

// Query is one search request as the engines receive it. Site is an optional
// domain restriction each engine maps to its own syntax.
type Query struct {
	Text       string
	Page       int
	MaxResults int
	Site       string
}

// searchFn is one backend. It returns rows, or an error; a blockedError says
// the engine answered but not with results, which is reported rather than
// merged.
type searchFn func(ctx context.Context, q Query, s Settings) ([]Result, error)

// blockedError marks an engine that answered without results because something
// stood between the request and the index. It is separate from an ordinary
// error so the report can say why the engine contributed nothing, and separate
// from an empty answer so the model is never told the web is silent when it is
// only the scraper that was turned away.
type blockedError struct{ reason string }

func (e *blockedError) Error() string { return "blocked: " + e.reason }

func blocked(format string, args ...interface{}) error {
	return &blockedError{reason: fmt.Sprintf(format, args...)}
}

// asBlocked reports whether err is a blockedError and returns its reason.
func asBlocked(err error) (string, bool) {
	var b *blockedError
	if errors.As(err, &b) {
		return b.reason, true
	}
	return "", false
}

// Report is one engine's line in the answer: what it did, how many rows it
// contributed after merging, and why when it contributed none.
type Report struct {
	Engine  string  `json:"engine"`
	Status  Outcome `json:"status"`
	Results int     `json:"results"`
	Reason  string  `json:"reason,omitempty"`
	TookMS  int64   `json:"took_ms"`
	// Cached marks an answer reused from a recent identical search rather than
	// asked again, so a report showing 0 ms is readable.
	Cached bool `json:"cached,omitempty"`
}

// engineRun is the internal result of firing one backend.
type engineRun struct {
	name   string
	rows   []Result
	report Report
}

// engines maps a configured name to its backend. The seams are package
// variables so tests can swap a backend without a live call, the way the
// original googleSearchFunc and bingSearchFunc did.
func engineFor(name string) (searchFn, bool) {
	switch name {
	case EngineBrave:
		return runBrave, true
	case EngineBing:
		return runBing, true
	case EngineDDG:
		return runDDG, true
	case EngineGoogle:
		return runGoogle, true
	case EngineSearXNG:
		return runSearXNG, true
	default:
		return nil, false
	}
}

// runEngines fires each configured backend in parallel under its own timeout,
// applies the relevance gate to what comes back, and returns the runs in the
// configured order. The whole call is bounded by the settings' total budget, so
// one hanging engine cannot hold the turn.
func runEngines(ctx context.Context, names []string, q Query, s Settings) []engineRun {
	total := s.TotalTimeout()
	ctx, cancel := context.WithTimeout(ctx, total)
	defer cancel()

	runs := make([]engineRun, len(names))
	sem := make(chan struct{}, s.MaxConcurrent())
	done := make(chan int, len(names))

	for i, name := range names {
		go func(i int, name string) {
			defer func() { done <- i }()
			sem <- struct{}{}
			defer func() { <-sem }()

			started := time.Now()
			run := engineRun{name: name}
			fn, ok := engineFor(name)
			if !ok {
				run.report = Report{Engine: name, Status: OutcomeError, Reason: "unknown engine"}
				runs[i] = run
				return
			}
			key := cacheKey(name, q, s)
			rows, err, cached := searchCache.get(key)
			if !cached {
				engCtx, engCancel := context.WithTimeout(ctx, s.EngineTimeout())
				rows, err = fn(engCtx, q, s)
				// Read the deadline before cancelling: after engCancel the
				// context always reports an error, which would make every
				// answer look like a timeout and never be remembered.
				expired := engCtx.Err() != nil
				engCancel()
				// A call the turn cut short says nothing about the engine, so
				// it is not remembered as the engine's answer.
				if !expired && ctx.Err() == nil {
					searchCache.put(key, rows, err, s.CacheTTL())
				}
			}
			run.report = classify(name, q, rows, err, time.Since(started))
			run.report.Cached = cached
			if run.report.Status == OutcomeOK {
				run.rows = rows
			}
			runs[i] = run
		}(i, name)
	}
	for range names {
		<-done
	}
	return runs
}

// classify turns one backend's raw answer into its reported outcome. It is the
// single place that decides what counts as blocked, so every engine is judged
// the same way and the decoy gate cannot be forgotten for a new backend.
func classify(name string, q Query, rows []Result, err error, took time.Duration) Report {
	rep := Report{Engine: name, TookMS: took.Milliseconds()}
	if err != nil {
		if reason, ok := asBlocked(err); ok {
			rep.Status, rep.Reason = OutcomeBlocked, reason
			return rep
		}
		rep.Status, rep.Reason = OutcomeError, err.Error()
		return rep
	}
	if len(rows) == 0 {
		rep.Status = OutcomeEmpty
		return rep
	}
	if reason, isDecoy := decoyReason(q.Text, rows); isDecoy {
		rep.Status, rep.Reason = OutcomeBlocked, reason
		return rep
	}
	rep.Status, rep.Results = OutcomeOK, len(rows)
	return rep
}

// httpGet performs one engine request with the shared browser-shaped headers
// and returns the body, refusing a non-2xx answer as blocked. Every HTML
// backend goes through here so the status handling is written once.
func httpGet(ctx context.Context, rawURL string, hdr map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusAccepted {
		// DuckDuckGo answers a scraper with 202 and a challenge page rather
		// than a status a client would retry on.
		return nil, blocked("http 202 anti-bot interstitial")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, blocked("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSERPBytes))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// browserUserAgent is what the HTML backends present. A search engine serves a
// scraper-shaped client either nothing or a challenge.
const browserUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// maxSERPBytes caps one engine's response body.
const maxSERPBytes = 4 << 20

// challengeMarkers are the phrases a challenge page carries. They separate "the
// engine answered a page with no results on it" from "the engine answered a
// page that is not a result page at all", which is the difference between an
// empty answer and a blocked one.
var challengeMarkers = []string{
	"captcha",
	"unusual traffic",
	"verifying your browser",
	"are you a robot",
	"enable javascript and cookies",
	"access denied",
	"/sorry/index",
}

// challengeReason reports the marker a body carries, if any. Only the head of
// the document is examined: a marker deep inside a real result page is far more
// likely to be a page about captchas than a challenge.
func challengeReason(body []byte) (string, bool) {
	head := body
	if len(head) > 64<<10 {
		head = head[:64<<10]
	}
	lower := strings.ToLower(string(head))
	for _, m := range challengeMarkers {
		if strings.Contains(lower, m) {
			return "challenge page (" + m + ")", true
		}
	}
	return "", false
}
