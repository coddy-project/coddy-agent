// Package version provides the application version string.
// The Version variable is injected at build time via -ldflags (see Makefile print-version target):
//
//	go build -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(make -s print-version)"
//
//	Otherwise run `make build` from the repository. Without -X the value stays "dev".
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
