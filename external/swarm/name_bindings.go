//go:build swarm

package swarm

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// A name a node registered stays bound to the lease secret it was given
// after the lease itself is gone: the registry lives in memory, so a relay
// restart, a rebuild on a settings save or a node offline past the grace
// period would otherwise free the name for whoever holds a pairing token and
// registers first. The binding keeps a digest of the secret, never the
// secret, and only DELETE /swarm/nodes/{node} or an operator-configured
// upstream of the same name lets it go.

// SetBindingsFile names the file the registry keeps its name bindings in and
// reads what it holds. A missing or unreadable file is an empty set.
func (r *Registry) SetBindingsFile(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindingsFile = path
	r.bindings = map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Default().Warn("swarm: the name bindings cannot be read, every name is free until its node returns", "file", path, "error", err)
		}
		return
	}
	var stored map[string]string
	if err := json.Unmarshal(data, &stored); err != nil {
		slog.Default().Warn("swarm: the name bindings cannot be read, every name is free until its node returns", "file", path, "error", err)
		return
	}
	for name, digest := range stored {
		r.bindings[name] = digest
	}
}

func secretDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// boundSecretLocked reports whether name is bound and, if so, whether secret
// is the one it is bound to.
func (r *Registry) boundSecretLocked(name, secret string) (bound, matches bool) {
	digest, ok := r.bindings[name]
	if !ok {
		return false, false
	}
	if secret == "" {
		return true, false
	}
	return true, subtle.ConstantTimeCompare([]byte(digest), []byte(secretDigest(secret))) == 1
}

func (r *Registry) bindLocked(name, secret string) {
	if r.bindings == nil {
		return
	}
	digest := secretDigest(secret)
	if r.bindings[name] == digest {
		return
	}
	r.bindings[name] = digest
	r.saveBindingsLocked()
}

func (r *Registry) unbindLocked(name string) {
	if _, ok := r.bindings[name]; !ok {
		return
	}
	delete(r.bindings, name)
	r.saveBindingsLocked()
}

// unbind frees a name for an operator-configured upstream: the operator's
// configuration outranks a node that held the name before.
func (r *Registry) unbind(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unbindLocked(name)
}

// saveBindingsLocked writes the bindings, readable by the owner only. A failed
// write keeps them in memory for this run.
func (r *Registry) saveBindingsLocked() {
	if r.bindingsFile == "" {
		return
	}
	data, err := json.Marshal(r.bindings)
	if err == nil {
		err = config.AtomicWriteFile(r.bindingsFile, data, 0o600)
	}
	if err != nil {
		slog.Default().Warn("swarm: the name bindings were not saved and hold for this run only", "file", r.bindingsFile, "error", err)
	}
}
