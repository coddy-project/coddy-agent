//go:build ui

package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The SPA's brand assets are symlinks into docs/assets, so the file the bundler
// opens lives outside the directory the image build copies into the Node stage.
// Docker carries a symlink over as a symlink, which leaves the link dangling
// unless the Dockerfile also stages its target at the same path - and a dangling
// import is not a build warning, it fails `npm run build:go` and the published
// image silently keeps whatever SPA it had. That is exactly how the sign-in
// wordmark stopped the image from building while the release binaries went out
// as usual, so the coupling gets a test rather than a note.
func TestDockerfileStagesEverySymlinkedAsset(t *testing.T) {
	copied := dockerfileAssetPatterns(t)

	assets := filepath.Join("src", "assets")
	entries, err := os.ReadDir(assets)
	if err != nil {
		t.Fatalf("read %s: %v", assets, err)
	}

	seen := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		seen++

		target, err := os.Readlink(filepath.Join(assets, entry.Name()))
		if err != nil {
			t.Fatalf("readlink %s: %v", entry.Name(), err)
		}
		// The link is resolved against src/assets, and the Dockerfile stages the
		// repository's docs/assets at /docs/assets - the very path four levels up
		// from src/assets inside the image.
		if want := filepath.Join("..", "..", "..", "..", "docs", "assets"); filepath.Dir(target) != want {
			t.Errorf("src/assets/%s points at %s; the image only stages %s/",
				entry.Name(), target, want)
			continue
		}
		if !matchesAny(copied, filepath.Base(target)) {
			t.Errorf("src/assets/%s resolves to docs/assets/%s, which no COPY in the Dockerfile stages: the image build fails on the import",
				entry.Name(), filepath.Base(target))
		}
	}

	if seen == 0 {
		t.Fatal("no symlinked assets found; this test no longer guards anything")
	}
}

// Base names, possibly globbed, that the Dockerfile copies out of docs/assets.
func dockerfileAssetPatterns(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	source := regexp.MustCompile(`(?m)^COPY\s+(.*)\s+/docs/assets/\s*$`)
	found := source.FindStringSubmatch(string(raw))
	if found == nil {
		t.Fatal("no COPY into /docs/assets/ in the Dockerfile; the symlinked assets have nowhere to land")
	}

	var patterns []string
	for _, field := range strings.Fields(found[1]) {
		patterns = append(patterns, filepath.Base(field))
	}
	return patterns
}

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, err := filepath.Match(pattern, name); err == nil && ok {
			return true
		}
	}
	return false
}
