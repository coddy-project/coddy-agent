package config

import "strings"

// Sessions is the YAML sessions section (key sessions).
type Sessions struct {
	// Dir is the filesystem root for persisted session bundles. Empty means
	// <Paths.Home>/sessions or ~/.coddy/sessions when Home is unset.
	Dir string `yaml:"dir"`
}

// Validate trims Dir in place.
func (s *Sessions) Validate() error {
	s.Dir = strings.TrimSpace(s.Dir)
	return nil
}
