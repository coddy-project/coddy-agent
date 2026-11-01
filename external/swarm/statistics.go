//go:build swarm

package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

type statisticsEnvelope struct {
	Events   []statistics.Event `json:"events,omitempty"`
	Nodes    []statisticsNode   `json:"nodes"`
	Warnings []string           `json:"warnings"`
	Report   statistics.Report  `json:"report"`
	AllTime  statistics.Report  `json:"allTime"`
}

func (s *Server) handleStatistics(w http.ResponseWriter, r *http.Request) {
	f := statistics.Filter{Timezone: r.URL.Query().Get("timezone"), From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to"), Project: r.URL.Query().Get("project"), Provider: r.URL.Query().Get("provider"), Model: r.URL.Query().Get("model"), Source: r.URL.Query().Get("source")}
	if err := f.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	path, err := s.hopPath(r)
	out := statisticsEnvelope{Events: []statistics.Event{}, Nodes: []statisticsNode{}, Warnings: []string{}}
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
			part, err := s.askStatistics(ctx, node, path)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Warnings = append(out.Warnings, node.Info.Name+": "+err.Error())
				return
			}
			out.Events = append(out.Events, part.Events...)
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
	// Dedupe before forwarding to another relay too, bounding a ring's payload.
	latest := map[string]statistics.Event{}
	for _, e := range out.Events {
		key := e.Dataset + ":" + e.ID
		if old, ok := latest[key]; !ok || e.RecordedAt.After(old.RecordedAt) {
			latest[key] = e
		}
	}
	out.Events = make([]statistics.Event, 0, len(latest))
	for _, e := range latest {
		out.Events = append(out.Events, e)
	}
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
	if only := r.URL.Query().Get("node"); only != "" {
		filtered := out.Events[:0]
		for _, e := range out.Events {
			if e.Dataset == only {
				filtered = append(filtered, e)
			}
		}
		out.Events = filtered
	}
	out.Report, _ = statistics.Summarize(out.Events, f, nil, time.Now())
	out.AllTime, _ = statistics.Summarize(out.Events, statistics.Filter{Timezone: f.Timezone}, nil, time.Now())
	if r.URL.Query().Get("raw") != "1" {
		out.Events = nil
	}
	sort.Strings(out.Warnings)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

func (s *Server) askStatistics(ctx context.Context, node Node, hops []string) (statisticsEnvelope, error) {
	var out statisticsEnvelope
	if node.Transport == nil || !node.Transport.Alive() || node.Transport.TargetURL() == nil {
		return out, fmt.Errorf("not reachable")
	}
	target := node.Transport.TargetURL()
	path := "/coddy/statistics/events"
	isRelay := node.Info.Kind == swarmdto.KindRelay
	if isRelay {
		path = "/swarm/statistics?raw=1"
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
	// silently truncated success. No prompt text travels in this payload.
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
		Dataset string                  `json:"dataset"`
		Events  []statistics.Event      `json:"events"`
		Import  statistics.ImportStatus `json:"import"`
	}
	if err := json.Unmarshal(body, &leaf); err != nil {
		return out, err
	}
	if leaf.Dataset == "" {
		return out, fmt.Errorf("missing statistics identity")
	}
	out.Events = leaf.Events
	out.Nodes = []statisticsNode{{Name: node.Info.Name, Dataset: leaf.Dataset, Path: []string{}, Import: leaf.Import}}
	return out, nil
}
