//go:build swarm

package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

type statisticsNode struct {
	Name    string                  `json:"name"`
	Dataset string                  `json:"dataset"`
	Path    []string                `json:"path"`
	Import  statistics.ImportStatus `json:"import"`
}

// statisticsEnvelope is what a relay answers and what it takes from another
// relay. Reports carries one partial aggregate per dataset: a node summarizes
// its own journal under the request's filters, so what travels is a few
// kilobytes of counters rather than its whole history, and a dataset reached
// by two routes is dropped on the second one instead of being added twice.
type statisticsEnvelope struct {
	Reports  []statistics.Report `json:"reports,omitempty"`
	AllTimes []statistics.Report `json:"allTimes,omitempty"`
	Nodes    []statisticsNode    `json:"nodes"`
	Warnings []string            `json:"warnings"`
	Report   statistics.Report   `json:"report"`
	AllTime  statistics.Report   `json:"allTime"`
}

func (s *Server) handleStatistics(w http.ResponseWriter, r *http.Request) {
	f := statistics.Filter{Timezone: r.URL.Query().Get("timezone"), From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to"), Project: r.URL.Query().Get("project"), Provider: r.URL.Query().Get("provider"), Model: r.URL.Query().Get("model"), Source: r.URL.Query().Get("source")}
	if err := f.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	path, err := s.hopPath(r)
	out := statisticsEnvelope{Nodes: []statisticsNode{}, Warnings: []string{}}
	if err != nil {
		if !errors.Is(err, errChainLooped) {
			out.Warnings = append(out.Warnings, err.Error())
		}
		writeJSON(w, 200, out)
		return
	}
	for _, node := range s.registry.List() {
		if !node.Online {
			out.Warnings = append(out.Warnings, node.Name+": offline")
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxFanoutConcurrent)
	for _, node := range s.registry.Online() {
		wg.Add(1)
		go func(node Node) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-r.Context().Done():
				return
			}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.Swarm.EffectiveFanoutTimeoutSeconds())*time.Second)
			defer cancel()
			part, err := s.askStatistics(ctx, node, path, r.URL.RawQuery)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Warnings = append(out.Warnings, node.Info.Name+": "+err.Error())
				return
			}
			out.Reports = append(out.Reports, part.Reports...)
			out.AllTimes = append(out.AllTimes, part.AllTimes...)
			for _, n := range part.Nodes {
				n.Path = append([]string{node.Info.Name}, n.Path...)
				out.Nodes = append(out.Nodes, n)
			}
			for _, warning := range part.Warnings {
				out.Warnings = append(out.Warnings, node.Info.Name+" -> "+warning)
			}
		}(node)
	}
	wg.Wait()
	sort.Slice(out.Nodes, func(i, j int) bool {
		if len(out.Nodes[i].Path) == len(out.Nodes[j].Path) {
			return strings.Join(out.Nodes[i].Path, "/") < strings.Join(out.Nodes[j].Path, "/")
		}
		return len(out.Nodes[i].Path) < len(out.Nodes[j].Path)
	})
	seen := map[string]bool{}
	nodes := out.Nodes[:0]
	for _, n := range out.Nodes {
		if !seen[n.Dataset] {
			nodes = append(nodes, n)
			seen[n.Dataset] = true
		}
	}
	out.Nodes = nodes
	// `node` names a dataset: one machine's counters out of the fleet's.
	if only := r.URL.Query().Get("node"); only != "" {
		out.Reports = keepDataset(out.Reports, only)
		out.AllTimes = keepDataset(out.AllTimes, only)
	}
	now := time.Now()
	out.Report, _ = statistics.MergeReports(out.Reports, f, now)
	out.AllTime, _ = statistics.MergeReports(out.AllTimes, statistics.Filter{Timezone: f.Timezone}, now)
	// Another relay merges the partials itself, so they travel one hop further
	// under raw=1; a browser gets the merged report alone.
	if r.URL.Query().Get("raw") != "1" {
		out.Reports, out.AllTimes = nil, nil
	}
	sort.Strings(out.Warnings)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

func keepDataset(reports []statistics.Report, dataset string) []statistics.Report {
	kept := reports[:0]
	for _, r := range reports {
		if r.Dataset == dataset {
			kept = append(kept, r)
		}
	}
	return kept
}

// askStatistics asks one node to summarize its own journal under the
// request's filters. The whole history never travels: a node answers with the
// counters of the period asked for, so a long history costs the same few
// kilobytes on the wire as a short one.
func (s *Server) askStatistics(ctx context.Context, node Node, hops []string, query string) (statisticsEnvelope, error) {
	var out statisticsEnvelope
	if node.Transport == nil || !node.Transport.Alive() || node.Transport.TargetURL() == nil {
		return out, fmt.Errorf("not reachable")
	}
	target := node.Transport.TargetURL()
	// node and raw are the relay's own; the rest is what a leaf filters by.
	forward := url.Values{}
	if asked, err := url.ParseQuery(query); err == nil {
		for _, key := range []string{"timezone", "from", "to", "project", "provider", "model", "source"} {
			if v := asked.Get(key); v != "" {
				forward.Set(key, v)
			}
		}
	}
	path := "/coddy/statistics"
	isRelay := node.Info.Kind == swarmdto.KindRelay
	if isRelay {
		forward.Set("raw", "1")
		path = "/swarm/statistics"
	}
	if encoded := forward.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Scheme+"://"+target.Host+strings.TrimRight(target.Path, "/")+path, nil)
	if err != nil {
		return out, err
	}
	if node.Token != "" {
		req.Header.Set("Authorization", "Bearer "+node.Token)
	}
	if isRelay {
		req.Header.Set(SwarmPathHeader, strings.Join(hops, ","))
	}
	res, err := node.Transport.RoundTripper().RoundTrip(req)
	if err != nil {
		return out, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return out, fmt.Errorf("%s", res.Status)
	}
	// Refuse an oversized contribution explicitly, rather than returning a
	// silently truncated success. A summary is kilobytes, so this is a guard
	// against a node answering something else, not a bound a fleet can reach.
	body, err := io.ReadAll(io.LimitReader(res.Body, (64<<20)+1))
	if err != nil {
		return out, err
	}
	if len(body) > 64<<20 {
		return out, fmt.Errorf("statistics payload exceeds 64 MiB")
	}
	if isRelay {
		err = json.Unmarshal(body, &out)
		return out, err
	}
	var leaf struct {
		Report   statistics.Report   `json:"report"`
		AllTime  statistics.Report   `json:"allTime"`
		Settings statistics.Settings `json:"settings"`
	}
	if err := json.Unmarshal(body, &leaf); err != nil {
		return out, err
	}
	if leaf.Report.Dataset == "" {
		return out, fmt.Errorf("missing statistics identity")
	}
	out.Reports = []statistics.Report{leaf.Report}
	out.AllTimes = []statistics.Report{leaf.AllTime}
	out.Nodes = []statisticsNode{{Name: node.Info.Name, Dataset: leaf.Report.Dataset, Path: []string{}, Import: leaf.Settings.Import}}
	return out, nil
}
