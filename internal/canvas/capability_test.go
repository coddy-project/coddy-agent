package canvas

import (
	"strings"
	"testing"
	"time"
)

func TestCapabilityRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_800_000_000, 0)
	claim := Claim{Session: "sess_a", Frame: "fr_1", Workspace: "ws-id", Root: "site", Gen: 3}
	token := SignCapability(key, claim, now)
	if strings.ContainsAny(token, "/%?#+= ") {
		t.Fatalf("token %q is not a single safe path segment", token)
	}
	got, err := VerifyCapability(key, token, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != "sess_a" || got.Frame != "fr_1" || got.Workspace != "ws-id" || got.Root != "site" || got.Gen != 3 {
		t.Fatalf("claim %+v", got)
	}
	if got.Expires != now.Add(CapabilityTTL).Unix() {
		t.Fatalf("expires %d, want %d", got.Expires, now.Add(CapabilityTTL).Unix())
	}
}

func TestCapabilityRefusals(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	other := []byte("fedcba9876543210fedcba9876543210")
	now := time.Unix(1_800_000_000, 0)
	token := SignCapability(key, Claim{Session: "sess_a", Frame: "fr_1", Workspace: "w", Root: ""}, now)
	parts := strings.SplitN(token, ".", 2)
	cases := map[string]struct {
		key   []byte
		token string
		at    time.Time
	}{
		"other key":       {other, token, now},
		"expired":         {key, token, now.Add(CapabilityTTL + time.Second)},
		"from the future": {key, SignCapability(key, Claim{Session: "s", Frame: "f", Workspace: "w"}, now.Add(48*time.Hour)), now},
		"tampered claim":  {key, "e30." + parts[1], now},
		"no signature":    {key, parts[0], now},
		"empty":           {key, "", now},
		"too long":        {key, strings.Repeat("a", 5000) + "." + parts[1], now},
		"three parts":     {key, token + ".x", now},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyCapability(tc.key, tc.token, tc.at); err == nil {
				t.Fatalf("%s verified", name)
			}
		})
	}
}

func TestCapabilityKeyIsNotTheMediaKey(t *testing.T) {
	// The HTTP server derives this key from the same secret as the media key;
	// the derivation is domain-separated so one token never passes for the other.
	if a, b := string(DeriveKey([]byte("secret"))), string(DeriveKey([]byte("secret"))); a != b {
		t.Fatal("key derivation is not deterministic")
	}
	if string(DeriveKey([]byte("secret"))) == "secret" {
		t.Fatal("the derived key is the secret itself")
	}
}
