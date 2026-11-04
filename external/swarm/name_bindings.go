//go:build swarm

package swarm

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// A name a node registered stays bound to the lease secret it was given
// after the lease itself is gone: the registry lives in memory, so a relay
// restart, a rebuild on a settings save or a node offline past the grace
// period would otherwise free the name for whoever holds a pairing token and
// registers first. The binding keeps a digest of the secret, never the
// secret, and only DELETE /swarm/nodes/{node} or an operator-configured
// upstream of the same name lets it go.

// SetBindingsFile loads the persistent bindings; only a missing file is empty.
func (r *Registry) SetBindingsFile(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("swarm: read name bindings: %w", err)
	}
	if err == nil {
		if err = json.Unmarshal(data, &stored); err != nil || stored == nil {
			return fmt.Errorf("swarm: invalid name bindings file %q", path)
		}
		for name, digest := range stored {
			decoded, err := hex.DecodeString(digest)
			if name == "" || err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != digest {
				return fmt.Errorf("swarm: invalid name binding in %q", path)
			}
		}
	}
	r.bindingsFile, r.bindings = path, stored
	return nil
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

var ErrBindingsPersistence = errors.New("swarm: name bindings persistence failed")

func (r *Registry) bindLocked(name, secret string) error {
	if r.bindings == nil {
		return nil
	}
	digest := secretDigest(secret)
	old, had := r.bindings[name]
	if had && old == digest {
		return nil
	}
	r.bindings[name] = digest
	if err := r.saveBindingsLocked(); err != nil {
		if !errors.Is(err, platform.ErrWriteNotDurable) {
			if had {
				r.bindings[name] = old
			} else {
				delete(r.bindings, name)
			}
		}
		return err
	}
	return nil
}

func (r *Registry) unbindLocked(name string) error {
	old, ok := r.bindings[name]
	if !ok {
		return nil
	}
	delete(r.bindings, name)
	if err := r.saveBindingsLocked(); err != nil {
		if !errors.Is(err, platform.ErrWriteNotDurable) {
			r.bindings[name] = old
		}
		return err
	}
	return nil
}

func (r *Registry) saveBindingsLocked() error {
	if r.bindingsFile == "" {
		return nil
	}
	data, err := json.Marshal(r.bindings)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBindingsPersistence, err)
	}
	report, err := platform.WritePrivateFileReplacing(r.bindingsFile, data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBindingsPersistence, err)
	}
	if report.DirSyncErr != nil {
		r.bindingsDurabilityPending = true
		return fmt.Errorf("%w: %w: %v", ErrBindingsPersistence, platform.ErrWriteNotDurable, report.DirSyncErr)
	}
	r.bindingsDurabilityPending = false
	return nil
}

func (r *Registry) ensureBindingsDurableLocked() error {
	if r.bindingsDurabilityPending {
		return r.saveBindingsLocked()
	}
	return nil
}
