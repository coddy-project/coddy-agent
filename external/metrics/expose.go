//go:build metrics

package metrics

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	vm "github.com/VictoriaMetrics/metrics"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// The text format escapes a label value and a help text each with a set of
// its own: a quote only matters inside a label value.
var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

// series is the name of one sample, name{k="v",...} for the pairs in kv, with
// every value escaped. The library takes it as it is, so a name it would
// reject (a raw line feed) can never get that far.
func series(name string, kv ...string) string {
	if len(kv) < 2 {
		return name
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i])
		b.WriteString(`="`)
		b.WriteString(labelEscaper.Replace(kv[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// family is one metric family and what is printed in front of its samples,
// once. An empty typ prints no TYPE line: the family stays untyped.
type family struct{ name, typ, help string }

func (f family) writeHeader(b *bytes.Buffer) {
	fmt.Fprintf(b, "# HELP %s %s\n", f.name, helpEscaper.Replace(f.help))
	if f.typ != "" {
		fmt.Fprintf(b, "# TYPE %s %s\n", f.name, f.typ)
	}
}

// writeRows writes the header and then rows, or nothing when rows is empty: a
// family with no sample is absent.
func (f family) writeRows(b, rows *bytes.Buffer) {
	if rows.Len() == 0 {
		return
	}
	f.writeHeader(b)
	b.Write(rows.Bytes())
}

// instruments is a family whose samples the library keeps, in a set of its
// own. A set writes its samples sorted by name and nothing else (its HELP and
// TYPE lines only after a process-wide vm.ExposeMetadata, which writeTo drops),
// so a set holding one family is what lets the header go in front of exactly
// its samples. None of the sets is shared with the library's package-level
// one, so nothing else in the process writes into a scrape.
type instruments struct {
	family
	set *vm.Set
}

func newInstruments(f family) *instruments {
	return &instruments{family: f, set: vm.NewSet()}
}

// counter is the counter of the series name{kv...}, created at zero.
func (i *instruments) counter(kv ...string) *vm.Counter {
	return i.set.GetOrCreateCounter(series(i.name, kv...))
}

// histogram is the histogram of the series name{kv...} with real le buckets
// at bounds, created empty.
func (i *instruments) histogram(bounds []float64, kv ...string) *vm.PrometheusHistogram {
	return i.set.GetOrCreatePrometheusHistogramExt(series(i.name, kv...), bounds)
}

func (i *instruments) writeTo(b *bytes.Buffer) {
	var raw, rows bytes.Buffer
	i.set.WritePrometheus(&raw)
	for line := range strings.Lines(raw.String()) {
		if !strings.HasPrefix(line, "#") {
			rows.WriteString(line)
		}
	}
	i.writeRows(b, &rows)
}

// writeLibrary writes the Go runtime and process figures the library reads on
// every call. The open and the allowed file descriptors are a call of their
// own in the library; they belong to the process figures all the same.
func writeLibrary(b *bytes.Buffer) {
	var raw bytes.Buffer
	vm.WriteProcessMetrics(&raw)
	vm.WriteFDMetrics(&raw)
	annotate(b, raw.String())
}

// annotate copies the samples of the library's exposition, with one HELP line
// in front of the first sample of each name; whatever metadata the library
// wrote is dropped, so a name is never announced twice. There is no TYPE line
// on purpose: the
// library's own types (a counter named go_cgo_calls_count, a gauge named
// process_io_read_bytes_total) would trip a linter that checks names against
// types, and an untyped family is read as a gauge.
func annotate(b *bytes.Buffer, text string) {
	seen := map[string]bool{}
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if n := strings.IndexAny(line, "{ "); n >= 0 {
			name = line[:n]
		}
		if !seen[name] {
			seen[name] = true
			family{name: name, help: libraryHelp(name)}.writeHeader(b)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func libraryHelp(name string) string {
	switch {
	case strings.HasPrefix(name, "go_"):
		return "Go runtime metric written by github.com/VictoriaMetrics/metrics."
	case strings.HasPrefix(name, "process_"):
		return "Process metric written by github.com/VictoriaMetrics/metrics."
	}
	return "Metric written by github.com/VictoriaMetrics/metrics."
}

func gauge(name, help string) family { return family{name: name, typ: "gauge", help: help} }

var (
	buildInfo = gauge("coddy_build_info",
		"Build of the running binary; the value is always 1.")
	subsystemUp = gauge("coddy_subsystem_up",
		"1 for a subsystem the process runs or is starting, 0 for one that is enabled and not running; the exporter itself is left out.")
	restartRequired = gauge("coddy_restart_required",
		"1 when a saved change only a fresh process can apply (a moved listen address, a surface that needs a session store) waits for a restart that nothing will perform on its own.")
	turnsRunning = gauge("coddy_turns_running",
		"Sessions with a turn running in this process. The turn families are read independently, so started minus ended need not equal it.")
	turnOldest = gauge("coddy_turn_oldest_running_seconds",
		"Age of the longest-running turn, 0 when none runs. It does not prove a turn is stuck: waits for a permission or a usage limit are legitimate.")
	backgroundTasks = gauge("coddy_background_tasks",
		"Background tasks of the model that are queued or running.")
	diskAvailable = gauge("coddy_sessions_disk_available_bytes",
		"Free space a non-root writer can use on the disk holding the session store.")
	diskSize = gauge("coddy_sessions_disk_size_bytes",
		"Size of the disk holding the session store.")
	promptsPending = gauge("coddy_prompts_pending",
		"Prompts that wait for a person to answer, by kind: a permission prompt or a question.")
	quotaUsed = gauge("coddy_provider_quota_used_ratio",
		"Share of a provider account's usage window used, from 0 to 1, as the session manager's cache last read it; a scrape asks the provider nothing.")
	quotaReset = gauge("coddy_provider_quota_reset_timestamp_seconds",
		"When a provider account's usage window starts over, as a Unix time.")
	swarmJoins = gauge("coddy_swarm_joins",
		"Registrations of this process into parent relays, by relay host, transport and the state of the last registration attempt.")
	swarmNodes = gauge("coddy_swarm_nodes",
		"Nodes the relay of this process knows, by kind, transport and state.")
)

// quotaWindow is the closed set of usage window identifiers the provider
// usage readers produce; anything else (a limit a provider names freely) is
// not exported.
var quotaWindow = regexp.MustCompile(`^(session|day|week|acu|window-[0-9]+)(-secondary)?$`)

// The kinds and statuses coddy_background_tasks always lists, so a quiet pool
// is a row of zeros rather than missing series.
var (
	taskKinds    = []bgtask.Kind{bgtask.KindCommand, bgtask.KindAgent, bgtask.KindServer}
	taskStatuses = []bgtask.Status{bgtask.StatusQueued, bgtask.StatusRunning}
)

// writeState writes what the process says about itself, from one
// serve.Snapshot taken for the scrape. It never leaves the process: no
// provider, bot API or MCP server is asked anything. admitted says which
// provider names hold a label of their own.
func writeState(b *bytes.Buffer, snap serve.Snapshot, admitted func(provider string) bool) {
	var rows bytes.Buffer
	flush := func(f family) {
		f.writeRows(b, &rows)
		rows.Reset()
	}

	bi := snap.Build
	vm.WriteGaugeUint64(&rows, series(buildInfo.name,
		"version", bi.Version, "revision", bi.Revision, "goversion", bi.GoVersion,
		"goos", bi.GOOS, "goarch", bi.GOARCH, "tags", strings.Join(bi.Tags, ",")), 1)
	flush(buildInfo)

	for _, sub := range snap.Subsystems {
		if sub.Kind == serve.KindMetrics {
			continue
		}
		up := 1.0
		if sub.State == serve.StateStopped {
			up = 0
		}
		vm.WriteGaugeFloat64(&rows, series(subsystemUp.name, "subsystem", string(sub.Kind)), up)
	}
	flush(subsystemUp)

	vm.WriteGaugeFloat64(&rows, restartRequired.name, boolValue(snap.RestartRequired))
	flush(restartRequired)
	vm.WriteGaugeFloat64(&rows, turnsRunning.name, float64(snap.TurnsRunning))
	flush(turnsRunning)
	oldest := 0.0
	if !snap.OldestTurnStartedAt.IsZero() {
		oldest = max(0, time.Since(snap.OldestTurnStartedAt).Seconds())
	}
	vm.WriteGaugeFloat64(&rows, turnOldest.name, oldest)
	flush(turnOldest)

	for _, kind := range taskKinds {
		for _, st := range taskStatuses {
			vm.WriteGaugeFloat64(&rows, series(backgroundTasks.name, "kind", string(kind), "status", string(st)),
				float64(snap.Tasks[string(kind)][string(st)]))
		}
	}
	flush(backgroundTasks)

	if snap.Disk != nil {
		vm.WriteGaugeUint64(&rows, diskAvailable.name, snap.Disk.AvailableBytes)
		flush(diskAvailable)
		vm.WriteGaugeUint64(&rows, diskSize.name, snap.Disk.SizeBytes)
		flush(diskSize)
	}

	vm.WriteGaugeUint64(&rows, series(promptsPending.name, "kind", "permission"), uint64(snap.PermissionPrompts))
	vm.WriteGaugeUint64(&rows, series(promptsPending.name, "kind", "question"), uint64(snap.Questions))
	flush(promptsPending)

	var resets bytes.Buffer
	for _, q := range snap.ProviderQuotas {
		if !admitted(q.Provider) || !quotaWindow.MatchString(q.Window) {
			continue
		}
		vm.WriteGaugeFloat64(&rows, series(quotaUsed.name, "provider", q.Provider, "window", q.Window), q.UsedRatio)
		if !q.ResetsAt.IsZero() {
			vm.WriteGaugeUint64(&resets, series(quotaReset.name, "provider", q.Provider, "window", q.Window), uint64(q.ResetsAt.Unix()))
		}
	}
	flush(quotaUsed)
	quotaReset.writeRows(b, &resets)

	writeJoins(&rows, snap.SwarmJoins)
	flush(swarmJoins)
	for _, n := range snap.RelayNodes {
		vm.WriteGaugeUint64(&rows, series(swarmNodes.name, "kind", n.Kind, "transport", n.Transport, "state", n.State), uint64(n.Count))
	}
	flush(swarmNodes)
}

// writeJoins counts the registrations by relay host and transport, both
// states written for every pair, so two registrations into one relay (under
// two names) add up instead of writing one series twice.
func writeJoins(rows *bytes.Buffer, joins []swarm.JoinStatus) {
	type key struct{ relay, transport string }
	counts := map[key][2]uint64{}
	for _, j := range joins {
		k := key{j.Relay, j.Transport}
		c := counts[k]
		if j.Online {
			c[0]++
		} else {
			c[1]++
		}
		counts[k] = c
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].relay != keys[j].relay {
			return keys[i].relay < keys[j].relay
		}
		return keys[i].transport < keys[j].transport
	})
	for _, k := range keys {
		c := counts[k]
		vm.WriteGaugeUint64(rows, series(swarmJoins.name, "relay", k.relay, "transport", k.transport, "state", serve.NodeOnline), c[0])
		vm.WriteGaugeUint64(rows, series(swarmJoins.name, "relay", k.relay, "transport", k.transport, "state", serve.NodeOffline), c[1])
	}
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
