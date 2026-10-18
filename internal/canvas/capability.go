package canvas

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// CapabilityTTL is how long a frame's address works. The web UI asks for a
// fresh one whenever it reads the board, and the address stops working as soon
// as the frame leaves the board, whatever its age.
const CapabilityTTL = 12 * time.Hour

// maxCapability bounds the token a request may present.
const maxCapability = 4096

const capabilityKind = "canvas"

// Claim is what a frame's address grants: the files of one frame's root, in
// one workspace, of one node of one .canvas file as one chat shows it, until it
// expires.
type Claim struct {
	Version int    `json:"v"`
	Kind    string `json:"k"`
	Session string `json:"s"`
	// File is the board's .canvas file, relative to the workspace.
	File      string `json:"c"`
	Frame     string `json:"f"`
	Workspace string `json:"w"`
	Root      string `json:"r"`
	Gen       int    `json:"g"`
	Expires   int64  `json:"e"`
}

// DeriveKey turns the server's signing secret into the canvas key, separated
// from every other use of the secret (the media capability of the workspace
// viewer derives its own), so a token of one kind never passes for the other.
func DeriveKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("coddy-canvas-frame\x00"))
	return mac.Sum(nil)
}

// SignCapability returns the token for a claim, valid for CapabilityTTL from
// now. It is one path segment of base64url characters and a dot.
func SignCapability(key []byte, c Claim, now time.Time) string {
	c.Version = 1
	c.Kind = capabilityKind
	c.Expires = now.Add(CapabilityTTL).Unix()
	payload, _ := json.Marshal(c)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCapability checks a token's signature, kind and lifetime and returns
// its claim. Whether the claim still names a frame on the board is the
// caller's check.
func VerifyCapability(key []byte, token string, now time.Time) (Claim, error) {
	bad := errors.New("invalid canvas address")
	if token == "" || len(token) > maxCapability {
		return Claim{}, bad
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Claim{}, bad
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claim{}, bad
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Claim{}, bad
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claim{}, bad
	}
	var c Claim
	if json.Unmarshal(payload, &c) != nil {
		return Claim{}, bad
	}
	unix := now.Unix()
	if c.Version != 1 || c.Kind != capabilityKind || c.Session == "" || c.File == "" || c.Frame == "" || c.Workspace == "" {
		return Claim{}, bad
	}
	if c.Expires <= unix || c.Expires > unix+int64(CapabilityTTL/time.Second) {
		return Claim{}, errors.New("the canvas address expired; reload the canvas")
	}
	return c, nil
}
