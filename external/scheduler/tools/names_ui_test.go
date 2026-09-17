//go:build scheduler

package schedtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// The web UI names a tool call by the action it performs ("resuming a scheduled job")
// through a tool.name.<id> entry in each of its dictionaries, and falls back to the raw
// id when the entry is missing. coddy_scheduler_job_resume shipped without one and the
// transcript read "coddy_scheduler_job_resume". Every registered scheduler tool must be
// named in every dictionary.
func TestEverySchedulerToolIsNamedInTheWebUI(t *testing.T) {
	cfg := &config.Config{Scheduler: config.SchedulerConfig{Enabled: true}}
	var names []string
	RegisterTools(func(tool *tooling.Tool) { names = append(names, tool.Definition.Name) }, cfg)
	if len(names) == 0 {
		t.Fatal("no scheduler tools registered")
	}
	for _, locale := range []string{"en", "ru"} {
		path := filepath.Join("..", "..", "ui", "src", "ui", "i18n", "messages", locale+".ts")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		dict := string(raw)
		for _, name := range names {
			if !strings.Contains(dict, `"tool.name.`+name+`"`) {
				t.Errorf("%s: no tool.name.%s entry, the transcript would show the raw id", locale, name)
			}
		}
	}
}
