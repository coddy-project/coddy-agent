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
)

func TestConfigValidateDefaults(t *testing.T) {
	c := Config{}
	if err := c.Validate(); err != nil {
		t.Fatalf("default validate: %v", err)
	}
	if c.Level != LevelInfo {
		t.Fatalf("level default: got %q", c.Level)
	}
	if c.Format != FormatText {
		t.Fatalf("format default: got %q", c.Format)
	}
	if len(c.Outputs) != 1 || c.Outputs[0] != OutputStderr {
		t.Fatalf("outputs default: got %v", c.Outputs)
	}
}

func TestConfigValidateUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Config
	}{
		{"bad level", Config{Level: "trace"}},
		{"bad format", Config{Format: "yaml"}},
		{"bad output", Config{Outputs: []string{"udp"}}},
		{"file w/o path", Config{Outputs: []string{OutputFile}}},
		{"negative size", Config{Outputs: []string{OutputFile}, File: "x", Rotation: Rotation{MaxSizeMB: -1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			if err := c.Validate(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestApplyOverrides(t *testing.T) {
	c := Config{Level: "debug", Outputs: []string{OutputStdout}, Format: "json"}
	c.Apply(CLIOverrides{
		Level:  "warn",
		Output: "both",
		File:   "/tmp/x.log",
		Format: "text",
	})
	if c.Level != "warn" || c.Format != "text" || c.File != "/tmp/x.log" {
		t.Fatalf("apply scalars: %+v", c)
	}
	if len(c.Outputs) != 2 || c.Outputs[0] != OutputStdout || c.Outputs[1] != OutputFile {
		t.Fatalf("apply output 'both': %v", c.Outputs)
	}
}

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

	slogLog, closer, err := New(Config{
		Level:   LevelDebug,
		Outputs: []string{OutputStdout, OutputFile},
		File:    path,
		Format:  FormatText,
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
	slogLog, closer, err := New(Config{
		Level:   LevelInfo,
		Outputs: []string{OutputFile},
		File:    path,
		Format:  FormatJSON,
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

	rf, err := newRotatingFile(path, Rotation{MaxSizeMB: 0, MaxFiles: 2})
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

	rf, err = newRotatingFile(path, Rotation{MaxSizeMB: 1, MaxFiles: 2})
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
