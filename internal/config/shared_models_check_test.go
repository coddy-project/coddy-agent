package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCheckFile writes body as <home>/config.yaml and returns its path.
func writeCheckFile(t *testing.T, home, body string) string {
	t.Helper()
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// noOutOfBandCredentials keeps the machine's own CODDY_HTTP_TOKEN & co. out of
// a check that is about the file.
func noOutOfBandCredentials(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CODDY_HTTP_TOKEN", "CODDY_HTTP_USER", "CODDY_HTTP_PASSWORD", "CODDY_SWARM_TOKEN", "CODDY_SWARM_PAIRING_TOKEN"} {
		t.Setenv(name, "")
	}
}

// findingAt returns the finding whose path is path, failing when there is none or
// more than one.
func findingAt(t *testing.T, findings []Finding, sev Severity, path string) Finding {
	t.Helper()
	var got []Finding
	for _, f := range findings {
		if f.Severity == sev && f.Path == path {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one finding at %s, got %d in %+v", path, len(got), findings)
	}
	return got[0]
}

func noFindingAt(t *testing.T, findings []Finding, path string) {
	t.Helper()
	for _, f := range findings {
		if f.Path == path {
			t.Fatalf("unexpected finding at %s: %+v", path, f)
		}
	}
}

func TestCheckAcceptsAConfigThatSharesAModel(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline(`providers:
  - name: openai
    type: openai
    api_key: sk-k
  - name: remote
    type: coddy
    api_base: https://remote.example:12345
    busy_wait_ms: 10000
models:
  - model: openai/gpt-5.5
    shared_as: smart
  - model: remote/other
    shared_as: ""
httpserver:
  auth_token: main
  shared_models:
    tokens: [tok-share-1]
    max_streams: 3
    max_call_ms: 600000
agent:
  shared_busy_wait_ms: 5000
`))
	if !rep.Valid() || len(rep.Findings) != 0 {
		t.Fatalf("a valid config must produce no findings, got %+v", rep.Findings)
	}
}

func TestCheckReportsSharedModelsWithoutAuthenticationAsAnError(t *testing.T) {
	noOutOfBandCredentials(t)
	const head = `providers:
  - name: openai
    type: openai
models:
  - model: openai/private
  - model: openai/gpt-5.5
    shared_as: smart
`
	f := onlyError(t, checkYAML(t, withModeline(head)))
	if f.Path != "models[openai/gpt-5.5].shared_as" || f.Line != 8 || f.Column == 0 {
		t.Errorf("finding at %q %d:%d, want the first shared_as on line 8", f.Path, f.Line, f.Column)
	}
	if !strings.Contains(f.Message, "authentication") || !strings.Contains(f.Fix, "httpserver.shared_models.tokens") || !strings.Contains(f.Fix, "allow_insecure") {
		t.Errorf("message %q / fix %q do not say what to do", f.Message, f.Fix)
	}
	if f.Doc == "" {
		t.Error("the finding has no doc line")
	}

	for name, extra := range map[string]string{
		"main token":        "httpserver:\n  auth_token: main\n",
		"shared token":      "httpserver:\n  shared_models:\n    tokens: [tok-share-1]\n",
		"login account":     "httpserver:\n  login:\n    user: me\n    password_hash: \"$$argon2id$$v=19$$m=65536,t=3,p=4$$c2FsdHNhbHQ$$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g\"\n",
		"allow_insecure":    "httpserver:\n  allow_insecure: true\n",
		"explicit login on": "httpserver:\n  login:\n    enable: true\n",
	} {
		rep := checkYAML(t, withModeline(head+extra))
		for _, e := range errorsOf(rep) {
			if strings.Contains(e.Message, "authentication") {
				t.Errorf("%s: still reported: %+v", name, e)
			}
		}
	}

	// A token out of band closes the gate too: the check sees the environment.
	t.Setenv("CODDY_HTTP_TOKEN", "from-env")
	if rep := checkYAML(t, withModeline(head)); !rep.Valid() {
		t.Errorf("CODDY_HTTP_TOKEN not counted: %+v", rep.Findings)
	}
	t.Setenv("CODDY_HTTP_TOKEN", "")
	t.Setenv("CODDY_HTTP_USER", "me")
	t.Setenv("CODDY_HTTP_PASSWORD", "pw")
	if rep := checkYAML(t, withModeline(head)); !rep.Valid() {
		t.Errorf("CODDY_HTTP_USER/PASSWORD not counted: %+v", rep.Findings)
	}
}

