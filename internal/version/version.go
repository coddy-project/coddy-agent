// Package version provides the application version string.
// The Version variable is injected at build time via -ldflags:
//
//	go build -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=v1.2.3"
package version

// Version is set at build time via -ldflags. Falls back to "dev".
var Version = "dev"

// Get returns the current version string.
func Get() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
