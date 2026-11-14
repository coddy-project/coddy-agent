//go:build http

package httpserver

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

func (s *Server) auxiliaryStatistics(ctx context.Context, source string) (context.Context, func()) {
	cfg := s.activeCfg()
	if cfg == nil || cfg.Paths.Home == "" {
		return ctx, func() {}
	}
	ctx, recorder := statistics.Begin(ctx, statistics.New(cfg.Paths.Home), statistics.Event{Source: source}, func(err error) { s.log.Warn("statistics could not be saved", "error", err) })
	return ctx, recorder.Finish
}

func (s *Server) registerStatisticsRoutes() {
	s.mux.HandleFunc("GET /coddy/statistics", s.statisticsGet)
	s.mux.HandleFunc("GET /coddy/statistics/export", s.statisticsExport)
	s.mux.HandleFunc("PUT /coddy/statistics/settings", s.statisticsSettings)
	s.mux.HandleFunc("POST /coddy/statistics/import", s.statisticsImport)
	s.mux.HandleFunc("DELETE /coddy/statistics", s.statisticsClear)
}

func (s *Server) statisticsStore(w http.ResponseWriter) *statistics.Store {
	if s.mgr == nil || strings.TrimSpace(s.mgr.Cfg().Paths.Home) == "" {
		http.Error(w, "statistics storage is unavailable", http.StatusServiceUnavailable)
		return nil
	}
	return s.mgr.Statistics()
}

func statisticsFilter(r *http.Request) statistics.Filter {
	q := r.URL.Query()
	return statistics.Filter{Timezone: q.Get("timezone"), From: q.Get("from"), To: q.Get("to"), Project: q.Get("project"), Provider: q.Get("provider"), Model: q.Get("model"), Source: q.Get("source")}
}

func statisticsJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) statisticsGet(w http.ResponseWriter, r *http.Request) {
	store := s.statisticsStore(w)
	if store == nil {
		return
	}
	f := statisticsFilter(r)
	if err := f.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// The skills of the chat's workspace, resolved the way every other
	// cwd-scoped listing resolves one: a caller must not point the count at
	// any folder of the machine.
	cwd, ok := s.resolveListingCWD(w, r)
	if !ok {
		return
	}
	settings, err := s.mgr.StatisticsImportStatus()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	report, all, err := store.Page(f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	statisticsJSON(w, 200, map[string]any{"report": report, "allTime": all, "settings": settings, "installedSkills": s.mgr.StatisticsSkillCount(cwd)})
}

func (s *Server) statisticsSettings(w http.ResponseWriter, r *http.Request) {
	store := s.statisticsStore(w)
	if store == nil {
		return
	}
	// A field the body does not name keeps its value: the route is public, and
	// a client saving only the time zone used to drop every custom tariff.
	var body struct {
		Timezone *string                      `json:"timezone"`
		Prices   *map[string]statistics.Price `json:"prices"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.Timezone != nil && *body.Timezone != "" {
		if _, err := time.LoadLocation(*body.Timezone); err != nil {
			http.Error(w, "invalid timezone", 400)
			return
		}
	}
	if body.Prices != nil {
		for name, p := range *body.Prices {
			if strings.TrimSpace(name) == "" || len(name) > 512 {
				http.Error(w, "invalid model name", 400)
				return
			}
			rates := []float64{p.Input, p.Output}
			if p.Cached != nil {
				rates = append(rates, *p.Cached)
			}
			for _, n := range rates {
				if n < 0 || math.IsInf(n, 0) || math.IsNaN(n) {
					http.Error(w, "prices must be finite and non-negative", 400)
					return
				}
			}
		}
	}
	if err := store.UpdateSettings(func(st *statistics.Settings) error {
		if body.Timezone != nil {
			st.Timezone = *body.Timezone
		}
		if body.Prices != nil {
			st.Prices = *body.Prices
		}
		return nil
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	st, err := store.Settings()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	statisticsJSON(w, 200, st)
}

func (s *Server) statisticsImport(w http.ResponseWriter, r *http.Request) {
	store := s.statisticsStore(w)
	if store == nil {
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	switch body.Action {
	case "start":
		if err := s.mgr.StartStatisticsImport(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "skip":
		if err := store.UpdateSettings(func(st *statistics.Settings) error {
			if st.Import.State == "running" {
				return fmt.Errorf("import is running")
			}
			st.Import.State = "skipped"
			return nil
		}); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	default:
		http.Error(w, "action must be start or skip", 400)
		return
	}
	st, err := s.mgr.StatisticsImportStatus()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	statisticsJSON(w, http.StatusAccepted, st.Import)
}

func (s *Server) statisticsClear(w http.ResponseWriter, r *http.Request) {
	store := s.statisticsStore(w)
	if store == nil {
		return
	}
	now := time.Now()
	cutoff := now
	switch r.URL.Query().Get("keep") {
	case "none":
	case "day":
		cutoff = now.Add(-24 * time.Hour)
	case "week":
		cutoff = now.Add(-7 * 24 * time.Hour)
	case "month":
		cutoff = now.AddDate(0, -1, 0)
	default:
		http.Error(w, "keep must be none, day, week or month", 400)
		return
	}
	if err := store.Clear(cutoff); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	statisticsJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) statisticsExport(w http.ResponseWriter, r *http.Request) {
	store := s.statisticsStore(w)
	if store == nil {
		return
	}
	f := statisticsFilter(r)
	if err := f.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	report, err := store.Query(f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Query().Get("format") {
	case "json":
		w.Header().Set("Content-Disposition", `attachment; filename="coddy-statistics.json"`)
		statisticsJSON(w, 200, report)
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="coddy-statistics.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"date", "timezone", "input_tokens", "output_tokens", "cached_tokens", "total_tokens", "requests"})
		str := func(n *int64) string {
			if n == nil {
				return ""
			}
			return strconv.FormatInt(*n, 10)
		}
		for _, d := range report.Days {
			_ = cw.Write([]string{d.Date, report.Timezone, str(d.Input), str(d.Output), str(d.Cached), str(d.Total), strconv.FormatInt(d.Requests, 10)})
		}
		cw.Flush()
	default:
		http.Error(w, "format must be json or csv", 400)
	}
}
