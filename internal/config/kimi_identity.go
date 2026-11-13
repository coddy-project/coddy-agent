package config

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

var kimiIdentityPattern = regexp.MustCompile(`^(?:[0-9a-f]{32}|[0-9a-f]{64})$`)

// ValidKimiIdentity reports whether id is a canonical random identity.
func ValidKimiIdentity(id string) bool { return kimiIdentityPattern.MatchString(id) }

// NewKimiIdentity creates a 128-bit provider identity.
func NewKimiIdentity() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

// KimiIdentityDir returns the managed credential directory or an empty path.
func KimiIdentityDir(home, id string) string {
	if strings.TrimSpace(home) == "" || !ValidKimiIdentity(id) {
		return ""
	}
	return filepath.Join(home, "providers", "kimi-identities", id)
}
