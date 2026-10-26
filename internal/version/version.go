// Package version provides the application version string.
// The Version and Revision variables are injected at build time via -ldflags (see the Makefile targets print-version and print-revision):
//
//	go build -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(make -s print-version) -X github.com/EvilFreelancer/coddy-agent/internal/version.Revision=$(make -s print-revision)"
//
//	Otherwise run `make build` from the repository. Without -X Version stays "dev" and Revision empty.
package version

// Version is set at build time via -ldflags. Falls back to "dev".
var Version = "dev"

// Revision is the full commit the binary was built from, set at build time the
// same way as Version (see the Makefile). It is empty when the build did not
// pass it; the build info the Go toolchain stamps (vcs.revision) is the
// fallback then. The build names it because Go 1.26 and earlier recognise a
// checkout only by a .git directory, so with such a toolchain a worktree nested
// in another checkout would be stamped with the enclosing checkout's commit.
var Revision string

// Get returns the current version string.
func Get() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
