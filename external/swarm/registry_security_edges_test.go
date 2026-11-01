//go:build swarm

package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBindingsRejectNoncanonicalDigest(t *testing.T) {
	secret := "owner-secret"
	digest := secretDigest(secret)
	for name, value := range map[string]string{"uppercase": strings.ToUpper(digest), "mixed": strings.ToUpper(digest[:8]) + digest[8:]} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			valid := filepath.Join(dir, "valid")
			invalid := filepath.Join(dir, "invalid")
			if err := os.WriteFile(valid, []byte(fmt.Sprintf(`{"node":%q}`, digest)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(invalid, []byte(fmt.Sprintf(`{"node":%q}`, value)), 0600); err != nil {
				t.Fatal(err)
			}
			r, _ := newTestRegistry(t, time.Minute)
			if err := r.SetBindingsFile(valid); err != nil {
				t.Fatal(err)
			}
			if err := r.SetBindingsFile(invalid); err == nil {
				t.Fatal("noncanonical digest accepted")
			}
			req := tunnelRequest("node")
			req.LeaseSecret = secret
			if _, err := r.Register(req); err != nil {
				t.Fatalf("valid binding changed: %v", err)
			}
		})
	}
}

func TestDirectRenewedAsTunnelWaitsForConnection(t *testing.T) {
	r, clock := newTestRegistry(t, time.Minute)
	res, err := r.Register(directRequest("node"))
	if err != nil {
		t.Fatal(err)
	}
	old := r.nodes["node"].transport
	req := tunnelRequest("node")
	req.LeaseSecret = res.LeaseSecret
	if _, err := r.Register(req); err != nil {
		t.Fatal(err)
	}
	l := r.nodes["node"]
	if l.transport != nil {
		t.Fatal("old direct transport retained")
	}
	if l.expiresAt != clock.Now().Add(time.Minute) {
		t.Fatalf("pending expiry %v", l.expiresAt)
	}
	node, ok := r.Node("node")
	if !ok || node.Info.Online || node.Transport != nil {
		t.Fatalf("pending tunnel: %+v", node)
	}
	if old == nil {
		t.Fatal("missing initial direct transport")
	}
	tun := &fakeTunnel{alive: true}
	if err := r.AttachTransport("node", res.LeaseSecret, tun); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(req); err != nil {
		t.Fatal(err)
	}
	node, ok = r.Node("node")
	if !ok || !node.Info.Online || node.Transport != tun || !r.nodes["node"].expiresAt.IsZero() {
		t.Fatalf("live tunnel not retained: %+v", node)
	}
}
