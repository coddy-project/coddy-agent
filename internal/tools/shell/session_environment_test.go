package shell

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestShellReceivesCallingSessionID(t *testing.T) {
	t.Setenv("CODDY_SESSION_ID", "inherited")
	t.Setenv("HOME", t.TempDir())
	sh := platform.CurrentShell()
	command := `printf '%s' "$CODDY_SESSION_ID"`
	switch sh.Kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		command = `Write-Output $env:CODDY_SESSION_ID`
	case platform.ShellCmd:
		command = `echo %CODDY_SESSION_ID%`
	}
	const id = "sess_calling_chat"
	t.Run("foreground", func(t *testing.T) {
		sc, err := startForegroundContext(context.Background(), command, &tooling.Env{SessionID: id}, sh)
		if err != nil {
			t.Fatal(err)
		}
		<-sc.waited
		if got := strings.TrimSpace(sc.completedResult()); got != id {
			t.Errorf("got %q, want %q", got, id)
		}
	})
	t.Run("background", func(t *testing.T) {
		var out bytes.Buffer
		h, err := (&bgtask.CommandRunner{Shell: sh}).Start(bgtask.Spec{Command: command, SessionID: id}, &out)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Wait(); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out.String()); got != id {
			t.Errorf("got %q, want %q", got, id)
		}
	})
}
