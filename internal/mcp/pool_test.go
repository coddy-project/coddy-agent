package mcp

// Tests of the server pool (pool.go) and of how a stdio server stops. The
// stdio stub, TestHelperPoolServer, records every start and every exit in the
// file it is handed, so a test counts the processes a scenario started and
// sees which of them stopped.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const (
	// poolRecordEnv names the file the stub records its starts and exits in.
	poolRecordEnv = "CODDY_TEST_POOL_RECORD"
	// poolHoldEnv names a file the stub waits for before it answers
	// initialize: a server that is still starting.
	poolHoldEnv = "CODDY_TEST_POOL_HOLD"
	// poolFailFirstEnv names a file the stub creates and then exits without
	// answering when it does not exist yet: a first start that fails.
	poolFailFirstEnv = "CODDY_TEST_POOL_FAIL_FIRST"
	// poolPIDEnv names a file the stub writes its pid to.
	poolPIDEnv = "CODDY_TEST_POOL_PID"
)

// TestHelperPoolServer is the stdio MCP stub of the pool tests. It offers
// "echo", which answers with its text argument, and "crash", which ends the
// process without an answer.
func TestHelperPoolServer(t *testing.T) {
	record := os.Getenv(poolRecordEnv)
	if record == "" {
		t.Skip("helper process")
	}
	appendLine(record, "started", true)
	if pidFile := os.Getenv(poolPIDEnv); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	if first := os.Getenv(poolFailFirstEnv); first != "" {
		if _, err := os.Stat(first); err != nil {
			_ = os.WriteFile(first, []byte("1"), 0o644)
			os.Exit(3)
		}
	}
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req struct {
			ID     interface{}     `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(in.Bytes(), &req); err != nil || req.ID == nil {
			continue
		}
		var result interface{} = map[string]interface{}{}
		switch req.Method {
		case "initialize":
			for hold := os.Getenv(poolHoldEnv); hold != ""; {
				if _, err := os.Stat(hold); err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "pool-stub", "version": "1"},
			}
		case "tools/list":
			result = map[string]interface{}{"tools": []map[string]interface{}{
				{"name": "echo", "description": "Echo text back", "inputSchema": map[string]interface{}{"type": "object"}},
				{"name": "crash", "description": "End the server", "inputSchema": map[string]interface{}{"type": "object"}},
			}}
		case "tools/call":
			var params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.Name == "crash" {
				os.Exit(4)
			}
			if params.Name == "hang" {
				// Never answers: a call the server is still working on.
				continue
			}
			if params.Name == "stall" {
				// Answers, then stops reading stdin for good: a server
				// stuck in its own work while clients keep writing.
				_ = out.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": map[string]interface{}{}})
				time.Sleep(time.Hour)
			}
			result = map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": params.Arguments["text"]}}}
		}
		_ = out.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	// Appended to the file the start created, never created anew: a server
	// stopped as its test ends must not put a file back into a temporary
	// folder being removed.
	appendLine(record, "exited", false)
	os.Exit(0)
}

func appendLine(path, line string, create bool) {
	flags := os.O_APPEND | os.O_WRONLY
	if create {
		flags |= os.O_CREATE
	}
	if f, err := os.OpenFile(path, flags, 0o644); err == nil {
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
}

// poolStub declares the stub as a server named name recording into record.
func poolStub(name, record string, env ...config.EnvVarConfig) config.MCPServerConfig {
	return config.MCPServerConfig{
		Type:    "stdio",
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestHelperPoolServer$"},
		Env:     append([]config.EnvVarConfig{{Name: poolRecordEnv, Value: record}}, env...),
	}
}

func globalServer(srv config.MCPServerConfig) ManagedServer {
	return ManagedServer{Config: srv, Scope: ScopeGlobal, Origin: OriginHome}
}

func projectManaged(srv config.MCPServerConfig) ManagedServer {
	return ManagedServer{Config: srv, Scope: ScopeLocal, Origin: OriginProject}
}

// recorded counts the lines of kind ("started", "exited") in record.
func recorded(record, kind string) int {
	data, err := os.ReadFile(record)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), kind+"\n")
}

func eventually(t *testing.T, within time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func newTestPool(t *testing.T) *Pool {
	t.Helper()
	pool := NewPool(slog.Default())
	pool.SetDialTimeout(10 * time.Second)
	pool.SetStopDelay(0)
	t.Cleanup(pool.Close)
	return pool
}

func acquire(t *testing.T, pool *Pool, srv ManagedServer, workspace string) *Client {
	t.Helper()
	client, err := pool.Acquire(testCtx(t), srv, workspace)
	if err != nil {
		t.Fatalf("Acquire %s: %v", srv.Config.Name, err)
	}
	return client
}

// Two leases on one server are one process: both call through it, one lease
// given back leaves the other working, and the last one stops the server.
func TestPoolSharesOneServerBetweenLeases(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	srv := globalServer(poolStub("shared", record))
	first := acquire(t, pool, srv, t.TempDir())
	second := acquire(t, pool, srv, t.TempDir())
	if got := recorded(record, "started"); got != 1 {
		t.Fatalf("two leases started %d processes, want 1", got)
	}
	for _, c := range []*Client{first, second} {
		got, err := c.CallTool(testCtx(t), "echo", `{"text":"hi"}`)
		if err != nil || got != "hi" {
			t.Fatalf("CallTool = %q, %v", got, err)
		}
		if c.Declared() != Fingerprint(srv.Config) {
			t.Fatalf("Declared() = %q, want the declaration's fingerprint", c.Declared())
		}
	}
	_ = first.Close()
	if got, err := second.CallTool(testCtx(t), "echo", `{"text":"still"}`); err != nil || got != "still" {
		t.Fatalf("the remaining lease stopped working: %q, %v", got, err)
	}
	if got := recorded(record, "exited"); got != 0 {
		t.Fatal("giving one lease back stopped the server")
	}
	_ = second.Close()
	if !eventually(t, 5*time.Second, func() bool { return recorded(record, "exited") == 1 }) {
		t.Fatal("the server kept running after the last lease was given back")
	}
	if running := pool.Running(); len(running) != 0 {
		t.Fatalf("the pool still lists %+v", running)
	}
}

// Callers that ask for a server at the same moment wait for one start.
func TestPoolStartsOnceForConcurrentAcquires(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	srv := globalServer(poolStub("shared", record))
	var wg sync.WaitGroup
	clients := make([]*Client, 6)
	errs := make([]error, 6)
	for i := range clients {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clients[i], errs[i] = pool.Acquire(testCtx(t), srv, "")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		defer func(c *Client) { _ = c.Close() }(clients[i])
	}
	if got := recorded(record, "started"); got != 1 {
		t.Fatalf("six concurrent callers started %d processes, want 1", got)
	}
	if running := pool.Running(); len(running) != 1 || running[0].Leases != 6 {
		t.Fatalf("Running() = %+v, want one server with six leases", running)
	}
}

// A project server, and a global one that names the workspace, runs once per
// workspace; a global server that does not is one process everywhere.
func TestPoolRunsWorkspaceBoundServersPerWorkspace(t *testing.T) {
	dir := t.TempDir()
	projectRecord, cwdRecord, plainRecord := filepath.Join(dir, "project"), filepath.Join(dir, "cwd"), filepath.Join(dir, "plain")
	pool := newTestPool(t)
	project := projectManaged(poolStub("proj", projectRecord))
	namesCWD := globalServer(poolStub("cwd", cwdRecord, config.EnvVarConfig{Name: "WORKSPACE", Value: "${CWD}"}))
	plain := globalServer(poolStub("plain", plainRecord))
	if WorkspaceBound(plain) || !WorkspaceBound(project) || !WorkspaceBound(namesCWD) {
		t.Fatalf("WorkspaceBound: plain=%v project=%v cwd=%v", WorkspaceBound(plain), WorkspaceBound(project), WorkspaceBound(namesCWD))
	}
	a, b := t.TempDir(), t.TempDir()
	for _, ws := range []string{a, a, b} {
		for _, srv := range []ManagedServer{project, namesCWD, plain} {
			c := acquire(t, pool, srv, ws)
			t.Cleanup(func() { _ = c.Close() })
		}
	}
	for record, want := range map[string]int{projectRecord: 2, cwdRecord: 2, plainRecord: 1} {
		if got := recorded(record, "started"); got != want {
			t.Fatalf("%s started %d times over three sessions in two workspaces, want %d", filepath.Base(record), got, want)
		}
	}
}

// A kept server starts with no caller, outlives its leases, and stops once
// the pool no longer keeps it.
func TestPoolKeepsKeptServersWithoutLeases(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	srv := globalServer(poolStub("kept", record))
	pool.Keep([]ManagedServer{srv})
	if !eventually(t, 10*time.Second, func() bool {
		running := pool.Running()
		return len(running) == 1 && running[0].Connected && running[0].Kept
	}) {
		t.Fatalf("the kept server did not start on its own: %+v", pool.Running())
	}
	lease := acquire(t, pool, srv, t.TempDir())
	_ = lease.Close()
	time.Sleep(200 * time.Millisecond)
	if got := recorded(record, "started"); got != 1 {
		t.Fatalf("a lease on a kept server started %d processes, want 1", got)
	}
	if got := recorded(record, "exited"); got != 0 {
		t.Fatal("the kept server stopped when its last lease was given back")
	}
	pool.Keep(nil)
	if !eventually(t, 5*time.Second, func() bool { return recorded(record, "exited") == 1 }) {
		t.Fatal("a server the pool no longer keeps kept running")
	}
}

// A server let go while a session still holds it stops only when that
// session gives it back.
func TestPoolLetsGoOfAKeptServerOnlyWhenItsLeasesAreBack(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	srv := globalServer(poolStub("kept", record))
	pool.Keep([]ManagedServer{srv})
	lease := acquire(t, pool, srv, "")
	pool.Keep(nil)
	time.Sleep(200 * time.Millisecond)
	if got := recorded(record, "exited"); got != 0 {
		t.Fatal("letting go of the server stopped it under a session that holds it")
	}
	if got, err := lease.CallTool(testCtx(t), "echo", `{"text":"x"}`); err != nil || got != "x" {
		t.Fatalf("CallTool = %q, %v", got, err)
	}
	_ = lease.Close()
	if !eventually(t, 5*time.Second, func() bool { return recorded(record, "exited") == 1 }) {
		t.Fatal("the server kept running after its last lease came back")
	}
}

// A server whose process ended is not handed out again: its leases say so,
// and the next Acquire starts a new one.
func TestPoolStartsAnEndedServerAgain(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	srv := globalServer(poolStub("fragile", record))
	first := acquire(t, pool, srv, "")
	defer func() { _ = first.Close() }()
	if _, err := first.CallTool(testCtx(t), "crash", `{}`); err == nil {
		t.Fatal("a call that ended the server succeeded")
	}
	if !eventually(t, 5*time.Second, func() bool { return !first.Alive() }) {
		t.Fatal("a lease on a server that ended still reports it alive")
	}
	if !eventually(t, 5*time.Second, func() bool { return len(pool.Running()) == 0 }) {
		t.Fatalf("the pool still lists the ended server: %+v", pool.Running())
	}
	second := acquire(t, pool, srv, "")
	defer func() { _ = second.Close() }()
	if got := recorded(record, "started"); got != 2 {
		t.Fatalf("starts = %d, want a second start after the first process ended", got)
	}
	if got, err := second.CallTool(testCtx(t), "echo", `{"text":"back"}`); err != nil || got != "back" {
		t.Fatalf("CallTool on the new server = %q, %v", got, err)
	}
}

// A caller that gives up while the server starts leaves the start to the
// others: they get the server, and it was started once.
func TestPoolCallerThatGivesUpLeavesTheStartToTheOthers(t *testing.T) {
	dir := t.TempDir()
	record, hold := filepath.Join(dir, "record"), filepath.Join(dir, "hold")
	pool := newTestPool(t)
	srv := globalServer(poolStub("slow", record, config.EnvVarConfig{Name: poolHoldEnv, Value: hold}))

	quitter, quit := context.WithCancel(context.Background())
	quitErr := make(chan error, 1)
	go func() {
		_, err := pool.Acquire(quitter, srv, "")
		quitErr <- err
	}()
	if !eventually(t, 10*time.Second, func() bool { return recorded(record, "started") == 1 }) {
		t.Fatal("the server did not start")
	}
	type result struct {
		c   *Client
		err error
	}
	waiter := make(chan result, 1)
	go func() {
		c, err := pool.Acquire(testCtx(t), srv, "")
		waiter <- result{c, err}
	}()
	// The second caller has to be waiting before the first gives up: one that
	// arrives after the server answered finds nobody kept it, which is another
	// case (TestPoolStopsAServerNobodyWaitsFor). Without this the test raced
	// the goroutine and failed on a loaded runner with two starts.
	if !eventually(t, 10*time.Second, func() bool {
		running := pool.Running()
		return len(running) == 1 && running[0].Leases == 2
	}) {
		t.Fatal("the second caller never waited for the start")
	}
	quit()
	if err := <-quitErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("the caller that gave up got %v, want context.Canceled", err)
	}
	_ = os.WriteFile(hold, []byte("1"), 0o644)
	r := <-waiter
	if r.err != nil {
		t.Fatalf("the caller still waiting got %v", r.err)
	}
	defer func() { _ = r.c.Close() }()
	if got := recorded(record, "started"); got != 1 {
		t.Fatalf("starts = %d, want 1", got)
	}
}

// A server nobody waits for any more by the time it answers is stopped, not
// left running with nothing holding it.
func TestPoolStopsAServerNobodyWaitsFor(t *testing.T) {
	dir := t.TempDir()
	record, hold := filepath.Join(dir, "record"), filepath.Join(dir, "hold")
	pool := newTestPool(t)
	srv := globalServer(poolStub("slow", record, config.EnvVarConfig{Name: poolHoldEnv, Value: hold}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := pool.Acquire(ctx, srv, "")
		done <- err
	}()
	if !eventually(t, 10*time.Second, func() bool { return recorded(record, "started") == 1 }) {
		t.Fatal("the server did not start")
	}
	cancel()
	<-done
	_ = os.WriteFile(hold, []byte("1"), 0o644)
	if !eventually(t, 5*time.Second, func() bool { return recorded(record, "exited") == 1 }) {
		t.Fatal("a server nobody waited for any more kept running")
	}
	if running := pool.Running(); len(running) != 0 {
		t.Fatalf("the pool still lists %+v", running)
	}
}

// A failed start is not remembered: every caller waiting for it gets the
// error, and the next Acquire starts the server again.
func TestPoolDoesNotRememberAFailedStart(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	srv := globalServer(poolStub("flaky", record, config.EnvVarConfig{Name: poolFailFirstEnv, Value: filepath.Join(dir, "failed-once")}))
	pool := newTestPool(t)
	if _, err := pool.Acquire(testCtx(t), srv, ""); err == nil {
		t.Fatal("a server that exited without answering was handed out")
	}
	c := acquire(t, pool, srv, "")
	defer func() { _ = c.Close() }()
	if got := recorded(record, "started"); got != 2 {
		t.Fatalf("starts = %d, want the failed start retried", got)
	}
}

// A context that is already done starts nothing.
func TestPoolExpiredContextStartsNothing(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pool.Acquire(ctx, globalServer(poolStub("idle", record)), ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := recorded(record, "started"); got != 0 {
		t.Fatalf("an expired context started %d processes", got)
	}
}

// A closed lease refuses calls, and closing it twice gives it back once.
func TestClosedLeaseRefusesCalls(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	srv := globalServer(poolStub("shared", record))
	closed := acquire(t, pool, srv, "")
	kept := acquire(t, pool, srv, "")
	defer func() { _ = kept.Close() }()
	_ = closed.Close()
	_ = closed.Close()
	if _, err := closed.CallTool(testCtx(t), "echo", `{"text":"x"}`); err == nil {
		t.Fatal("a closed lease still calls the server")
	}
	if closed.Alive() {
		t.Fatal("a closed lease reports itself alive")
	}
	if running := pool.Running(); len(running) != 1 || running[0].Leases != 1 {
		t.Fatalf("Running() = %+v, want the one lease still held", running)
	}
	if got, err := kept.CallTool(testCtx(t), "echo", `{"text":"y"}`); err != nil || got != "y" {
		t.Fatalf("the other lease = %q, %v", got, err)
	}
}

// Close stops every server, kept or leased, and refuses leases after.
func TestPoolCloseStopsEveryServer(t *testing.T) {
	dir := t.TempDir()
	keptRecord, leasedRecord := filepath.Join(dir, "kept"), filepath.Join(dir, "leased")
	pool := NewPool(slog.Default())
	kept := globalServer(poolStub("kept", keptRecord))
	pool.Keep([]ManagedServer{kept})
	lease := acquire(t, pool, projectManaged(poolStub("leased", leasedRecord)), t.TempDir())
	_ = acquire(t, pool, kept, "")
	pool.Close()
	for _, record := range []string{keptRecord, leasedRecord} {
		if got := recorded(record, "exited"); got != 1 {
			t.Fatalf("%s: exits = %d after Close, want 1", filepath.Base(record), got)
		}
	}
	if lease.Alive() {
		t.Fatal("a lease on a closed pool reports itself alive")
	}
	if _, err := pool.Acquire(testCtx(t), kept, ""); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("Acquire after Close: err = %v, want ErrPoolClosed", err)
	}
}

// The key follows what runs, not the switches: a disabled tool or the
// order of the environment does not split a server, a changed argument
// does.
func TestPoolKeyFollowsWhatRuns(t *testing.T) {
	base := poolStub("s", "record", config.EnvVarConfig{Name: "A", Value: "1"}, config.EnvVarConfig{Name: "B", Value: "2"})
	switched := base
	switched.DisabledTools = []string{"echo"}
	reordered := base
	reordered.Env = []config.EnvVarConfig{base.Env[2], base.Env[0], base.Env[1]}
	edited := base
	edited.Args = append(append([]string(nil), base.Args...), "-test.v")
	key := PoolKey(globalServer(base), "/w")
	if PoolKey(globalServer(switched), "/w") != key || PoolKey(globalServer(reordered), "/w") != key {
		t.Fatal("a switch or the environment's order changed the key")
	}
	if PoolKey(globalServer(edited), "/w") == key {
		t.Fatal("an edited command line kept the key")
	}
	if PoolKey(globalServer(base), "/other") != key {
		t.Fatal("a global server that names no workspace got a key per workspace")
	}
	if PoolKey(projectManaged(base), "/w") == PoolKey(projectManaged(base), "/other") {
		t.Fatal("a project server got one key for two workspaces")
	}
	// A name set twice runs with its last value, so the order of its pairs
	// is what runs.
	twice := poolStub("s", "record", config.EnvVarConfig{Name: "T", Value: "old"}, config.EnvVarConfig{Name: "T", Value: "new"})
	swapped := twice
	swapped.Env = []config.EnvVarConfig{twice.Env[0], twice.Env[2], twice.Env[1]}
	if PoolKey(globalServer(twice), "/w") == PoolKey(globalServer(swapped), "/w") {
		t.Fatal("two declarations that run with different values of T share a key")
	}
}

// A server nothing holds runs on for the stop delay: a session that takes it
// within the delay gets the same process, and once the delay passes with
// nobody taking it the server stops.
func TestPoolStopDelayBridgesASessionSwap(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	pool.SetStopDelay(400 * time.Millisecond)
	srv := projectManaged(poolStub("proj", record))
	ws := t.TempDir()
	old := acquire(t, pool, srv, ws)
	_ = old.Close()
	fresh := acquire(t, pool, srv, ws)
	time.Sleep(600 * time.Millisecond)
	if got := recorded(record, "started"); got != 1 {
		t.Fatalf("starts = %d, want the server the old session let go of", got)
	}
	if got := recorded(record, "exited"); got != 0 {
		t.Fatal("the server stopped under the session that took it within the delay")
	}
	_ = fresh.Close()
	time.Sleep(100 * time.Millisecond)
	if got := recorded(record, "exited"); got != 0 {
		t.Fatal("the server stopped before its delay passed")
	}
	if !eventually(t, 5*time.Second, func() bool { return recorded(record, "exited") == 1 }) {
		t.Fatal("the server kept running after its delay passed with nobody holding it")
	}
}

// A server whose declaration is no longer in effect stops as soon as the
// last lease comes back, whatever the stop delay: the delay is for a server
// the configuration still wants and no session happens to hold.
func TestPoolStopsAnUnwantedServerAtOnce(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	pool.SetStopDelay(time.Hour)
	var wanted atomic.Bool
	wanted.Store(true)
	pool.SetWanted(func(ManagedServer, string) bool { return wanted.Load() })
	srv := projectManaged(poolStub("proj", record))
	ws := t.TempDir()
	lease := acquire(t, pool, srv, ws)
	_ = lease.Close()
	time.Sleep(300 * time.Millisecond)
	if got := recorded(record, "exited"); got != 0 {
		t.Fatal("a wanted server stopped before its delay passed")
	}
	lease = acquire(t, pool, srv, ws)
	wanted.Store(false)
	_ = lease.Close()
	if !eventually(t, 5*time.Second, func() bool { return recorded(record, "exited") == 1 }) {
		t.Fatal("a server no longer declared waited out the delay")
	}
	if got := recorded(record, "started"); got != 1 {
		t.Fatalf("starts = %d, want the lingering server taken again", got)
	}
}

// A kept server the configuration lets go of stops at once when no lease
// holds it, without the stop delay.
func TestPoolLetsGoOfAnUnkeptServerAtOnce(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	pool.SetStopDelay(time.Hour)
	srv := globalServer(poolStub("kept", record))
	pool.Keep([]ManagedServer{srv})
	if !eventually(t, 10*time.Second, func() bool { return recorded(record, "started") == 1 && len(pool.Running()) == 1 && pool.Running()[0].Connected }) {
		t.Fatal("the kept server did not start")
	}
	pool.Keep(nil)
	if !eventually(t, 5*time.Second, func() bool { return recorded(record, "exited") == 1 }) {
		t.Fatal("a server the configuration let go of waited out the stop delay")
	}
}

// A call still waiting for its answer fails as soon as its connection is
// ended, instead of hanging until the caller's own deadline.
func TestEndedConnectionFailsTheCallsInFlight(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	pool := newTestPool(t)
	lease := acquire(t, pool, globalServer(poolStub("slow", record)), "")
	failed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := lease.conn.callTool(ctx, "hang", `{}`)
		failed <- err
	}()
	time.Sleep(200 * time.Millisecond)
	_ = lease.Close()
	select {
	case err := <-failed:
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the call in flight ended with %v, want the connection's end", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call in flight kept waiting after its connection was ended")
	}
}

// statefulHTTPServer is a streamable HTTP MCP server that hands out a
// session id at initialize, answers 404 for it once forget is set - a server
// that restarted - and records the DELETE that ends a session.
type statefulHTTPServer struct {
	*httptest.Server
	forget  atomic.Bool
	deleted atomic.Int32
	inits   atomic.Int32
}

func newStatefulHTTPServer(t *testing.T) *statefulHTTPServer {
	t.Helper()
	s := &statefulHTTPServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.deleted.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		var req struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method != "initialize" && (s.forget.Load() || r.Header.Get("Mcp-Session-Id") != "sess-1") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result interface{} = map[string]interface{}{}
		switch req.Method {
		case "initialize":
			s.inits.Add(1)
			w.Header().Set("Mcp-Session-Id", "sess-1")
			result = map[string]interface{}{"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{}}
		case "tools/list":
			result = map[string]interface{}{"tools": []map[string]interface{}{{"name": "echo", "inputSchema": map[string]interface{}{"type": "object"}}}}
		case "tools/call":
			result = map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "ok"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(s.Close)
	return s
}

// A streamable HTTP server that no longer knows the session answers 404: the
// connection is gone for good, so its leases say so and the next Acquire
// opens a new session with a fresh initialize.
func TestStreamableHTTPSessionTheServerForgotIsGone(t *testing.T) {
	srv := newStatefulHTTPServer(t)
	pool := newTestPool(t)
	decl := globalServer(config.MCPServerConfig{Type: "http", Name: "remote", URL: srv.URL})
	lease := acquire(t, pool, decl, "")
	if got, err := lease.CallTool(testCtx(t), "echo", `{}`); err != nil || got != "ok" {
		t.Fatalf("CallTool = %q, %v", got, err)
	}
	srv.forget.Store(true)
	if _, err := lease.CallTool(testCtx(t), "echo", `{}`); err == nil {
		t.Fatal("a call on a session the server forgot succeeded")
	}
	if !eventually(t, 5*time.Second, func() bool { return !lease.Alive() }) {
		t.Fatal("the lease still reports a forgotten session alive")
	}
	srv.forget.Store(false)
	fresh := acquire(t, pool, decl, "")
	defer func() { _ = fresh.Close() }()
	if got := srv.inits.Load(); got != 2 {
		t.Fatalf("initialize was sent %d times, want a new session", got)
	}
	_ = lease.Close()
}

// Closing a streamable HTTP connection ends its session on the server before
// the transport counts as stopped, so a process on its way out does not
// leave the session behind; closing it from many goroutines at once is safe.
func TestStreamableHTTPCloseEndsTheSessionOnce(t *testing.T) {
	srv := newStatefulHTTPServer(t)
	client, err := NewHTTPClient(testCtx(t), "remote", srv.URL, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.conn.close()
		}()
	}
	wg.Wait()
	select {
	case <-client.conn.stopped():
	case <-time.After(10 * time.Second):
		t.Fatal("the transport never counted as stopped")
	}
	if got := srv.deleted.Load(); got != 1 {
		t.Fatalf("DELETE sent %d times by the time the transport stopped, want 1", got)
	}
}

// An answer that races in as a start's time runs out is not installed: the
// entry was already dropped for the next caller to start afresh, so a
// connection put there would run where neither Running nor Close finds it.
func TestPoolDropsAnAnswerThatCameAfterItsStartsTime(t *testing.T) {
	answered := NewStaticClient("late", nil)
	prev := poolConnect
	poolConnect = func(ctx context.Context, _ config.MCPServerConfig, _ string, _ *slog.Logger) (*Client, error) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		return answered, nil
	}
	t.Cleanup(func() { poolConnect = prev })
	pool := newTestPool(t)
	pool.SetDialTimeout(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pool.Acquire(ctx, globalServer(config.MCPServerConfig{Type: "stdio", Name: "late", Command: "late-mcp"}), "")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire = %v, want the start's own deadline", err)
	}
	if running := pool.Running(); len(running) != 0 {
		t.Fatalf("the pool lists %+v after the start ran out of time", running)
	}
	select {
	case <-answered.conn.gone:
	default:
		t.Fatal("the connection that answered too late was left open")
	}
}

// Closing a streamable HTTP connection aborts a request the server has not
// answered yet, instead of leaving the call inside it until its own deadline,
// and the transport counts as stopped only once that request is over.
func TestStreamableHTTPCloseAbortsARequestInFlight(t *testing.T) {
	var hanging atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method == "tools/call" {
			hanging.Store(true)
			<-r.Context().Done()
			return
		}
		var result interface{} = map[string]interface{}{"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{}}
		if req.Method == "tools/list" {
			result = map[string]interface{}{"tools": []interface{}{}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	client, err := NewHTTPClient(testCtx(t), "remote", srv.URL, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := client.conn.callTool(ctx, "slow", `{}`)
		done <- err
	}()
	if !eventually(t, 5*time.Second, hanging.Load) {
		t.Fatal("the call never reached the server")
	}
	_ = client.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the aborted call succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call stayed inside its request after Close")
	}
	select {
	case <-client.conn.stopped():
	case <-time.After(5 * time.Second):
		t.Fatal("the transport never counted as stopped")
	}
}
