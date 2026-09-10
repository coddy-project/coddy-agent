// Package swarm implements the stateless relay nodes register into.
//
// The relay itself is behind the swarm build tag. This file is not: it carries
// the options a caller fills in and the flag that says whether this binary can
// relay at all, so `coddy serve` can read a configuration that asks for a relay
// and answer honestly in a build that has none.
package swarm

import (
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TokenEnvVar is where the relay looks for its client credential when no flag
// carries one, so a token need not be written into config.yaml.
const TokenEnvVar = "CODDY_SWARM_TOKEN"

// PairingEnvVar is the registration credential's environment fallback.
const PairingEnvVar = "CODDY_SWARM_PAIRING_TOKEN"

// Options are what one relay instance is built from.
type Options struct {
	// Cfg is the configuration the relay starts with.
	Cfg *config.Config
	// Log is the process logger.
	Log *slog.Logger
	// Home is the agent state directory, where join credentials are kept.
	Home string
	// ListenAddr is the already-resolved host:port to bind.
	ListenAddr string
	// ExtraAuthTokens are client credentials supplied out of band
	// (--swarm-auth-token, CODDY_SWARM_TOKEN).
	ExtraAuthTokens []string
}
