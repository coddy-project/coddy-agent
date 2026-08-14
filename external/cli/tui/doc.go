//go:build cli

// Package tui is a terminal UI framework for the coddy interactive console:
// a component tree rendered inline into the terminal main buffer with
// line-diff updates, synchronized output, and per-line style resets.
//
// The rendering model, component contracts, and byte-level conventions
// (segment reset, APC cursor marker, editor borders, select-list prefixes,
// braille loader frames) are ported from the pi coding agent's TUI library
// (github.com/badlogic/pi-mono packages/tui, commit b1efcf7d7, MIT License,
// Copyright (c) 2025 Mario Zechner). This Go implementation is an
// independent rewrite of the documented behavior.
//
// Width semantics follow pi: tabs occupy three columns, regional indicators
// are always double width, and only CSI sequences ending in m/G/K/H/J plus
// OSC and APC blocks are invisible to width measurement. Application text
// must pass through SanitizeText before entering components, so foreign
// escape sequences never reach rendered lines.
package tui
