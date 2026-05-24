package config

// Rules configures project rules discovery.
type Rules struct {
	AutoDiscover *bool    `yaml:"auto_discover"`
	Systems      []string `yaml:"systems"`
}

// ApplyDefaults sets rules defaults.
func (r *Rules) ApplyDefaults() {
	if r.AutoDiscover == nil {
		v := true
		r.AutoDiscover = &v
	}
}

// Validate accepts any layout from ApplyDefaults.
func (r *Rules) Validate() error {
	return nil
}

// AutoDiscoverEnabled reports whether CWD rule roots are scanned.
func (r *Rules) AutoDiscoverEnabled() bool {
	if r.AutoDiscover == nil {
		return true
	}
	return *r.AutoDiscover
}
