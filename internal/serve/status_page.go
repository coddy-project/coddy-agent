package serve

import (
	"encoding/json"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

// What decides that something needs attention. Each is a fixed figure, not a
// setting: the rules of examples/prometheus/coddy-rules.yml carry the same
// ones, and a test holds all three to those rules.
const (
	// DiskLowBytes is the room on the sessions disk below which it needs
	// attention: a session grows by what a conversation and its tool output
	// weigh, and the disk is the one thing a process cannot make room on.
	DiskLowBytes = 512 << 20
	// ProviderFailingWindow is how long a provider failure keeps the provider
	// listed as failing, unless a success comes after it.
	ProviderFailingWindow = 15 * time.Minute
	// SwarmJoinGrace is how long a registration into a parent relay may stay
	// offline before it needs attention, as the CoddySwarmJoinDown rule has it.
	SwarmJoinGrace = 5 * time.Minute
)

// AttentionCode says what needs attention, in the order Attention lists them.
type AttentionCode string

const (
	// AttentionRestartRequired says a saved change waits for a fresh process
	// and nothing will start one by itself (Snapshot.RestartRequired).
	AttentionRestartRequired AttentionCode = "restart_required"
	// AttentionDiskLow says the sessions disk has less than DiskLowBytes left.
	AttentionDiskLow AttentionCode = "disk_low"
	// AttentionSubsystemDown says a subsystem the configuration enables is not
	// running. It names the subsystem.
	AttentionSubsystemDown AttentionCode = "subsystem_down"
	// AttentionProviderFailing says a provider's calls failed lately and none
	// has succeeded since. It names the provider.
	AttentionProviderFailing AttentionCode = "provider_failing"
	// AttentionSwarmJoinDown says registrations into a parent relay have been
	// offline for SwarmJoinGrace. It names the relay and since when.
	AttentionSwarmJoinDown AttentionCode = "swarm_join_down"
)

// AttentionItem is one thing that needs attention. Only the members its code
// names are set.
type AttentionItem struct {
	Code      AttentionCode `json:"code"`
	Subsystem Kind          `json:"subsystem,omitempty"`
	Provider  string        `json:"provider,omitempty"`
	// Relay is the host[:port] of the parent relay, and OfflineSince the
	// earliest start of an outage among the registrations into it.
	Relay        string    `json:"relay,omitempty"`
	OfflineSince time.Time `json:"offlineSince,omitzero"`
}

// MetricsBlock is what the metrics exporter of a binary built with it keeps:
// the figures per provider, by name, and the last hour of its samples. A
// binary without the exporter has none, and the status document then has no
// metrics member and no failing provider. The block is also the member of the
// document: its tags are the wire shape.
type MetricsBlock struct {
	Providers []ProviderStats `json:"providers"`
	Series    Series          `json:"series"`
}

// ProviderStats is what the exporter has counted for one LLM provider since
// the process started.
type ProviderStats struct {
	// Name is the provider's name in the configuration.
	Name string `json:"name"`
	// Attempts counts the calls made to the provider, successful or not.
	Attempts uint64 `json:"attempts"`
	// Failures counts the attempts that ended in a failure outcome.
	Failures uint64 `json:"failures"`
	// MeanSeconds is the mean duration of an attempt, nil while there is no
	// duration to average.
	MeanSeconds *float64 `json:"meanSeconds,omitempty"`
	// LastFailureAt is when a failure was last counted, LastOKAt a success;
	// zero when none was.
	LastFailureAt time.Time `json:"lastFailureAt,omitzero"`
	LastOKAt      time.Time `json:"lastOkAt,omitzero"`
}

// Series is a window of samples, oldest first, the last one taken at End and
// each one StepSeconds before the next. All the slices have the same length. A
// NaN is a gap: no sample was taken, or none could be computed (the mean of a
// window with no attempt), and the document sends it as null.
type Series struct {
	// StepSeconds is the time between two samples.
	StepSeconds float64 `json:"stepSeconds"`
	// End is when the last sample was taken.
	End time.Time `json:"end"`
	// AttemptsOK and ProviderErrors are provider attempts per minute that
	// succeeded and that failed; AttemptSecondsMean is their mean duration in
	// seconds.
	AttemptsOK         Samples `json:"attemptsOk"`
	ProviderErrors     Samples `json:"providerErrors"`
	AttemptSecondsMean Samples `json:"attemptSecondsMean"`
	// TokensIn and TokensOut are tokens per minute sent to and received from
	// the providers.
	TokensIn  Samples `json:"tokensIn"`
	TokensOut Samples `json:"tokensOut"`
	// TurnsRunning and TasksUnfinished are the turns running and the
	// unfinished background tasks at each sample.
	TurnsRunning    Samples `json:"turnsRunning"`
	TasksUnfinished Samples `json:"tasksUnfinished"`
}

// SetMetricsSource names the function that reads the exporter's metrics block
// while the status document is built, or with nil takes it away. The exporter
// sets it when it starts and clears it when it stops, so a binary without the
// exporter, and a process whose exporter has stopped, have no such block.
func (r *Runtime) SetMetricsSource(source func() *MetricsBlock) {
	r.metricsMu.Lock()
	r.metrics = source
	r.metricsMu.Unlock()
}

// StatusDocument is Status rendered for the API: what GET /coddy/status
// answers. The operating system's home folder is shown as ~ in the path of
// the configuration file, and the path is left as it is when the home cannot
// be told.
func (r *Runtime) StatusDocument() StatusDocument {
	r.metricsMu.Lock()
	source := r.metrics
	r.metricsMu.Unlock()
	var block *MetricsBlock
	if source != nil {
		block = source()
	}
	home, _ := os.UserHomeDir()
	return RenderStatus(r.Status(), block, time.Now(), home)
}

// StatusObject is the value of the object member of a status document.
const StatusObject = "coddy.status"

// StatusDocument is the status of a process as the API sends it: camelCase
// members, and every optional member left out when it is empty, false or zero
// rather than sent as zero.
type StatusDocument struct {
	Object      string            `json:"object"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Build       Build             `json:"build"`
	Process     ProcessDoc        `json:"process"`
	Subsystems  []SubsystemStatus `json:"subsystems"`
	// RestartRequired is only ever true.
	RestartRequired bool       `json:"restartRequired,omitempty"`
	RestartRefused  string     `json:"restartRefused,omitempty"`
	Turns           TurnsDoc   `json:"turns"`
	Prompts         PromptsDoc `json:"prompts,omitzero"`
	// Tasks counts the unfinished background tasks by kind and status.
	Tasks       map[string]map[string]int `json:"tasks,omitempty"`
	Disk        *Disk                     `json:"disk,omitempty"`
	Unavailable []string                  `json:"unavailable,omitempty"`
	Attention   []AttentionItem           `json:"attention,omitempty"`
	// Metrics is present when the binary has the exporter.
	Metrics *MetricsBlock `json:"metrics,omitempty"`
}

// ProcessDoc is how the running process came to be.
type ProcessDoc struct {
	StartedAt time.Time `json:"startedAt"`
	StartedBy StartedBy `json:"startedBy"`
	// ConfigFile shows the operating system's home folder as ~.
	ConfigFile string `json:"configFile"`
}

// TurnsDoc counts the turns in flight. OldestSeconds is how long the longest
// has run, and is present only while one runs.
type TurnsDoc struct {
	Running       int    `json:"running"`
	OldestSeconds *int64 `json:"oldestSeconds,omitempty"`
}

// PromptsDoc counts the prompts that wait for a person: the document leaves
// out the object when none does, and either count when it is zero.
type PromptsDoc struct {
	Permission int `json:"permission,omitempty"`
	Question   int `json:"question,omitempty"`
}

// Samples is a series of figures in which a gap is NaN in memory and null on
// the wire.
type Samples []float64

// MarshalJSON writes a gap, a NaN or an infinity, as null, and a series with
// nothing in it as an empty array.
func (s Samples) MarshalJSON() ([]byte, error) {
	out := make([]*float64, len(s))
	for i := range s {
		if !math.IsNaN(s[i]) && !math.IsInf(s[i], 0) {
			out[i] = &s[i]
		}
	}
	return json.Marshal(out)
}

// RenderStatus builds the status document of a snapshot at now. m is the
// metrics exporter's block, nil when the binary has none: the document then
// has no metrics member and no failing provider. home is the operating
// system's home folder, shown as ~ in the path of the configuration file.
// Times are in UTC. The document shares the snapshot's slices and the block's
// series, read-only like them; the providers are a copy.
func RenderStatus(s Snapshot, m *MetricsBlock, now time.Time, home string) StatusDocument {
	now = now.UTC()
	doc := StatusDocument{
		Object:      StatusObject,
		GeneratedAt: now,
		Build:       s.Build,
		Process: ProcessDoc{
			StartedAt: s.StartedAt.UTC(), StartedBy: s.StartedBy, ConfigFile: tildeHome(s.ConfigFile, home),
		},
		Subsystems:      s.Subsystems,
		RestartRequired: s.RestartRequired,
		RestartRefused:  s.RestartRefused,
		Turns:           TurnsDoc{Running: s.TurnsRunning},
		Prompts:         PromptsDoc{Permission: s.PermissionPrompts, Question: s.Questions},
		Tasks:           tasksDoc(s.Tasks),
		Unavailable:     s.Unavailable,
	}
	if doc.Subsystems == nil {
		doc.Subsystems = []SubsystemStatus{}
	}
	if s.TurnsRunning > 0 && !s.OldestTurnStartedAt.IsZero() {
		seconds := max(int64(now.Sub(s.OldestTurnStartedAt)/time.Second), 0)
		doc.Turns.OldestSeconds = &seconds
	}
	if s.Disk != nil {
		disk := *s.Disk
		doc.Disk = &disk
	}

	var providers []ProviderStats
	if m != nil {
		providers = sortedByName(m.Providers)
		doc.Metrics = &MetricsBlock{Providers: providers, Series: m.Series}
		doc.Metrics.Series.End = m.Series.End.UTC()
	}
	doc.Attention = attention(s, providers, now)
	return doc
}

// attention lists what needs the operator's attention, in a fixed order: a
// restart that is due, a low disk, each stopped subsystem in row order, each
// failing provider by name, then each relay that registrations have been
// offline from, by host. providers is nil for a binary without the metrics
// exporter, which therefore lists no provider.
//
// A subsystem that is only starting is not down, and neither is one the
// configuration disabled and the process still runs (retained). A provider is
// failing when a failure was counted within ProviderFailingWindow of now, up
// to and including its edge, and no success was counted at the same moment or
// after it. A relay is down when a registration into it, of any kind, is
// offline, has a start, and has been for SwarmJoinGrace; it is one item
// whatever the number of registrations, since to the operator it is one
// relay, and carries the earliest start.
func attention(s Snapshot, providers []ProviderStats, now time.Time) []AttentionItem {
	var items []AttentionItem
	if s.RestartRequired {
		items = append(items, AttentionItem{Code: AttentionRestartRequired})
	}
	if s.Disk != nil && s.Disk.AvailableBytes < DiskLowBytes {
		items = append(items, AttentionItem{Code: AttentionDiskLow})
	}
	for i := range s.Subsystems {
		if row := &s.Subsystems[i]; row.State == StateStopped {
			items = append(items, AttentionItem{Code: AttentionSubsystemDown, Subsystem: row.Kind})
		}
	}
	for i := range providers {
		if p := &providers[i]; p.failing(now) {
			items = append(items, AttentionItem{Code: AttentionProviderFailing, Provider: p.Name})
		}
	}
	down := map[string]time.Time{}
	var relays []string
	for i := range s.SwarmJoins {
		j := &s.SwarmJoins[i]
		if j.Online || j.OfflineSince.IsZero() || now.Sub(j.OfflineSince) < SwarmJoinGrace {
			continue
		}
		since, seen := down[j.Relay]
		if !seen {
			relays = append(relays, j.Relay)
		}
		if !seen || j.OfflineSince.Before(since) {
			down[j.Relay] = j.OfflineSince
		}
	}
	sort.Strings(relays)
	for _, relay := range relays {
		items = append(items, AttentionItem{Code: AttentionSwarmJoinDown, Relay: relay, OfflineSince: down[relay].UTC()})
	}
	return items
}

// failing reports whether the provider's calls failed lately and none has
// succeeded since.
func (p *ProviderStats) failing(now time.Time) bool {
	if p.LastFailureAt.IsZero() || now.Sub(p.LastFailureAt) > ProviderFailingWindow {
		return false
	}
	return p.LastOKAt.Before(p.LastFailureAt)
}

// sortedByName returns a copy of providers ordered by name, never nil, with
// its times in UTC.
func sortedByName(providers []ProviderStats) []ProviderStats {
	out := append(make([]ProviderStats, 0, len(providers)), providers...)
	for i := range out {
		out[i].LastFailureAt, out[i].LastOKAt = out[i].LastFailureAt.UTC(), out[i].LastOKAt.UTC()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// tasksDoc copies the counts that are above zero, and nothing when there are none.
func tasksDoc(in map[string]map[string]int) map[string]map[string]int {
	var out map[string]map[string]int
	for kind, byStatus := range in {
		for status, n := range byStatus {
			if n <= 0 {
				continue
			}
			if out == nil {
				out = make(map[string]map[string]int)
			}
			if out[kind] == nil {
				out[kind] = make(map[string]int)
			}
			out[kind][status] = n
		}
	}
	return out
}

// tildeHome writes the home folder at the start of path as ~, on this
// platform's terms: Windows and macOS ignore case in paths, and only Windows
// takes a backslash for a separator.
func tildeHome(path, home string) string {
	return tilde(path, home, runtime.GOOS == "windows" || runtime.GOOS == "darwin", runtime.GOOS == "windows")
}

// tilde replaces the folder home at the start of path with ~. The folder
// matches on whole path elements only, so /home/al is not the start of
// /home/alice, and the separators of the rest of the path are kept as they
// are. fold is true where paths ignore case, backslash where a backslash
// separates names. An empty home, or one that is a disk's root, which ~ would
// stand for whole, leaves the path as it is.
func tilde(path, home string, fold, backslash bool) string {
	isSeparator := func(c byte) bool { return c == '/' || (backslash && c == '\\') }
	for home != "" && isSeparator(home[len(home)-1]) {
		home = home[:len(home)-1]
	}
	if home == "" || (backslash && len(home) == 2 && home[1] == ':') || len(path) < len(home) {
		return path
	}
	if head := path[:len(home)]; head != home && (!fold || !strings.EqualFold(head, home)) {
		return path
	}
	rest := path[len(home):]
	switch {
	case rest == "":
		return "~"
	case isSeparator(rest[0]):
		return "~" + rest
	}
	return path
}
