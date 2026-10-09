package session_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// logRecord is one line a manager wrote to its logger.
type logRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// logRecords collects what a manager logs, so a test reads the level and the
// words of a line instead of matching formatted output.
type logRecords struct {
	mu   sync.Mutex
	recs []logRecord
}

func (l *logRecords) handler() slog.Handler { return &recordingHandler{sink: l} }

func (l *logRecords) all() []logRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]logRecord(nil), l.recs...)
}

// at returns the records of one level whose message contains substr.
func (l *logRecords) at(level slog.Level, substr string) []logRecord {
	var out []logRecord
	for _, r := range l.all() {
		if r.level == level && strings.Contains(r.msg, substr) {
			out = append(out, r)
		}
	}
	return out
}

type recordingHandler struct {
	sink  *logRecords
	attrs []slog.Attr
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	for _, a := range h.attrs {
		attrs[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	h.sink.mu.Lock()
	h.sink.recs = append(h.sink.recs, logRecord{level: r.Level, msg: r.Message, attrs: attrs})
	h.sink.mu.Unlock()
	return nil
}

func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recordingHandler{sink: h.sink, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// diskFullError is what a write to a full volume returns, as the os package
// shapes it.
func diskFullError(op, path string) error {
	return &os.PathError{Op: op, Path: path, Err: diskFullErrno}
}

// faultOn fails the named operations of the store with err and lets every
// other one through.
func faultOn(err error, ops ...string) func(string) error {
	return func(op string) error {
		for _, o := range ops {
			if o == op {
				return err
			}
		}
		return nil
	}
}

func newStoreManager(t *testing.T) (*session.Manager, *session.FileStore, *logRecords) {
	t.Helper()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	logs := &logRecords{}
	m := session.NewManager(testConfig(), noopSender{}, noopRunner, slog.New(logs.handler()), root, store)
	return m, store, logs
}

// A session that cannot be laid out on disk because the volume is full fails
// with the cause intact (the caller tells a full disk from any other failure
// through platform.IsDiskFull) and the operator finds it in the log at error
// level, naming the full disk, rather than not at all.
func TestNewSessionOnAFullDiskKeepsTheCauseAndLogsAnError(t *testing.T) {
	m, store, logs := newStoreManager(t)
	store.SetFaultForTest(faultOn(diskFullError("mkdir", "/home/coddy/sessions/sess_x"), "layout"))

	_, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err == nil {
		t.Fatal("HandleSessionNew succeeded on a full disk")
	}
	if !platform.IsDiskFull(err) {
		t.Fatalf("the failure lost its cause: %v", err)
	}
	if !errors.Is(err, diskFullErrno) {
		t.Fatalf("errors.Is(err, %v) = false for %v", diskFullErrno, err)
	}
	recs := logs.at(slog.LevelError, "no space left on device")
	if len(recs) != 1 {
		t.Fatalf("error records naming the full disk = %d, want 1; log: %+v", len(recs), logs.all())
	}
	if recs[0].attrs["id"] == "" || recs[0].attrs["error"] == "" || recs[0].attrs["hint"] == "" {
		t.Fatalf("the record lacks the id, the error or the hint: %+v", recs[0])
	}
}

// Any other failure to lay a session out is not reported as a full disk.
func TestNewSessionFailureThatIsNotDiskFullIsNotCalledOne(t *testing.T) {
	m, store, logs := newStoreManager(t)
	store.SetFaultForTest(faultOn(&os.PathError{Op: "mkdir", Path: "/x", Err: syscall.EACCES}, "layout"))

	_, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err == nil {
		t.Fatal("HandleSessionNew succeeded")
	}
	if platform.IsDiskFull(err) {
		t.Fatalf("a permission failure reads as a full disk: %v", err)
	}
	if recs := logs.at(slog.LevelError, "no space left on device"); len(recs) != 0 {
		t.Fatalf("a full-disk error was logged for a permission failure: %+v", recs)
	}
}

// A save that fails because the volume is full is an error in the log with a
// hint about what is lost, and the session keeps running in memory.
func TestPersistFailureOnAFullDiskIsAnErrorWithAHint(t *testing.T) {
	m, store, logs := newStoreManager(t)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(res.SessionID)
	store.SetFaultForTest(faultOn(diskFullError("write", "/home/coddy/sessions/messages.json.tmp"), "save"))

	st.SetMode("plan")

	if got := st.GetMode(); got != "plan" {
		t.Fatalf("mode = %q: a failed save must not undo the change in memory", got)
	}
	recs := logs.at(slog.LevelError, "persist session")
	if len(recs) != 1 {
		t.Fatalf("error records for the failed save = %d, want 1; log: %+v", len(recs), logs.all())
	}
	r := recs[0]
	if !strings.Contains(r.msg, "no space left on device") {
		t.Fatalf("message = %q, want it to name the full disk", r.msg)
	}
	if r.attrs["id"] != res.SessionID {
		t.Fatalf("id attr = %q, want %q", r.attrs["id"], res.SessionID)
	}
	if !strings.Contains(r.attrs["hint"], "free") {
		t.Fatalf("hint = %q, want it to say what to do", r.attrs["hint"])
	}
	if warns := logs.at(slog.LevelWarn, "persist session"); len(warns) != 0 {
		t.Fatalf("the full-disk failure was also logged as a warning: %+v", warns)
	}
}

// A save that fails for any other reason stays the warning it always was.
func TestPersistFailureThatIsNotDiskFullStaysAWarning(t *testing.T) {
	m, store, logs := newStoreManager(t)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(res.SessionID)
	store.SetFaultForTest(faultOn(fmt.Errorf("save: %w", &os.PathError{Op: "write", Path: "/x", Err: syscall.EACCES}), "save"))

	st.SetMode("plan")

	if warns := logs.at(slog.LevelWarn, "persist session"); len(warns) != 1 {
		t.Fatalf("warnings = %d, want 1; log: %+v", len(warns), logs.all())
	}
	if errs := logs.at(slog.LevelError, ""); len(errs) != 0 {
		t.Fatalf("unexpected error records: %+v", errs)
	}
}

// The first save of a new session is held to the same rule.
func TestInitialSaveOnAFullDiskIsAnError(t *testing.T) {
	m, store, logs := newStoreManager(t)
	store.SetFaultForTest(faultOn(diskFullError("write", "/home/coddy/sessions/session.json"), "save"))

	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("a session whose layout was written must open even when its first save fails: %v", err)
	}
	if m.SessionByID(res.SessionID) == nil {
		t.Fatal("the session is not live")
	}
	if recs := logs.at(slog.LevelError, "no space left on device"); len(recs) == 0 {
		t.Fatalf("no error record names the full disk; log: %+v", logs.all())
	}
}
