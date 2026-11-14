package statistics

import (
	"fmt"
	"sort"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
)

type Filter struct {
	Timezone string `json:"timezone"`
	From     string `json:"from"`
	To       string `json:"to"`
	Project  string `json:"project"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Source   string `json:"source"`
}

type Totals struct {
	Input          *int64   `json:"inputTokens"`
	Output         *int64   `json:"outputTokens"`
	Cached         *int64   `json:"cachedTokens"`
	Total          *int64   `json:"totalTokens"`
	CostUSD        *float64 `json:"costUsd"`
	ActiveMS       *int64   `json:"activeMs"`
	LongestMS      *int64   `json:"longestMs"`
	Requests       int64    `json:"requests"`
	Calls          int64    `json:"calls"`
	Chats          int      `json:"chats"`
	SkillCalls     int64    `json:"skillCalls"`
	UniqueSkills   int      `json:"uniqueSkills"`
	CurrentStreak  int      `json:"currentStreak"`
	LongestStreak  int      `json:"longestStreak"`
	MaxDailyTokens *int64   `json:"maxDailyTokens"`
	UnpricedCalls  int      `json:"unpricedCalls"`
	UnknownUsage   int      `json:"unknownUsage"`
	ImportedTurns  int      `json:"importedTurns"`
}

type Day struct {
	Date     string `json:"date"`
	Input    *int64 `json:"inputTokens"`
	Output   *int64 `json:"outputTokens"`
	Cached   *int64 `json:"cachedTokens"`
	Total    *int64 `json:"totalTokens"`
	Requests int64  `json:"requests"`
	Manual   bool   `json:"manual"`
}

type Breakdown struct {
	Name   string `json:"name"`
	Count  int64  `json:"count"`
	Tokens *int64 `json:"tokens"`
}

type Report struct {
	Dataset   string      `json:"dataset"`
	Timezone  string      `json:"timezone"`
	Totals    Totals      `json:"totals"`
	Days      []Day       `json:"days"`
	Models    []Breakdown `json:"models"`
	Providers []Breakdown `json:"providers"`
	Projects  []Breakdown `json:"projects"`
	Sources   []Breakdown `json:"sources"`
	Reasoning []Breakdown `json:"reasoning"`
	Skills    []Breakdown `json:"skills"`
}

func sum(dst **int64, v *int64) {
	if v == nil {
		return
	}
	if *dst == nil {
		*dst = Int(0)
	}
	**dst += *v
}

// ApplyPrice fills a missing estimate using USD rates per million tokens.
// An explicit zero rate is supported for a locally hosted model.
func ApplyPrice(e *Event, prices map[string]Price) {
	if e.CostUSD != nil || e.Input == nil || e.Output == nil {
		return
	}
	p, ok := prices[e.Provider+"/"+e.Model]
	if !ok {
		p, ok = prices[e.Model]
	}
	if !ok {
		catalog, found := models.LookupPrice(e.Model)
		if !found {
			return
		}
		p = Price{Input: catalog.Input, Output: catalog.Output, Cached: catalog.Cached}
	}
	cached := int64(0)
	if e.Cached != nil {
		cached = min(*e.Cached, *e.Input)
	}
	v := (float64(*e.Input-cached)*p.Input + float64(cached)*p.CachedRate() + float64(*e.Output)*p.Output) / 1e6
	e.CostUSD = &v
}

func (f Filter) Location() (*time.Location, error) {
	if f.Timezone == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(f.Timezone)
}

func (f Filter) Validate() error {
	if _, err := f.Location(); err != nil {
		return err
	}
	for _, v := range []string{f.From, f.To} {
		if v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return fmt.Errorf("invalid date: %w", err)
			}
		}
	}
	if f.From != "" && f.To != "" && f.From > f.To {
		return fmt.Errorf("from must not be after to")
	}
	return nil
}

func (s *Store) Query(f Filter) (Report, error) {
	out, err := s.Reports(f)
	if err != nil {
		return Report{}, err
	}
	return out[0], nil
}

// Reports answers several filters from one read of the journals, reading only
// the journals their periods can touch.
func (s *Store) Reports(filters ...Filter) ([]Report, error) {
	var out []Report
	err := s.locked(func() error {
		st, err := s.settings()
		if err != nil {
			return err
		}
		lower, upper := unionWindow(filters)
		events, err := s.events(st.Cutoff, lower, upper)
		if err != nil {
			return err
		}
		out, err = summarizeEach(events, filters, st, time.Now())
		return err
	})
	return out, err
}

// unionWindow is the journals every one of these periods can be answered
// from. One unbounded period makes the union unbounded.
func unionWindow(filters []Filter) (string, string) {
	lower, upper := "", ""
	for i, f := range filters {
		low, up := f.window()
		if i == 0 {
			lower, upper = low, up
			continue
		}
		if low == "" || lower == "" {
			lower = ""
		} else if low < lower {
			lower = low
		}
		if up == "" || upper == "" {
			upper = ""
		} else if up > upper {
			upper = up
		}
	}
	return lower, upper
}

// summarizeEach applies the store's own settings to every filter: the saved
// zone is what a request naming none is laid out in, and the dataset is what
// a relay tells this machine's contribution apart by.
func summarizeEach(events []Event, filters []Filter, st Settings, now time.Time) ([]Report, error) {
	out := make([]Report, 0, len(filters))
	for _, f := range filters {
		// A request that names a zone wins: a relay has to lay every node of a
		// fleet out on one calendar, or the days of two machines cannot be
		// added at all.
		if f.Timezone == "" {
			f.Timezone = st.Timezone
		}
		r, err := Summarize(events, f, st.Prices, now)
		if err != nil {
			return nil, err
		}
		r.Dataset = st.Dataset
		out = append(out, r)
	}
	return out, nil
}

// Page answers the filtered report a screen draws and the lifetime summary it
// shows beside it. The lifetime one covers everything and so reads every
// journal; it is kept until a journal or the settings move, which leaves the
// usual read of an open screen the period alone.
func (s *Store) Page(f Filter) (Report, Report, error) {
	var report, lifetime Report
	err := s.locked(func() error {
		st, err := s.settings()
		if err != nil {
			return err
		}
		g, err := s.guard()
		if err != nil {
			return err
		}
		zone := f.Timezone
		if zone == "" {
			zone = st.Timezone
		}
		lifetimeFilter := Filter{Timezone: zone}
		stamp, err := s.journalStamp()
		if err != nil {
			return err
		}
		key := stamp + "|" + g.stamp + "|" + zone
		if g.lifetimeKey == key {
			lower, upper := f.window()
			events, err := s.events(st.Cutoff, lower, upper)
			if err != nil {
				return err
			}
			out, err := summarizeEach(events, []Filter{f}, st, time.Now())
			if err != nil {
				return err
			}
			report, lifetime = out[0], g.lifetime
			return nil
		}
		events, err := s.events(st.Cutoff, "", "")
		if err != nil {
			return err
		}
		// The journals outside the period hold no day it covers, so the two
		// reports come from this one read and the narrowed read above answers
		// the period alike.
		out, err := summarizeEach(events, []Filter{f, lifetimeFilter}, st, time.Now())
		if err != nil {
			return err
		}
		report, lifetime = out[0], out[1]
		g.lifetime, g.lifetimeKey = lifetime, key
		return nil
	})
	return report, lifetime, err
}

// Summarize is also used by a relay, which merges leaf datasets before
// computing streaks, distinct chats and daily records (never sums summaries).
func Summarize(events []Event, f Filter, prices map[string]Price, now time.Time) (Report, error) {
	r := Report{Days: []Day{}, Models: []Breakdown{}, Providers: []Breakdown{}, Projects: []Breakdown{}, Sources: []Breakdown{}, Reasoning: []Breakdown{}, Skills: []Breakdown{}}
	if err := f.Validate(); err != nil {
		return r, err
	}
	loc, _ := f.Location()
	r.Timezone = loc.String()
	days := map[string]*Day{}
	chats := map[string]bool{}
	latest := map[string]Event{}
	for _, e := range events {
		key := e.Dataset + ":" + e.ID
		if old, ok := latest[key]; !ok || e.RecordedAt.After(old.RecordedAt) {
			latest[key] = e
		}
	}
	models, providers, projects, sources, reasoning, skills := map[string]*Breakdown{}, map[string]*Breakdown{}, map[string]*Breakdown{}, map[string]*Breakdown{}, map[string]*Breakdown{}, map[string]*Breakdown{}
	add := func(m map[string]*Breakdown, name string, n int64, tokens *int64) {
		if name == "" {
			return
		}
		if m[name] == nil {
			m[name] = &Breakdown{Name: name}
		}
		m[name].Count += n
		sum(&m[name].Tokens, tokens)
	}
	for _, e := range latest {
		date := e.At.In(loc).Format("2006-01-02")
		if (f.From != "" && date < f.From) || (f.To != "" && date > f.To) || (f.Project != "" && e.Project != f.Project) || (f.Provider != "" && e.Provider != f.Provider) || (f.Model != "" && e.Model != f.Model) || (f.Source != "" && e.Source != f.Source) {
			continue
		}
		if e.Session != "" {
			chats[e.Dataset+":"+e.Session] = true
		}
		d := days[date]
		if d == nil {
			d = &Day{Date: date}
			days[date] = d
		}
		r.Totals.Requests += e.Requests
		d.Requests += e.Requests
		if e.Requests > 0 && !NonInteractive(e.Source) {
			d.Manual = true
		}
		if e.Imported && e.Kind == "turn" {
			r.Totals.ImportedTurns++
		}
		sum(&r.Totals.ActiveMS, e.ActiveMS)
		if e.Kind == "turn" && e.Session != "" && e.ActiveMS != nil && (r.Totals.LongestMS == nil || *e.ActiveMS > *r.Totals.LongestMS) {
			r.Totals.LongestMS = Int(*e.ActiveMS)
		}
		var tokens *int64
		if e.Input != nil || e.Output != nil {
			tokens = Int(0)
			sum(&tokens, e.Input)
			sum(&tokens, e.Output)
			sum(&r.Totals.Input, e.Input)
			sum(&r.Totals.Output, e.Output)
			sum(&r.Totals.Cached, e.Cached)
			sum(&r.Totals.Total, tokens)
			sum(&d.Input, e.Input)
			sum(&d.Output, e.Output)
			sum(&d.Cached, e.Cached)
			sum(&d.Total, tokens)
		}
		if e.Kind == "call" {
			r.Totals.Calls++
			if tokens == nil {
				r.Totals.UnknownUsage++
			}
			ApplyPrice(&e, prices)
			cost := e.CostUSD
			if cost == nil {
				r.Totals.UnpricedCalls++
			} else {
				if r.Totals.CostUSD == nil {
					r.Totals.CostUSD = new(float64)
				}
				*r.Totals.CostUSD += *cost
			}
		}
		n := int64(0)
		if e.Kind == "call" {
			n = 1
		}
		add(models, e.Model, n, tokens)
		add(providers, e.Provider, n, tokens)
		turns := int64(0)
		if e.Kind == "turn" {
			turns = 1
		}
		turns = max(turns, e.Requests)
		add(projects, e.Project, turns, tokens)
		add(sources, e.Source, turns, tokens)
		if e.Kind == "call" {
			add(reasoning, e.Reasoning, 1, nil)
		}
		for _, name := range e.Skills {
			add(skills, name, 1, nil)
			r.Totals.SkillCalls++
		}
	}
	r.Totals.Chats = len(chats)
	r.Totals.UniqueSkills = len(skills)
	finishDays(&r, days, loc, now)
	r.Models, r.Providers, r.Projects, r.Sources, r.Reasoning, r.Skills = breakdownRows(models), breakdownRows(providers), breakdownRows(projects), breakdownRows(sources), breakdownRows(reasoning), breakdownRows(skills)
	return r, nil
}

// finishDays writes the daily series in order and derives from it what only
// the whole series can say: the daily record and the streaks. A merge of
// several datasets runs it over their merged days, which is why a relay
// cannot simply add up finished reports.
func finishDays(r *Report, days map[string]*Day, loc *time.Location, now time.Time) {
	dates := make([]string, 0, len(days))
	for date := range days {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	run := 0
	previous := ""
	for _, date := range dates {
		d := days[date]
		r.Days = append(r.Days, *d)
		if d.Total != nil && (r.Totals.MaxDailyTokens == nil || *d.Total > *r.Totals.MaxDailyTokens) {
			r.Totals.MaxDailyTokens = Int(*d.Total)
		}
		if !d.Manual {
			continue
		}
		at, _ := time.ParseInLocation("2006-01-02", date, loc)
		if previous == at.AddDate(0, 0, -1).Format("2006-01-02") {
			run++
		} else {
			run = 1
		}
		previous = date
		r.Totals.LongestStreak = max(r.Totals.LongestStreak, run)
	}
	today := now.In(loc)
	if previous == today.Format("2006-01-02") || previous == today.AddDate(0, 0, -1).Format("2006-01-02") {
		r.Totals.CurrentStreak = run
	}
}

func breakdownRows(m map[string]*Breakdown) []Breakdown {
	out := make([]Breakdown, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	return out
}

// MergeReports combines the reports of several datasets into one.
//
// A report is a partial aggregate that carries the dataset it belongs to, so a
// ring delivering the same dataset by two routes contributes it once: a
// dataset already seen is dropped whole rather than added, which is what makes
// the merge idempotent. What is summed is summed per dataset, never per day
// (a chat belongs to one dataset but may span midnight); the daily series is
// merged first, and the daily record and the streaks are derived from the
// merged series afterwards.
func MergeReports(reports []Report, f Filter, now time.Time) (Report, error) {
	out := Report{Days: []Day{}, Models: []Breakdown{}, Providers: []Breakdown{}, Projects: []Breakdown{}, Sources: []Breakdown{}, Reasoning: []Breakdown{}, Skills: []Breakdown{}}
	if err := f.Validate(); err != nil {
		return out, err
	}
	loc, _ := f.Location()
	out.Timezone = loc.String()
	days := map[string]*Day{}
	models, providers, projects, sources, reasoning, skills := map[string]*Breakdown{}, map[string]*Breakdown{}, map[string]*Breakdown{}, map[string]*Breakdown{}, map[string]*Breakdown{}, map[string]*Breakdown{}
	merge := func(m map[string]*Breakdown, rows []Breakdown) {
		for _, row := range rows {
			if row.Name == "" {
				continue
			}
			if m[row.Name] == nil {
				m[row.Name] = &Breakdown{Name: row.Name}
			}
			m[row.Name].Count += row.Count
			sum(&m[row.Name].Tokens, row.Tokens)
		}
	}
	seen := map[string]bool{}
	for _, r := range reports {
		if r.Dataset != "" {
			if seen[r.Dataset] {
				continue
			}
			seen[r.Dataset] = true
		}
		t := r.Totals
		sum(&out.Totals.Input, t.Input)
		sum(&out.Totals.Output, t.Output)
		sum(&out.Totals.Cached, t.Cached)
		sum(&out.Totals.Total, t.Total)
		sum(&out.Totals.ActiveMS, t.ActiveMS)
		if t.CostUSD != nil {
			if out.Totals.CostUSD == nil {
				out.Totals.CostUSD = new(float64)
			}
			*out.Totals.CostUSD += *t.CostUSD
		}
		if t.LongestMS != nil && (out.Totals.LongestMS == nil || *t.LongestMS > *out.Totals.LongestMS) {
			out.Totals.LongestMS = Int(*t.LongestMS)
		}
		out.Totals.Requests += t.Requests
		out.Totals.Calls += t.Calls
		out.Totals.Chats += t.Chats
		out.Totals.SkillCalls += t.SkillCalls
		out.Totals.UnpricedCalls += t.UnpricedCalls
		out.Totals.UnknownUsage += t.UnknownUsage
		out.Totals.ImportedTurns += t.ImportedTurns
		for _, d := range r.Days {
			day := days[d.Date]
			if day == nil {
				day = &Day{Date: d.Date}
				days[d.Date] = day
			}
			sum(&day.Input, d.Input)
			sum(&day.Output, d.Output)
			sum(&day.Cached, d.Cached)
			sum(&day.Total, d.Total)
			day.Requests += d.Requests
			day.Manual = day.Manual || d.Manual
		}
		merge(models, r.Models)
		merge(providers, r.Providers)
		merge(projects, r.Projects)
		merge(sources, r.Sources)
		merge(reasoning, r.Reasoning)
		merge(skills, r.Skills)
	}
	out.Totals.UniqueSkills = len(skills)
	finishDays(&out, days, loc, now)
	out.Models, out.Providers, out.Projects, out.Sources, out.Reasoning, out.Skills = breakdownRows(models), breakdownRows(providers), breakdownRows(projects), breakdownRows(sources), breakdownRows(reasoning), breakdownRows(skills)
	return out, nil
}