func TestCheckWithCountsTheCredentialsOfTheFlags(t *testing.T) {
	noOutOfBandCredentials(t)
	home := t.TempDir()
	body := withModeline(`providers:
  - name: openai
    type: openai
models:
  - model: openai/m
    shared_as: smart
httpserver:
  shared_models:
    tokens: [tok-share-1]
`)
	path := writeCheckFile(t, home, body)
	rep, err := CheckWith(CLIPaths{Home: home, Config: path}, ExtraTokens{HTTP: []string{"tok-share-1"}})
	if err != nil {
		t.Fatal(err)
	}
	f := onlyError(t, rep)
	if f.Path != "httpserver.shared_models.tokens[0]" || !strings.Contains(f.Message, "CODDY_HTTP_TOKEN") {
		t.Errorf("finding %+v does not name the token of --auth-token", f)
	}
	if strings.Contains(f.Message, "tok-share-1") || strings.Contains(f.Fix, "tok-share-1") {
		t.Errorf("finding leaks the token: %+v", f)
	}
	if f.Line != 10 {
		t.Errorf("finding on line %d, want the token entry on line 10", f.Line)
	}
}

func TestCheckPointsAtTheSecondRowOfADuplicateAlias(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline(`providers:
  - name: openai
    type: openai
models:
  - model: openai/a
    shared_as: dup
  - model: openai/b
    shared_as: dup
httpserver:
  auth_token: t
`))
	f := onlyError(t, rep)
	if f.Line != 9 || f.Column == 0 {
		t.Errorf("position %d:%d, want line 9", f.Line, f.Column)
	}
	if !strings.Contains(f.Message, "models[openai/a]") || !strings.Contains(f.Message, "models[openai/b]") {
		t.Errorf("message %q does not name both rows", f.Message)
	}
	if !strings.Contains(f.Fix, "alias of its own") {
		t.Errorf("fix %q", f.Fix)
	}
}

func TestCheckRefusesASubscriptionRowWithoutTheAcknowledgement(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline(`providers:
  - name: cx
    type: codex
models:
  - model: cx/gpt-5.5
    shared_as: smart
httpserver:
  auth_token: t
`))
	f := onlyError(t, rep)
	if f.Line != 7 {
		t.Errorf("line %d, want 7", f.Line)
	}
	if !strings.Contains(f.Message, "quota") || !strings.Contains(f.Message, "terms") || !strings.Contains(f.Fix, "shared_subscription_ack: true") {
		t.Errorf("finding %+v lacks the warning or the way out", f)
	}
}

func TestCheckReportsABadAliasOnce(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline(`providers:
  - name: openai
    type: openai
models:
  - model: openai/a
    shared_as: "with/slash"
httpserver:
  auth_token: t
`))
	f := onlyError(t, rep)
	if f.Line != 7 {
		t.Errorf("line %d, want 7", f.Line)
	}
}

func TestCheckSchemaBoundsOfTheSharedLimits(t *testing.T) {
	noOutOfBandCredentials(t)
	f := onlyError(t, checkYAML(t, withModeline("httpserver:\n  shared_models:\n    max_call_ms: 28800001\n")))
	if f.Path != "httpserver.shared_models.max_call_ms" || !strings.Contains(f.Message, "28800000") {
		t.Errorf("finding %+v", f)
	}
	f = onlyError(t, checkYAML(t, withModeline("httpserver:\n  shared_models:\n    max_streams: -1\n")))
	if f.Path != "httpserver.shared_models.max_streams" {
		t.Errorf("finding %+v", f)
	}
	f = onlyError(t, checkYAML(t, withModeline("agent:\n  shared_busy_wait_ms: -1\n")))
	if f.Path != "agent.shared_busy_wait_ms" {
		t.Errorf("finding %+v", f)
	}
}

func TestCheckCoddyProviderBase(t *testing.T) {
	noOutOfBandCredentials(t)
	f := onlyError(t, checkYAML(t, withModeline("providers:\n  - name: remote\n    type: coddy\n")))
	if f.Line != 3 || !strings.Contains(f.Message, "api_base") {
		t.Errorf("finding %+v", f)
	}
	f = onlyError(t, checkYAML(t, withModeline("providers:\n  - name: remote\n    type: coddy\n    api_base: ftp://x\n")))
	if f.Line != 5 || !strings.Contains(f.Message, "api_base") {
		t.Errorf("finding %+v", f)
	}
}

