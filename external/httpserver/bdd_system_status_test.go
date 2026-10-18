//go:build http

package httpserver

// Godog harness for features/system_status.feature: a real serve.Runtime over
// a temporary home, the HTTP API run as a subsystem by a serve.Supervisor on a
// free port, and the status document read over HTTP the way the web UI reads
// it. The @ui scenario of the feature belongs to external/ui.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

type systemStatusState struct {
	root    string
	addr    string
	cancel  context.CancelFunc
	stopped chan error
	restore []func()

	statusCode int
	doc        serve.StatusDocument
}

// setenv sets a variable for the scenario and puts it back afterwards.
func (s *systemStatusState) setenv(key, value string) {
	old, had := os.LookupEnv(key)
	s.restore = append(s.restore, func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
	_ = os.Setenv(key, value)
}

func (s *systemStatusState) close() {
	if s.cancel != nil {
		s.cancel()
		select {
		case <-s.stopped:
		case <-time.After(10 * time.Second):
		}
		s.cancel = nil
	}
	for i := len(s.restore) - 1; i >= 0; i-- {
		s.restore[i]()
	}
	s.restore = nil
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.doc = serve.StatusDocument{}
}

// aServeWithLowDisk runs the HTTP API under a supervisor, as coddy serve does.
// The sessions disk is reported low by the function the server is given: the
// runtime reads the real one through a seam this package cannot reach, and a
// test must not fill a disk, so that function takes the runtime's own snapshot
// (rt.Status), replaces only its Disk, which is below 512 MiB, and renders it
// the way Runtime.StatusDocument does (serve.RenderStatus).
func (s *systemStatusState) aServeWithLowDisk() error {
	root, err := os.MkdirTemp("", "coddy-system-status-*")
	if err != nil {
		return err
	}
	s.root = root
	// The operating system's home is the temporary folder, so the document has
	// a home to write as ~, and no role names a systemd unit or a daemon.
	s.setenv("HOME", root)
	s.setenv("USERPROFILE", root)
	s.setenv(serve.EnvRole, "")
	home, cwd := filepath.Join(root, ".coddy"), filepath.Join(root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	_ = ln.Close()

	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: cwd}}
	log := slog.New(slog.DiscardHandler)
	rt := &serve.Runtime{}
	if err := rt.Init(serve.Options{
		CLI: config.CLIPaths{Home: home, CWD: cwd}, SessionsRoot: filepath.Join(root, "sessions"),
		NeedsSessions: true, Log: log, Cfg: cfg,
	}); err != nil {
		return err
	}
	low := func() serve.StatusDocument {
		snap := rt.Status()
		snap.Disk = &serve.Disk{AvailableBytes: 100 << 20, SizeBytes: 100 << 30}
		userHome, _ := os.UserHomeDir()
		return serve.RenderStatus(snap, nil, time.Now(), userHome)
	}
	api := serve.Subsystem{
		Kind: serve.KindHTTP, ConfigKey: "httpserver.enable", BuildTag: "http", Available: true, NeedsSessions: true,
		Enabled: func(*config.Config) bool { return true },
		Address: func(*config.Config) string { return s.addr },
		Run: func(ctx context.Context, c *config.Config) error {
			return Serve(ctx, Options{
				Cfg: c, Mgr: rt.Mgr, Log: log, DefaultCWD: cwd, Home: home, ListenAddr: s.addr, Status: low,
			})
		},
	}
	sup := serve.NewSupervisor(log, []serve.Subsystem{api})
	rt.SetSupervisor(sup)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.stopped = make(chan error, 1)
	go func() { s.stopped <- sup.Run(ctx, cfg, make(chan *config.Config)) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", s.addr, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("the HTTP API never listened on %s", s.addr)
}

func (s *systemStatusState) theWebUIReads(path string) error {
	resp, err := http.Get("http://" + s.addr + path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	s.statusCode = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s\n%s", path, resp.Status, body)
	}
	return json.Unmarshal(body, &s.doc)
}

func (s *systemStatusState) itNamesTheBuildAndHowItWasStarted() error {
	want := serve.CurrentBuild()
	if s.doc.Object != serve.StatusObject {
		return fmt.Errorf("object = %q, want %q", s.doc.Object, serve.StatusObject)
	}
	if s.doc.Build.Version == "" || s.doc.Build.Version != want.Version {
		return fmt.Errorf("version = %q, want this binary's %q", s.doc.Build.Version, want.Version)
	}
	if !slices.Equal(s.doc.Build.Tags, want.Tags) {
		return fmt.Errorf("tags = %v, want this binary's %v", s.doc.Build.Tags, want.Tags)
	}
	if s.doc.Process.StartedBy != serve.StartedByForeground {
		return fmt.Errorf("startedBy = %q, want %q: nothing set a role", s.doc.Process.StartedBy, serve.StartedByForeground)
	}
	if s.doc.Process.ConfigFile != filepath.Join("~", ".coddy", "config.yaml") {
		return fmt.Errorf("configFile = %q, want the home written as ~", s.doc.Process.ConfigFile)
	}
	return nil
}

func (s *systemStatusState) theAttentionListNamesTheLowDisk() error {
	want := []serve.AttentionItem{{Code: serve.AttentionDiskLow}}
	if !slices.Equal(s.doc.Attention, want) {
		return fmt.Errorf("attention = %+v, want %+v", s.doc.Attention, want)
	}
	if s.doc.Disk == nil || s.doc.Disk.AvailableBytes >= serve.DiskLowBytes {
		return fmt.Errorf("disk = %+v, want less than %d bytes available", s.doc.Disk, int64(serve.DiskLowBytes))
	}
	return nil
}

func (s *systemStatusState) itListsTheAPIOnThatPort() error {
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		return err
	}
	if _, err := strconv.Atoi(port); err != nil {
		return err
	}
	for _, row := range s.doc.Subsystems {
		if row.Kind != serve.KindHTTP {
			continue
		}
		if row.State != serve.StateRunning || !strings.HasSuffix(row.Address, ":"+port) {
			return fmt.Errorf("httpserver row = %+v, want running on port %s", row, port)
		}
		return nil
	}
	return fmt.Errorf("no httpserver row in %+v", s.doc.Subsystems)
}

func initializeSystemStatusScenario(sc *godog.ScenarioContext) {
	s := &systemStatusState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a coddy serve with the HTTP API on a free port and the sessions disk reported below 512 MiB$`, s.aServeWithLowDisk)
	sc.Step(`^the web UI reads "([^"]*)"$`, s.theWebUIReads)
	sc.Step(`^it names the version and the build tags of this binary and how it was started$`, s.itNamesTheBuildAndHowItWasStarted)
	sc.Step(`^the attention list names the low disk$`, s.theAttentionListNamesTheLowDisk)
	sc.Step(`^it lists the HTTP API as running on that port$`, s.itListsTheAPIOnThatPort)
}

func TestSystemStatusFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "system-status",
		ScenarioInitializer: initializeSystemStatusScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/system_status.feature"},
			Tags:     "~@ui",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("system status feature failed")
	}
}
