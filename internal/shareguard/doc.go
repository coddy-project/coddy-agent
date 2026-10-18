// Package shareguard holds the two small pieces a lender of shared models needs
// beside the stream slot: a calls-per-window limiter and in-memory outcome
// counters. It is untagged and imports nothing of Coddy, so the node's HTTP
// surface and the swarm relay share it without a build-tag edge.
package shareguard
