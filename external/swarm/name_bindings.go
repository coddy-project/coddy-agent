//go:build swarm

package swarm

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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
		return
	}
	var stored map[string]string
	if json.Unmarshal(data, &stored) == nil {
		for name, digest := range stored {
			r.bindings[name] = digest
		}
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

// saveBindingsLocked writes the bindings through a temporary file, readable
// by the owner only. A failed write keeps them in memory for this run.
func (r *Registry) saveBindingsLocked() {
	if r.bindingsFile == "" {
		return
	}
	data, err := json.Marshal(r.bindings)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.bindingsFile), filepath.Base(r.bindingsFile)+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), r.bindingsFile) != nil {
		_ = os.Remove(tmp.Name())
	}
}
