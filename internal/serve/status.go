package serve

import (
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// Subsystem states, as the supervisor reads them against the live configuration.
const (
	// StateRunning is a subsystem the configuration enables and the process runs.
	StateRunning = "running"
	// StateStarting is one the configuration enables and the supervisor has not
	// applied the configuration for yet.
	StateStarting = "starting"
	// StateStopped is one the configuration enables, the supervisor applied, and
	// the process does not run (this binary cannot run it).
	StateStopped = "stopped"
	// StateRetained is one the configuration disables and the process still
	// runs: the last surface is kept, and any other is about to stop.
	StateRetained = "retained"
)

// StartedBy is how a process came to run, as Snapshot.StartedBy says it.
type StartedBy string

// The ways a coddy serve process is started.
const (
	// StartedBySystemd is a foreground coddy serve under the systemd user unit.
	StartedBySystemd StartedBy = "systemd"
	// StartedByDaemon is the worker of coddy serve --daemon.
	StartedByDaemon StartedBy = "daemon"
	// StartedByForeground is any other start: a terminal, a container, a script.
	StartedByForeground StartedBy = "foreground"
)

// Credential is what a request to the metrics exporter has to carry, as
// SubsystemStatus.Credential says it. The token itself is never part of a
// snapshot.
type Credential string

const (
	// CredentialToken is an exporter that answers only a request with the token.
	CredentialToken Credential = "token"
	// CredentialNone is an exporter that answers any request that reaches it.
	CredentialNone Credential = "none"
)

// UnavailableDisk names the disk in Snapshot.Unavailable.
const UnavailableDisk = "disk"

// Snapshot is what one coddy serve process reads off itself at one moment.
// Every part is read on its own, so two parts can disagree by what happened
// between the reads.
type Snapshot struct {
	Build Build
	// StartedAt is when the runtime was initialised, StartedBy how the process
	// was started and ConfigFile the path of the configuration file it loaded;
	// all three are read once, by Init.
	StartedAt  time.Time
	StartedBy  StartedBy
	ConfigFile string
	// Subsystems are the rows of the subsystem table, in declaration order.
	Subsystems []SubsystemStatus
	// RestartRequired is true when a saved change waits for a fresh process
	// and nothing will start one by itself: a listener's address moved, or a
	// surface that runs turns was enabled in a process started without a
	// session store.
	RestartRequired bool
	// RestartRefused is why a dispatcher would not restart into the saved
	// configuration: the message of the pre-flight that rejects it, keys and
	// build tags and never values. Empty otherwise.
	RestartRefused string
	// TurnsRunning counts the sessions with a turn running in this process.
	TurnsRunning int
	// OldestTurnStartedAt is when the longest-running of them began, zero when
	// none runs.
	OldestTurnStartedAt time.Time
	// Tasks counts the unfinished background tasks of the model, by kind and then
	// status. The runtime's own system tasks are not counted.
	Tasks map[string]map[string]int
	// Disk is the disk of the session store, nil when it was not read: no store
	// in this process, a platform without the call, or a failed read.
	Disk *Disk
	// Unavailable names the parts that exist in this process and could not be
	// read now (UnavailableDisk); a part with no source is not listed.
	Unavailable []string
	// PermissionPrompts and Questions count the prompts of the process that
	// wait for a person to answer.
	PermissionPrompts, Questions int
	// ProviderQuotas are the usage windows of the provider accounts, read from
	// the session manager's cache; nothing is asked of a provider.
	ProviderQuotas []ProviderQuota
	// SwarmJoins are the registrations of this process into parent relays.
	SwarmJoins []swarm.JoinStatus
	// RelayNodes counts the nodes the relay of this process knows, every
	// combination of kind, transport and state; nil when it runs no relay.
	RelayNodes []RelayNodeCount
}

// ProviderQuota is one usage window of a provider account.
type ProviderQuota struct {
	// Provider is the provider row, Window the window's identifier (session,
	// day, week and the like, as the provider's usage reader names it).
	Provider, Window string
	// UsedRatio is the share of the window used, from 0 to 1.
	UsedRatio float64
	// ResetsAt is when the window starts over, zero when the provider names no
	// moment.
	ResetsAt time.Time
}

// RelayNodeCount is how many nodes of one kind, transport and state the relay
// of this process knows.
type RelayNodeCount struct {
	Kind, Transport, State string
	Count                  int
}

// The states of a relay's node: its lease or tunnel is alive, or it is still
// listed while its lease runs out.
const (
	NodeOnline  = "online"
	NodeOffline = "offline"
)

// Build is what the binary says it is.
type Build struct {
	Version   string `json:"version"`
	Revision  string `json:"revision,omitempty"` // version.Revision, else vcs.revision; empty when the binary carries neither
	GoVersion string `json:"goVersion"`
	GOOS      string `json:"os"`
	GOARCH    string `json:"arch"`
	// Tags are the build tags of the binary.
	Tags []string `json:"tags,omitempty"`
}

// SubsystemStatus is one row of the process's subsystem table.
type SubsystemStatus struct {
	Kind Kind `json:"kind"`
	// State is one of the State constants.
	State string `json:"state"`
	// Address is the listener address the running instance was started on,
	// empty for a surface without one and for one that is not running.
	Address string `json:"address,omitempty"`
	// PendingAddress is the address the live configuration moved a running
	// listener to, empty when it did not; it takes a fresh process.
	PendingAddress string `json:"pendingAddress,omitempty"`
	// Credential says what a request to the metrics exporter has to carry. It
	// is set on that row only.
	Credential Credential `json:"credential,omitempty"`
}

// Disk is the room on one disk: what a process without privileges can still
// write there, and the disk's size. Both figures are always sent: no room left
// is a figure of 0.
type Disk struct {
	AvailableBytes uint64 `json:"availableBytes"`
	SizeBytes      uint64 `json:"sizeBytes"`
}

var (
	buildOnce sync.Once
	build     Build
)

// CurrentBuild describes the running binary. It is read once.
func CurrentBuild() Build {
	buildOnce.Do(func() {
		info, _ := debug.ReadBuildInfo()
		build = buildOf(info)
	})
	return build
}

func buildOf(info *debug.BuildInfo) Build {
	b := Build{
		Version:   version.Get(),
		Revision:  version.Revision,
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
	if info == nil {
		return b
	}
	for _, setting := range info.Settings {
		switch {
		case setting.Key == "vcs.revision" && b.Revision == "":
			b.Revision = setting.Value
		case setting.Key == "-tags" && setting.Value != "":
			b.Tags = strings.Split(setting.Value, ",")
		}
	}
	return b
}

// SetSupervisor names the supervisor whose reading of the live configuration
// Status reports. The supervisor needs the runtime to be built and the runtime
// needs the supervisor to answer for it, so the link is made after both exist.
func (r *Runtime) SetSupervisor(sup *Supervisor) { r.sup.Store(sup) }

// startedByRole says how a process with this role came to run: under the
// systemd unit, as the worker of a daemon, or in the foreground, which is
// every other role, the dispatcher's included.
func startedByRole(role string) StartedBy {
	switch role {
	case RoleService:
		return StartedBySystemd
	case RoleWorker:
		return StartedByDaemon
	}
	return StartedByForeground
}

// Status reads what this process knows about itself, part by part and without
// writing anything: the binary, how the process was started, the supervisor's
// reading of the live configuration (when SetSupervisor named one), the turns
// and tasks in flight, the disk of the session store, the prompts that wait
// for a person, the cached usage of the provider accounts and the swarm state
// the process published. A part the process has no source for is left out,
// and a disk that has a source and could not be read is named in Unavailable.
func (r *Runtime) Status() Snapshot {
	snap := Snapshot{
		Build:     CurrentBuild(),
		StartedAt: r.process.StartedAt, StartedBy: r.process.StartedBy, ConfigFile: r.process.ConfigFile,
		Tasks: make(map[string]map[string]int),
	}
	if sup := r.sup.Load(); sup != nil {
		if live := r.Cfg(); live != nil {
			snap.Subsystems, snap.RestartRequired, snap.RestartRefused = sup.Status(live)
			for i := range snap.Subsystems {
				if snap.Subsystems[i].Kind == KindMetrics {
					snap.Subsystems[i].Credential = CredentialNone
					if live.Metrics.HasToken() {
						snap.Subsystems[i].Credential = CredentialToken
					}
				}
			}
		}
	}
	if r.Mgr != nil {
		snap.TurnsRunning = len(r.Mgr.ActiveTurnSessionIDs())
		snap.OldestTurnStartedAt, _ = r.Mgr.OldestTurnStartedAt()
		snap.ProviderQuotas = providerQuotas(r.Mgr.CachedProviderUsage())
	}
	snap.PermissionPrompts, snap.Questions = agent.PendingPrompts()
	snap.SwarmJoins = swarm.Joins()
	if nodes, ok := swarm.RelayNodes(); ok {
		snap.RelayNodes = relayNodeCounts(nodes)
	}
	for kind, byStatus := range bgtask.Default().CountUnfinished() {
		counts := make(map[string]int, len(byStatus))
		for st, n := range byStatus {
			counts[string(st)] = n
		}
		snap.Tasks[string(kind)] = counts
	}
	if r.Store != nil {
		read := r.diskFree
		if read == nil {
			read = platform.DiskFree
		}
		if available, size, err := read(r.Store.Root); err == nil {
			snap.Disk = &Disk{AvailableBytes: available, SizeBytes: size}
		} else {
			// A failed read and a platform without the call both leave the
			// disk unknown, which is not the same as full.
			snap.Unavailable = append(snap.Unavailable, UnavailableDisk)
		}
	}
	return snap
}

// providerQuotas turns the cached usage into one row per provider and window,
// in a fixed order. A snapshot that is stale, failed, unsupported or switched
// off says nothing current, and a window without an identifier names nothing,
// so they are left out.
func providerQuotas(usage map[string]acp.ProviderUsageUpdate) []ProviderQuota {
	var out []ProviderQuota
	for provider, u := range usage {
		if u.Stale || u.Error != "" || u.Unsupported || u.Disabled {
			continue
		}
		for _, w := range u.Windows {
			if strings.TrimSpace(w.ID) == "" {
				continue
			}
			q := ProviderQuota{Provider: provider, Window: w.ID, UsedRatio: min(max(w.UsedPercent, 0), 100) / 100}
			if at, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
				q.ResetsAt = at.UTC()
			}
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Window < out[j].Window
	})
	return out
}

// relayNodeCounts counts a relay's nodes by kind, transport and state, every
// combination of the closed sets present; a node outside them is not counted.
func relayNodeCounts(nodes []swarm.NodeInfo) []RelayNodeCount {
	kinds := []string{swarm.KindAgent, swarm.KindRelay}
	transports := []string{swarm.TransportDirect, swarm.TransportTunnel}
	out := make([]RelayNodeCount, 0, len(kinds)*len(transports)*2)
	index := map[[3]string]int{}
	for _, kind := range kinds {
		for _, transport := range transports {
			for _, state := range []string{NodeOnline, NodeOffline} {
				index[[3]string{kind, transport, state}] = len(out)
				out = append(out, RelayNodeCount{Kind: kind, Transport: transport, State: state})
			}
		}
	}
	for _, n := range nodes {
		state := NodeOffline
		if n.Online {
			state = NodeOnline
		}
		if i, ok := index[[3]string{n.Kind, n.Transport, state}]; ok {
			out[i].Count++
		}
	}
	return out
}
