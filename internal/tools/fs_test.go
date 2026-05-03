package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

func makeEnv(t *testing.T) *tools.Env {
	t.Helper()
	return &tools.Env{
		CWD:           t.TempDir(),
		RestrictToCWD: true,
	}
}

func TestReadFile(t *testing.T) {
	env := makeEnv(t)
	content := "hello, world\nline 2\n"
	path := filepath.Join(env.CWD, "test.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{"path": "test.txt"})
	result, err := reg.Execute(context.Background(), "read_file", string(args), env)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if result != content {
		t.Errorf("expected %q, got %q", content, result)
	}
}

func TestReadFileLines(t *testing.T) {
	env := makeEnv(t)
	content := "line1\nline2\nline3\nline4\n"
	path := filepath.Join(env.CWD, "test.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{
		"path":       "test.txt",
		"start_line": 2,
		"end_line":   3,
	})
	result, err := reg.Execute(context.Background(), "read_file", string(args), env)
	if err != nil {
		t.Fatalf("read_file with lines: %v", err)
	}
	if !strings.Contains(result, "line2") || !strings.Contains(result, "line3") {
		t.Errorf("unexpected result: %q", result)
	}
	if strings.Contains(result, "line1") || strings.Contains(result, "line4") {
		t.Errorf("should not contain out-of-range lines: %q", result)
	}
}

