//go:build scheduler

// Package daemon is the cron loop of the scheduler and the runtime that starts,
// stops and keeps the runs: each run is a background task of kind agent under
// the job's session, driven by internal/agent.RunScheduledJob through the
// session manager, exactly what a spawn_agent call starts. Nothing here runs an
// agent turn on its own state.
package daemon
