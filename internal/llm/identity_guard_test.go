package llm

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A row's TLS identity travels with its proxy: a ProviderInput built from a
// provider row and given its ProxyURL must be given its ClientTLS too, or a
// coddy row would present its certificate on the completion and not on the
// listing, the usage read or the context-window probe. The sites that copy
// ProxyURL from something that is not a provider row are named here.
var identityGuardExempt = map[string]string{
	"internal/llm/codex_auth.go": "the Codex catalog fetch: a codex row has no identity",
}

func TestEveryProviderInputThatCopiesTheProxyCopiesTheIdentity(t *testing.T) {
	root := filepath.Join("..", "..")
	var misses []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "build", "dist":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if _, ok := identityGuardExempt[rel]; ok || strings.HasPrefix(rel, "internal/llm/") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "ProxyURL:") || strings.Contains(line, "//") && strings.Index(line, "//") < strings.Index(line, "ProxyURL:") {
				continue
			}
			lo, hi := max(0, i-14), min(len(lines), i+14)
			if !strings.Contains(strings.Join(lines[lo:hi], "\n"), "ClientTLS:") {
				misses = append(misses, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(misses) > 0 {
		t.Fatalf("these ProviderInput literals copy ProxyURL and not ClientTLS:\n%s", strings.Join(misses, "\n"))
	}
}