func TestCheckWarnsAboutAClearTextRemote(t *testing.T) {
	noOutOfBandCredentials(t)
	row := func(base string) string {
		return "providers:\n  - name: remote\n    type: coddy\n    api_base: " + base + "\n"
	}
	rep := checkYAML(t, withModeline(row("http://remote.example:12345")))
	if !rep.Valid() {
		t.Fatalf("a warning must not fail the check: %+v", rep.Findings)
	}
	w := findingAt(t, rep.Findings, SeverityWarning, "providers[remote].api_base")
	if w.Line != 5 || !strings.Contains(w.Message, "clear text") || !strings.Contains(w.Fix, "https") {
		t.Errorf("warning %+v", w)
	}
	for _, ok := range []string{
		"https://remote.example", "http://localhost:12345", "http://127.0.0.1:12345", "http://[::1]:12345",
		"http://box.localhost:12345", "https://relay.example/swarm/nodes/box",
	} {
		noFindingAt(t, checkYAML(t, withModeline(row(ok))).Findings, "providers[remote].api_base")
	}
	// Another provider type with an http address is the operator's local server.
	rep = checkYAML(t, withModeline("providers:\n  - name: lan\n    type: openai\n    api_base: http://10.0.0.5:8080/v1\n"))
	noFindingAt(t, rep.Findings, "providers[lan].api_base")
}

func TestCheckWarnsAboutAStreamGuardTooShortForTheHeartbeat(t *testing.T) {
	noOutOfBandCredentials(t)
	const coddy = "providers:\n  - name: remote\n    type: coddy\n    api_base: https://remote.example\n"
	for _, tc := range []struct {
		name, agent string
		warn        bool
	}{
		{"off", "agent:\n  llm_stream_idle_timeout_ms: 0\n", true},
		{"below twice the heartbeat", "agent:\n  llm_stream_idle_timeout_ms: 29999\n", true},
		{"short", "agent:\n  llm_stream_idle_timeout_ms: 5000\n", true},
		{"exactly twice", "agent:\n  llm_stream_idle_timeout_ms: 30000\n", false},
		{"default", "", false},
		{"long", "agent:\n  llm_stream_idle_timeout_ms: 600000\n", false},
	} {
		rep := checkYAML(t, withModeline(coddy+tc.agent))
		if !rep.Valid() {
			t.Fatalf("%s: a warning must not fail the check: %+v", tc.name, rep.Findings)
		}
		if tc.warn {
			w := findingAt(t, rep.Findings, SeverityWarning, "agent.llm_stream_idle_timeout_ms")
			if w.Line != 7 {
				t.Errorf("%s: warning on line %d, want 7: %+v", tc.name, w.Line, w)
			}
		} else {
			noFindingAt(t, rep.Findings, "agent.llm_stream_idle_timeout_ms")
		}
	}
	// Without a coddy provider the guard is nobody's concern here.
	rep := checkYAML(t, withModeline("providers:\n  - name: a\n    type: openai\nagent:\n  llm_stream_idle_timeout_ms: 0\n"))
	noFindingAt(t, rep.Findings, "agent.llm_stream_idle_timeout_ms")
}

func TestCheckWarnsAboutABlockingSharedRowThatCanHoldASlotForHours(t *testing.T) {
	noOutOfBandCredentials(t)
	rows := func(provider, share, limits string) string {
		return withModeline("providers:\n  - name: openai\n    type: openai\n" + provider +
			"models:\n  - model: openai/slow\n    stream: false\n" + share +
			"httpserver:\n  auth_token: t\n" + limits)
	}
	const shared = "    shared_as: slow\n"
	raised := "  shared_models:\n    max_call_ms: 3600000\n"
	rep := checkYAML(t, rows("", shared, raised))
	w := findingAt(t, rep.Findings, SeverityWarning, "models[openai/slow].stream")
	if w.Line != 7 || !strings.Contains(w.Message, "3600000") || !strings.Contains(w.Fix, "timeout_ms") {
		t.Errorf("warning %+v", w)
	}
	// An explicit 0 is the eight hour ceiling.
	w = findingAt(t, checkYAML(t, rows("", shared, "  shared_models:\n    max_call_ms: 0\n")).Findings, SeverityWarning, "models[openai/slow].stream")
	if !strings.Contains(w.Message, "28800000") {
		t.Errorf("warning %+v does not say the effective bound", w)
	}
	for name, body := range map[string]string{
		"default max_call_ms":       rows("", shared, ""),
		"max_call_ms at 30 minutes": rows("", shared, "  shared_models:\n    max_call_ms: 1800000\n"),
		"timeout_ms on provider":    rows("    timeout_ms: 600000\n", shared, raised),
		"row not shared":            rows("", "", raised),
		"streaming row":             strings.Replace(rows("", shared, raised), "    stream: false\n", "", 1),
	} {
		noFindingAt(t, checkYAML(t, body).Findings, "models[openai/slow].stream")
		_ = name
	}
}

