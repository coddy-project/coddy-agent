package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestActionCommandRowsHintTheCompactOptions(t *testing.T) {
	for _, row := range ActionCommandRows(&config.Config{}) {
		if row.Name == "compact" {
			if row.Hint != "[--model <id>] [instructions]" {
				t.Fatalf("hint = %q", row.Hint)
			}
			return
		}
	}
	t.Fatal("no compact row while compaction is enabled")
}