func TestWriteFile(t *testing.T) {
	env := makeEnv(t)
	reg := tools.NewRegistry()

	args, _ := json.Marshal(map[string]interface{}{
		"path":    "output.txt",
		"content": "new file content",
	})
	result, err := reg.Execute(context.Background(), "write_file", string(args), env)
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(result, "output.txt") {
		t.Errorf("unexpected result: %q", result)
	}

	data, err := os.ReadFile(filepath.Join(env.CWD, "output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new file content" {
		t.Errorf("file content mismatch: %q", string(data))
	}
}

func TestWriteFileCreatesDirectories(t *testing.T) {
	env := makeEnv(t)
	reg := tools.NewRegistry()

	args, _ := json.Marshal(map[string]interface{}{
		"path":    "subdir/nested/file.txt",
		"content": "nested content",
	})
	_, err := reg.Execute(context.Background(), "write_file", string(args), env)
	if err != nil {
		t.Fatalf("write_file nested: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(env.CWD, "subdir/nested/file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nested content" {
		t.Errorf("nested file content mismatch: %q", string(data))
	}
}

func TestReadFileOutsideCWD(t *testing.T) {
	env := makeEnv(t)
	reg := tools.NewRegistry()

	args, _ := json.Marshal(map[string]interface{}{"path": "/etc/passwd"})
	_, err := reg.Execute(context.Background(), "read_file", string(args), env)
	if err == nil {
		t.Error("expected error when reading outside CWD")
	}
}

func TestListDir(t *testing.T) {
	env := makeEnv(t)

	// Create some files.
	if err := os.WriteFile(filepath.Join(env.CWD, "a.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.CWD, "b.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(env.CWD, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{})
	result, err := reg.Execute(context.Background(), "list_dir", string(args), env)
	if err != nil {
		t.Fatalf("list_dir: %v", err)
	}
	if !strings.Contains(result, "a.go") || !strings.Contains(result, "b.go") {
		t.Errorf("missing files in list_dir output: %q", result)
	}
	if !strings.Contains(result, "subdir/") {
		t.Errorf("missing subdir in list_dir output: %q", result)
	}
}

func TestListDirHiddenDefault(t *testing.T) {
	env := makeEnv(t)
	if err := os.WriteFile(filepath.Join(env.CWD, "visible.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.CWD, ".hidden.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(env.CWD, ".hiddendir"), 0o755); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{})
	result, err := reg.Execute(context.Background(), "list_dir", string(args), env)
	if err != nil {
		t.Fatalf("list_dir: %v", err)
	}
	if !strings.Contains(result, "visible.txt") {
		t.Errorf("expected visible file: %q", result)
	}
	if strings.Contains(result, ".hidden.txt") || strings.Contains(result, ".hiddendir/") {
		t.Errorf("did not expect hidden entries by default: %q", result)
	}

	args, _ = json.Marshal(map[string]interface{}{"show_hidden": true})
	result, err = reg.Execute(context.Background(), "list_dir", string(args), env)
	if err != nil {
		t.Fatalf("list_dir show_hidden: %v", err)
	}
	if !strings.Contains(result, ".hidden.txt") || !strings.Contains(result, ".hiddendir/") {
		t.Errorf("expected hidden file and dir when show_hidden: %q", result)
	}
}

func TestListDirRecursiveSkipsHiddenSubtree(t *testing.T) {
	env := makeEnv(t)
	sub := filepath.Join(env.CWD, "outer")
	if err := os.MkdirAll(filepath.Join(sub, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "ok.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{"path": "outer", "recursive": true})
	result, err := reg.Execute(context.Background(), "list_dir", string(args), env)
	if err != nil {
		t.Fatalf("list_dir: %v", err)
	}
	if strings.Contains(result, ".git/") {
		t.Errorf("did not expect .git in non-show_hidden recursive listing: %q", result)
	}
	if !strings.Contains(result, "ok.txt") {
		t.Errorf("expected ok.txt: %q", result)
	}
}

func TestApplyDiff(t *testing.T) {
	env := makeEnv(t)

	original := "line1\nline2\nline3\n"
	path := filepath.Join(env.CWD, "file.txt")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// A simple diff that replaces line2 with newline2.
	diff := `@@ -2,1 +2,1 @@
-line2
+newline2
`
	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{
		"path": "file.txt",
		"diff": diff,
	})
	_, err := reg.Execute(context.Background(), "apply_diff", string(args), env)
	if err != nil {
		t.Fatalf("apply_diff: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "newline2") {
		t.Errorf("diff not applied: %q", string(data))
	}
}

func TestUnknownTool(t *testing.T) {
	env := makeEnv(t)
	reg := tools.NewRegistry()
	_, err := reg.Execute(context.Background(), "nonexistent_tool", "{}", env)
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestMkdir(t *testing.T) {
	env := makeEnv(t)
	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{"path": "a/b/c", "parents": true})
	if _, err := reg.Execute(context.Background(), "mkdir", string(args), env); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fi, err := os.Stat(filepath.Join(env.CWD, "a", "b", "c"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected directory: %v", err)
	}
}

func TestTouchCreatesFile(t *testing.T) {
	env := makeEnv(t)
	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{"path": "hello.txt"})
	if _, err := reg.Execute(context.Background(), "touch", string(args), env); err != nil {
		t.Fatalf("touch: %v", err)
	}
	fi, err := os.Stat(filepath.Join(env.CWD, "hello.txt"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("expected regular file: %v", err)
	}
}

func TestMvRename(t *testing.T) {
	env := makeEnv(t)
	src := filepath.Join(env.CWD, "old.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{"src": "old.txt", "dst": "new.txt"})
	if _, err := reg.Execute(context.Background(), "mv", string(args), env); err != nil {
		t.Fatalf("mv: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.CWD, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("expected old path removed")
	}
}

func TestRmFile(t *testing.T) {
	env := makeEnv(t)
	p := filepath.Join(env.CWD, "gone.txt")
	if err := os.WriteFile(p, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{"path": "gone.txt"})
	if _, err := reg.Execute(context.Background(), "rm", string(args), env); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("expected file removed")
	}
}

func TestRmdirEmptyDir(t *testing.T) {
	env := makeEnv(t)
	d := filepath.Join(env.CWD, "empty")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	args, _ := json.Marshal(map[string]interface{}{"path": "empty"})
	if _, err := reg.Execute(context.Background(), "rmdir", string(args), env); err != nil {
		t.Fatalf("rmdir: %v", err)
	}
	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Error("expected directory removed")
	}
}