func TestCheckWarnsAboutABlankSharedToken(t *testing.T) {
	noOutOfBandCredentials(t)
	t.Setenv("UNSET_SHARED_TOKEN", "")
	rep := checkYAML(t, withModeline("httpserver:\n  auth_token: main\n  shared_models:\n    tokens:\n      - real\n      - ${UNSET_SHARED_TOKEN}\n      - \"\"\n"))
	if !rep.Valid() {
		t.Fatalf("a warning must not fail the check: %+v", rep.Findings)
	}
	findingAt(t, rep.Findings, SeverityWarning, "httpserver.shared_models.tokens[1]")
	w := findingAt(t, rep.Findings, SeverityWarning, "httpserver.shared_models.tokens[2]")
	if w.Line != 8 || !strings.Contains(w.Message, "ignored") {
		t.Errorf("warning %+v", w)
	}
	noFindingAt(t, rep.Findings, "httpserver.shared_models.tokens[0]")
}

func TestCheckSaysTheAPIIsClosedWhenOnlySharedTokensExist(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline("httpserver:\n  shared_models:\n    tokens: [tok-share-1]\n"))
	if !rep.Valid() {
		t.Fatalf("a notice must not fail the check: %+v", rep.Findings)
	}
	w := findingAt(t, rep.Findings, SeverityWarning, "httpserver.shared_models.tokens")
	if !strings.Contains(w.Message, "closed") || !strings.Contains(w.Message, "web UI") || w.Line != 4 {
		t.Errorf("notice %+v", w)
	}
	for name, body := range map[string]string{
		"main token":    "httpserver:\n  auth_token: main\n  shared_models:\n    tokens: [tok-share-1]\n",
		"login account": "httpserver:\n  login:\n    user: me\n    password_hash: \"$$argon2id$$v=19$$m=65536,t=3,p=4$$c2FsdHNhbHQ$$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g\"\n  shared_models:\n    tokens: [tok-share-1]\n",
	} {
		if r := checkYAML(t, withModeline(body)); len(warningsOf(r)) != 0 {
			t.Errorf("%s: unexpected notice %+v", name, r.Findings)
		}
	}
	t.Setenv("CODDY_HTTP_TOKEN", "main-from-env")
	noFindingAt(t, checkYAML(t, withModeline("httpserver:\n  shared_models:\n    tokens: [tok-share-1]\n")).Findings, "httpserver.shared_models.tokens")
}

func TestCheckWarnsAboutBusyWaitOnAProviderThatNeverWaits(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline("providers:\n  - name: lan\n    type: openai\n    busy_wait_ms: 5000\n  - name: remote\n    type: coddy\n    api_base: https://r.example\n    busy_wait_ms: 5000\n"))
	if !rep.Valid() {
		t.Fatalf("a warning must not fail the check: %+v", rep.Findings)
	}
	w := findingAt(t, rep.Findings, SeverityWarning, "providers[lan].busy_wait_ms")
	if w.Line != 5 {
		t.Errorf("warning on line %d, want 5", w.Line)
	}
	noFindingAt(t, rep.Findings, "providers[remote].busy_wait_ms")
}

func TestCheckRefusesASharedTokenOfTwoClassesInTheFile(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline("swarm:\n  auth_token: same\nhttpserver:\n  shared_models:\n    tokens:\n      - other\n      - same\n"))
	f := onlyError(t, rep)
	if f.Line != 8 {
		t.Errorf("line %d: want the offending token entry on line 8", f.Line)
	}
	if !strings.Contains(f.Message, "httpserver.shared_models.tokens[1]") && f.Path != "httpserver.shared_models.tokens[1]" {
		t.Errorf("finding %+v does not name the entry", f)
	}
	if !strings.Contains(f.Message, "swarm.auth_token") {
		t.Errorf("finding %+v does not name swarm.auth_token", f)
	}
}

func TestCheckRedactsSharedTokensInMessages(t *testing.T) {
	noOutOfBandCredentials(t)
	rep := checkYAML(t, withModeline("httpserver:\n  shared_models:\n    tokens: supersecret-shared\n"))
	f := onlyError(t, rep)
	if strings.Contains(f.Message, "supersecret-shared") || strings.Contains(f.Fix, "supersecret-shared") {
		t.Errorf("a token must not be echoed: %+v", f)
	}
}
