package configapi

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// One live configuration keeps one revision; a revision this process never issued -
// from before a restart, or made up - names nothing, and neither does one older than
// the configurations kept, so the save falls back to the live configuration.
func TestRevisions(t *testing.T) {
	served := NewRevisions()
	first := &config.Config{}
	rev := served.Revision(first)
	if again := served.Revision(first); again != rev {
		t.Fatalf("one configuration got two revisions: %q and %q", rev, again)
	}
	if got := served.Lookup(rev); got != first {
		t.Fatalf("lookup(%q) = %p, want the configuration served under it", rev, got)
	}
	if got := NewRevisions().Lookup(rev); got != nil {
		t.Fatalf("another process resolved %q", rev)
	}
	for _, unknown := range []string{"", "made-up", rev + "0"} {
		if got := served.Lookup(unknown); got != nil {
			t.Fatalf("lookup(%q) resolved to %p", unknown, got)
		}
	}
	for i := 0; i < RevisionsKept; i++ {
		served.Revision(&config.Config{})
	}
	if got := served.Lookup(rev); got != nil {
		t.Fatalf("a revision older than the %d kept still resolves", RevisionsKept)
	}
}
