package swarm

import (
	"strings"
	"testing"
)

func TestValidateNodeName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"plain", "nas02", true},
		{"digits and dashes", "gpu-03_b", true},
		{"single char", "a", true},
		{"empty", "", false},
		{"space", "nas 02", false},
		{"slash", "nas/02", false},
		{"encoded slash", "nas%2F02", false},
		{"dot segment", "..", false},
		{"leading dot", ".hidden", false},
		{"unicode", "узел", false},
		{"too long", strings.Repeat("a", 65), false},
		{"max length", strings.Repeat("a", 64), true},
		{"reserved nodes", "nodes", false},
		{"reserved info", "info", false},
		{"reserved register", "register", false},
		{"reserved tunnel", "tunnel", false},
		{"reserved sessions", "sessions", false},
		{"reserved topology", "topology", false},
		{"reserved is case-insensitive", "Nodes", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNodeName(tc.in)
			if tc.ok && err != nil {
				t.Fatalf("ValidateNodeName(%q) = %v, want nil", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ValidateNodeName(%q) = nil, want an error", tc.in)
			}
		})
	}
}

// A session reference addresses one session inside a swarm. Node names cannot
// contain a separator, so the last segment is always the session id and
// everything before it is the path of nodes leading to the owning agent. That
// rule is what keeps a multi-hop reference unambiguous.
func TestParseSessionRef(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		path    []string
		id      string
		wantErr bool
	}{
		{"bare id", "sess_abc", nil, "sess_abc", false},
		{"one hop", "nas02/sess_abc", []string{"nas02"}, "sess_abc", false},
		{"three hops", "relay2/relay3/agent7/sess_abc", []string{"relay2", "relay3", "agent7"}, "sess_abc", false},
		{"empty", "", nil, "", true},
		{"trailing separator", "nas02/", nil, "", true},
		{"leading separator", "/sess_abc", nil, "", true},
		{"empty middle segment", "a//sess", nil, "", true},
		{"invalid node name", "na s/sess_abc", nil, "", true},
		{"invalid session id", "nas02/sess abc", nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := ParseSessionRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSessionRef(%q) = %+v, want an error", tc.in, ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSessionRef(%q): %v", tc.in, err)
			}
			if ref.ID != tc.id {
				t.Fatalf("id = %q, want %q", ref.ID, tc.id)
			}
			if strings.Join(ref.NodePath, "/") != strings.Join(tc.path, "/") {
				t.Fatalf("node path = %v, want %v", ref.NodePath, tc.path)
			}
		})
	}
}

func TestSessionRefRoundTrip(t *testing.T) {
	for _, in := range []string{"sess_abc", "nas02/sess_abc", "r2/r3/agent7/sess_abc"} {
		ref, err := ParseSessionRef(in)
		if err != nil {
			t.Fatalf("ParseSessionRef(%q): %v", in, err)
		}
		if got := ref.String(); got != in {
			t.Fatalf("round trip: %q -> %q", in, got)
		}
	}
}

// The mount prefix is what a client prepends to reach one node through a relay.
// It has to survive a multi-hop path without collapsing the hops.
func TestSessionRefMountPrefix(t *testing.T) {
	ref, err := ParseSessionRef("relay2/agent7/sess_abc")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.MountPrefix(), "/swarm/nodes/relay2/swarm/nodes/agent7"; got != want {
		t.Fatalf("MountPrefix() = %q, want %q", got, want)
	}
	local, err := ParseSessionRef("sess_abc")
	if err != nil {
		t.Fatal(err)
	}
	if got := local.MountPrefix(); got != "" {
		t.Fatalf("a local session needs no mount prefix, got %q", got)
	}
}

func TestValidateAdvertiseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"http host port", "http://nas02:12345", true},
		{"https", "https://agent.example", true},
		{"with path", "http://gw.example/coddy-node", true},
		{"empty", "", false},
		{"no scheme", "nas02:12345", false},
		{"unsupported scheme", "ftp://nas02", false},
		{"query", "http://nas02:12345?x=1", false},
		{"fragment", "http://nas02:12345#f", false},
		{"credentials", "http://user:pw@nas02:12345", false},
		{"no host", "http://", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateAdvertiseURL(tc.in)
			if tc.ok && err != nil {
				t.Fatalf("ValidateAdvertiseURL(%q) = %v, want nil", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ValidateAdvertiseURL(%q) = nil, want an error", tc.in)
			}
		})
	}
}

// A registration document describes how the relay may reach the node. The two
// transports disagree about what must be present, so validation is what keeps
// a half-filled document from reaching the registry.
func TestRegisterRequestValidate(t *testing.T) {
	base := func() RegisterRequest {
		return RegisterRequest{
			Name:         "nas02",
			Kind:         KindAgent,
			Transport:    TransportDirect,
			AdvertiseURL: "http://nas02:12345",
			InstanceUUID: "11111111-1111-1111-1111-111111111111",
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("a complete direct registration should validate: %v", err)
	}

	tunnel := base()
	tunnel.Transport = TransportTunnel
	tunnel.AdvertiseURL = ""
	if err := tunnel.Validate(); err != nil {
		t.Fatalf("a tunnel registration needs no advertise url: %v", err)
	}

	bad := []struct {
		name   string
		mutate func(*RegisterRequest)
	}{
		{"no name", func(r *RegisterRequest) { r.Name = "" }},
		{"bad name", func(r *RegisterRequest) { r.Name = "nas/02" }},
		{"unknown kind", func(r *RegisterRequest) { r.Kind = "robot" }},
		{"unknown transport", func(r *RegisterRequest) { r.Transport = "carrier-pigeon" }},
		{"direct without url", func(r *RegisterRequest) { r.AdvertiseURL = "" }},
		{"direct with bad url", func(r *RegisterRequest) { r.AdvertiseURL = "ftp://nas02" }},
		{"no instance uuid", func(r *RegisterRequest) { r.InstanceUUID = "" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			req := base()
			tc.mutate(&req)
			if err := req.Validate(); err == nil {
				t.Fatalf("%+v should not validate", req)
			}
		})
	}
}
