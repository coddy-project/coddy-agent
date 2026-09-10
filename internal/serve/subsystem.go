// Package serve owns what `coddy serve` is: one process that runs every
// subsystem the configuration enables, over one session manager.
//
// A surface used to be a subcommand and therefore a process of its own, which
// meant the messenger gateway and the HTTP API could only look at the same
// sessions through the filesystem and never at the same time. Here they are
// goroutines under one supervisor instead: a Telegram conversation is a live
// session in the web UI, a swarm node can serve its own API while relaying for
// others, and turning a surface on is a line of YAML rather than a second unit
// file.
//
// This package knows nothing about the surfaces themselves. Each one is
// described by a Subsystem the CLI hands over, so a binary built without the
// build tag that carries a surface still compiles, still parses its
// configuration, and still says exactly which tag is missing.
package serve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Kind names one long-running surface, for logs and for the restart bookkeeping.
type Kind string

// The surfaces `coddy serve` knows how to run.
const (
	KindHTTP      Kind = "httpserver"
	KindGateway   Kind = "gateway"
	KindSwarm     Kind = "swarm"
	KindScheduler Kind = "scheduler"
)

// Subsystem describes one surface: whether the configuration asks for it,
// whether this binary can run it at all, and how to run it.
type Subsystem struct {
	// Kind names the surface.
	Kind Kind
	// ConfigKey is the YAML key that turns this surface on, quoted back at the
	// operator when something is wrong with it.
	ConfigKey string
	// BuildTag is the go build tag that carries the surface. It is the whole
	// content of the error an operator gets when they enable a surface their
	// binary was not built with.
	BuildTag string
	// Available is false when this binary was built without BuildTag. The
	// descriptor still exists so the configuration can be read and refused
	// honestly instead of silently doing nothing.
	Available bool
	// Enabled reports whether cfg asks for this surface.
	Enabled func(*config.Config) bool
	// Fingerprint returns the part of the configuration the surface is built
	// from. When it changes under a reload the supervisor restarts the surface.
	// A nil Fingerprint means the surface cannot be rebuilt in place - it owns a
	// listener the caller may be talking through.
	Fingerprint func(*config.Config) string
	// RestartKey returns the part of the configuration that can only be honoured
	// by a fresh process: the address a listener is bound to. A surface cannot
	// move its own listener out from under the caller who is talking through it,
	// so when this changes the whole process asks to be replaced instead. A nil
	// RestartKey means nothing about this surface needs that.
	RestartKey func(*config.Config) string
	// Run blocks until ctx is cancelled or the surface fails.
	Run func(ctx context.Context) error
}

// enabled answers the descriptor's own question, tolerating a missing func.
func (s Subsystem) enabled(cfg *config.Config) bool {
	return s.Enabled != nil && s.Enabled(cfg)
}

// fingerprint reads the rebuild key, or "" when the surface has none or there
// is no configuration to read it from.
func (s Subsystem) fingerprint(cfg *config.Config) string {
	if s.Fingerprint == nil || cfg == nil {
		return ""
	}
	return s.Fingerprint(cfg)
}

// restartKey reads the process-restart key, or "" when the surface has none or
// there is no configuration to read it from.
func (s Subsystem) restartKey(cfg *config.Config) string {
	if s.RestartKey == nil || cfg == nil {
		return ""
	}
	return s.RestartKey(cfg)
}

// ErrNothingEnabled is returned when the configuration asks for no surface at
// all. Running an agent daemon that serves nobody is never what was meant.
type ErrNothingEnabled struct {
	// Keys are the configuration keys that would have started something in
	// this binary. A key whose build tag is missing is left out: offering it
	// would only lead to the refusal ErrNotBuilt already explains.
	Keys []string
}

func (e *ErrNothingEnabled) Error() string {
	if len(e.Keys) == 0 {
		return "no subsystem is enabled, and this binary was built with none of them (rebuild with -tags \"http ui scheduler memory cli gateway swarm\")"
	}
	return "no subsystem is enabled: set one of " + strings.Join(e.Keys, ", ") + " to true in config.yaml"
}

// ErrNotBuilt is returned when the configuration enables a surface this binary
// cannot run.
type ErrNotBuilt struct {
	Kind      Kind
	ConfigKey string
	BuildTag  string
}

func (e *ErrNotBuilt) Error() string {
	return fmt.Sprintf("%s is true but this binary has no %s support (rebuild with -tags %s)",
		e.ConfigKey, e.Kind, e.BuildTag)
}

// Resolve returns the subsystems cfg asks for, in the order they were declared.
//
// A surface that is enabled but missing from the build is an error rather than
// a warning: an operator who wrote `enable: true` and got a log line they never
// read has a bot that is silently offline, which is the failure this whole
// command exists to remove.
func Resolve(cfg *config.Config, all []Subsystem) ([]Subsystem, error) {
	var out []Subsystem
	keys := make([]string, 0, len(all))
	for _, sub := range all {
		if sub.Available {
			keys = append(keys, sub.ConfigKey)
		}
		if !sub.enabled(cfg) {
			continue
		}
		if !sub.Available {
			return nil, &ErrNotBuilt{Kind: sub.Kind, ConfigKey: sub.ConfigKey, BuildTag: sub.BuildTag}
		}
		out = append(out, sub)
	}
	if len(out) == 0 {
		sort.Strings(keys)
		return nil, &ErrNothingEnabled{Keys: keys}
	}
	return out, nil
}
