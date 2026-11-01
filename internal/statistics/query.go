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
		p = Price{Input: catalog.Input, Output: catalog.Output, Cached: catalog.Input}
		if catalog.Cached != nil {
			p.Cached = *catalog.Cached
		}
	}
	cached := int64(0)
	if e.Cached != nil {
		cached = min(*e.Cached, *e.Input)
	}
	v := (float64(*e.Input-cached)*p.Input + float64(cached)*p.Cached + float64(*e.Output)*p.Output) / 1e6
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
	st, err := s.Settings()
	if err != nil {
		return Report{}, err
	}
	if st.Timezone != "" {
		f.Timezone = st.Timezone
	}
	events, err := s.Events()
	if err != nil {
		return Report{}, err
	}
	r, err := Summarize(events, f, st.Prices, time.Now())
	r.Dataset = st.Dataset
	return r, err
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
		if e.Requests > 0 && e.Source != "subagent" && e.Source != "memory" && e.Source != "scheduler" && e.Source != "background" {
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
	var dates []string
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
	rows := func(m map[string]*Breakdown) []Breakdown {
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
	r.Models, r.Providers, r.Projects, r.Sources, r.Reasoning, r.Skills = rows(models), rows(providers), rows(projects), rows(sources), rows(reasoning), rows(skills)
	return r, nil
}
