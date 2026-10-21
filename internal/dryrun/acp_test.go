package dryrun

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
)

// TestHelperACPAgent turns the test binary into the scripted ACP agent when
// acptest.Command started it; the ordinary run skips it.
func TestHelperACPAgent(t *testing.T) {
	if !acptest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

// acpRowYAML is a providers block whose row "agent" runs the fake agent of
// script, with modes.plan set and modes.ask left out.
func acpRowYAML(t *testing.T, script acptest.Script) string {
	t.Helper()
	t.Setenv(llm.ACPDepthEnv, "")
	t.Cleanup(llm.StopACPAgents)
	cmd, args := acptest.Command(t, script)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return fmt.Sprintf(`providers:
  - name: agent
    type: acp
    acp:
      command: %q
      args: [%s]
      modes: {plan: read}
`, cmd, strings.Join(quoted, ", "))
}

// The probe of an acp row starts its agent and reads the models a session
// offers: the configured ids are checked against them, and the mode a
// session in ask mode would need is reported missing.
func TestACPRowIsProbedThroughItsAgent(t *testing.T) {
	body := acpRowYAML(t, acptest.Script{Models: []string{"small", "large"}, Modes: []string{"default", "read"}, Close: true}) + `models:
  - model: agent/default
  - model: agent/large
  - model: agent/huge
agent:
  model: agent/default
`
	rep := run(t, body, nil)
	if p := find(t, rep, "providers[agent]"); p.Status != StatusOK || !strings.Contains(p.Message, "lists 3 models") {
		t.Errorf("provider check %+v", p)
	}
	for _, id := range []string{"agent/default", "agent/large"} {
		if m := find(t, rep, "models["+id+"]"); m.Status != StatusOK {
			t.Errorf("listed model %s: %+v", id, m)
		}
	}
	if m := find(t, rep, "models[agent/huge]"); m.Status != StatusWarning || !strings.Contains(m.Message, "not in the model list") {
		t.Errorf("a model the agent does not offer: %+v", m)
	}
	if m := find(t, rep, "providers[agent].acp.modes.ask"); m.Status != StatusWarning {
		t.Errorf("missing ask mode: %+v", m)
	}
	for _, c := range rep.Checks {
		if c.Path == "providers[agent].acp.modes.plan" {
			t.Errorf("a set plan mode is reported: %+v", c)
		}
	}
}

// The agents a probe started are stopped when the run ends; what their
// connections note on the way out is not a finding and stays out of the
// terminal the report is printed to.
func TestDryRunKeepsTheAgentsConnectionLogQuiet(t *testing.T) {
	// slog's default logger writes through the log package.
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	body := acpRowYAML(t, acptest.Script{Models: []string{"small"}, Close: true}) + `models:
  - model: agent/default
agent:
  model: agent/default
`
	rep := run(t, body, nil)
	if p := find(t, rep, "providers[agent]"); p.Status != StatusOK {
		t.Fatalf("provider check %+v", p)
	}
	if strings.Contains(logged.String(), "connection closed") {
		t.Fatalf("the run logged the agent's connection:\n%s", logged.String())
	}
}
