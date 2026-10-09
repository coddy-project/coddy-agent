package ssh

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyCallback returns an ssh.HostKeyCallback for the given mode and known_hosts file path.
//
//   - "insecure"   – no host key verification (tools.permission_mode = bypass).
//   - anything else – new hosts are added; changed keys are replaced (TOFU, default).
func hostKeyCallback(mode, knownHostsPath string) (gossh.HostKeyCallback, error) {
	if mode == "insecure" {
		// The operator's explicit choice (tools.permission_mode bypass); every
		// other mode verifies through known_hosts below.
		return gossh.InsecureIgnoreHostKey(), nil //nolint:gosec // nosemgrep: go.lang.security.audit.crypto.insecure_ssh.avoid-ssh-insecure-ignore-host-key
	}
	return autoUpdateCallback(knownHostsPath)
}

// autoUpdateCallback builds a host key callback that automatically trusts new hosts
// and replaces changed keys in known_hosts (TOFU + auto-rotate).
// The known_hosts file is created on first connect if it does not exist.
func autoUpdateCallback(knownHostsPath string) (gossh.HostKeyCallback, error) {
	// Load existing entries (best-effort; no file is fine).
	var strictCB gossh.HostKeyCallback
	if knownHostsPath != "" {
		if cb, err := knownhosts.New(knownHostsPath); err == nil {
			strictCB = cb
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("ssh: load known_hosts: %w", err)
		}
	}

	return func(hostname string, remote net.Addr, key gossh.PublicKey) error {
		if strictCB != nil {
			err := strictCB(hostname, remote, key)
			if err == nil {
				return nil // key matches — all good
			}
			var keyErr *knownhosts.KeyError
			if errors.As(err, &keyErr) && len(keyErr.Want) > 0 {
				// Key changed: remove stale entry and fall through to add the new one.
				if rerr := removeKnownHost(knownHostsPath, hostname); rerr != nil {
					return fmt.Errorf("ssh: update known_hosts for %s: %w", hostname, rerr)
				}
				// strictCB is now stale; allow the append below to record the new key.
			}
			// Want is empty → host not yet known; fall through to add it.
		}
		return appendKnownHost(knownHostsPath, hostname, remote, key)
	}, nil
}

// removeKnownHost removes all known_hosts lines whose host field matches hostname
// (after normalisation). Lines for other hosts are kept unchanged.
func removeKnownHost(knownHostsPath, hostname string) error {
	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	normalized := knownhosts.Normalize(hostname)
	var kept bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if shouldDropLine(line, normalized) {
			continue
		}
		kept.WriteString(line)
		kept.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return os.WriteFile(knownHostsPath, kept.Bytes(), 0600)
}

// shouldDropLine reports whether a known_hosts line belongs to normalizedHost.
func shouldDropLine(line, normalizedHost string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	// First field is comma-separated list of hostnames (or a @marker/@cert-authority prefix).
	fields := strings.Fields(trimmed)
	if len(fields) < 3 {
		return false
	}
	hosts := strings.Split(fields[0], ",")
	for _, h := range hosts {
		if h == normalizedHost {
			return true
		}
	}
	return false
}

// appendKnownHost appends a new entry to the known_hosts file.
func appendKnownHost(knownHostsPath, hostname string, remote net.Addr, key gossh.PublicKey) error {
	if knownHostsPath == "" {
		return nil // no file configured, silently allow
	}
	f, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("ssh: write known_hosts: %w", err)
	}
	defer func() { _ = f.Close() }()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	if _, err := fmt.Fprintln(f, line); err != nil {
		return fmt.Errorf("ssh: write known_hosts entry: %w", err)
	}
	return nil
}
