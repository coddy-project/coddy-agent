package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestNewWritesToStdoutAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coddy.log")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	slogLog, closer, err := New(config.Logger{
		Level:   config.LogLevelDebug,
		Outputs: []string{config.LogOutputStdout, config.LogOutputFile},
		File:    path,
		Format:  config.LogFormatText,
	})
	if err != nil {
		t.Fatal(err)
	}
	slogLog.Info("hello", "key", "value")
	_ = closer.Close()
	_ = w.Close()

	stdoutBytes, _ := io.ReadAll(r)
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(stdoutBytes), "hello") || !strings.Contains(string(stdoutBytes), "key=value") {
		t.Fatalf("stdout missing record: %q", stdoutBytes)
	}
	if !strings.Contains(string(fileBytes), "hello") || !strings.Contains(string(fileBytes), "key=value") {
		t.Fatalf("file missing record: %q", fileBytes)
	}
}

func TestNewJSONFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coddy.log")
	slogLog, closer, err := New(config.Logger{
		Level:   config.LogLevelInfo,
		Outputs: []string{config.LogOutputFile},
		File:    path,
		Format:  config.LogFormatJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	slogLog.Info("hi", "k", 1)
	_ = closer.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &raw); err != nil {
		t.Fatalf("file not JSON: %v\n%s", err, data)
	}
	if raw["msg"] != "hi" {
		t.Fatalf("wrong msg: %+v", raw)
	}
}

func TestRotationSizeAndCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")

	rf, err := newRotatingFile(path, config.LoggerRotation{MaxSizeMB: 0, MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := rf.Write([]byte(strings.Repeat("a", 1024))); err != nil {
			t.Fatal(err)
		}
	}
	_ = rf.Close()
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatal("unexpected backup with MaxSizeMB=0")
	}

	rf, err = newRotatingFile(path, config.LoggerRotation{MaxSizeMB: 1, MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("z"), 256*1024)
	for i := 0; i < 15; i++ {
		if _, err := rf.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	_ = rf.Close()

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf(".1 backup missing: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf(".2 backup missing: %v", err)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatal(".3 backup must have been pruned")
	}
}

func TestParseTextRecord(t *testing.T) {
	var buf bytes.Buffer
	slogLog := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slogLog.Info("scheduler tick",
		"component", "scheduler",
		"task", "morning summary",
		"phase", "start",
	)

	recs, err := ParseReader(&buf, Filter{
		Attrs: map[string]string{"component": "scheduler", "task": "morning summary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d: %+v", len(recs), recs)
	}
	if recs[0].Message != "scheduler tick" {
		t.Fatalf("wrong msg: %q", recs[0].Message)
	}
	if recs[0].Attrs["phase"] != "start" {
		t.Fatalf("missing phase=start: %+v", recs[0].Attrs)
	}
	if recs[0].Time.IsZero() {
		t.Fatal("expected non-zero time")
	}
}

func TestParseJSONRecord(t *testing.T) {
	var buf bytes.Buffer
	slogLog := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slogLog.Warn("oops", "component", "scheduler", "task", "x")

	recs, err := ParseReader(&buf, Filter{Attrs: map[string]string{"component": "scheduler"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Level != "warn" {
		t.Fatalf("unexpected: %+v", recs)
	}
}

func TestParseFilterSinceAndLimit(t *testing.T) {
	var buf bytes.Buffer
	slogLog := slog.New(slog.NewTextHandler(&buf, nil))
	slogLog.Info("a", "n", "1")
	time.Sleep(20 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(20 * time.Millisecond)
	slogLog.Info("b", "n", "2")
	slogLog.Info("c", "n", "3")

	recs, err := ParseReader(&buf, Filter{Since: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("Since filter: want 2, got %d", len(recs))
	}

	buf.Reset()
	for i := 0; i < 5; i++ {
		slogLog.Info("x")
	}
	recs, err = ParseReader(&buf, Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("Limit filter: want 2, got %d", len(recs))
	}
}

func TestParseSkipsMalformed(t *testing.T) {
	in := strings.NewReader("garbage line\n" +
		"time=2026-05-03T16:00:00Z level=INFO msg=ok component=x\n" +
		"\n" +
		"another garbage\n")
	recs, err := ParseReader(in, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1, got %d: %+v", len(recs), recs)
	}
	if recs[0].Attrs["component"] != "x" {
		t.Fatalf("attr lost: %+v", recs[0].Attrs)
	}
}
