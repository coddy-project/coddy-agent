package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDotEnvLine(t *testing.T) {
	cases := []struct {
		line  string
		key   string
		value string
		ok    bool
	}{
		// basic
		{"KEY=value", "KEY", "value", true},
		{"KEY=", "KEY", "", true},
		// export prefix
		{"export KEY=value", "KEY", "value", true},
		{"export  KEY=value", "KEY", "value", true},
		// double-quoted
		{`KEY="hello world"`, "KEY", "hello world", true},
		{`KEY="with \"escaped\""`, "KEY", `with "escaped"`, true},
		{`KEY="line\nnewline"`, "KEY", "line\nnewline", true},
		// single-quoted (no escape processing)
		{"KEY='hello world'", "KEY", "hello world", true},
		// inline comment stripped for unquoted
		{"KEY=value # comment", "KEY", "value", true},
		// whitespace around value
		{"KEY=  value  ", "KEY", "value", true},
		// comment lines
		{"# this is a comment", "", "", false},
		{"  # indented comment", "", "", false},
		// blank
		{"", "", "", false},
		{"   ", "", "", false},
		// no equals
		{"KEYONLY", "", "", false},
		// invalid key (contains space)
		{"KEY NAME=val", "", "", false},
		// equals at position 0
		{"=value", "", "", false},
	}

	for _, tc := range cases {
		k, v, ok := parseDotEnvLine(tc.line)
		if ok != tc.ok {
			t.Errorf("line %q: ok=%v want %v", tc.line, ok, tc.ok)
			continue
		}
		if ok {
			if k != tc.key {
				t.Errorf("line %q: key=%q want %q", tc.line, k, tc.key)
			}
			if v != tc.value {
				t.Errorf("line %q: value=%q want %q", tc.line, v, tc.value)
			}
		}
	}
}

func TestLoadDotEnv_SetsMissingVars(t *testing.T) {
	dir := t.TempDir()
	content := "CODDY_TEST_DOTENV_A=hello\nCODDY_TEST_DOTENV_B=world\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ensure vars are unset before the call.
	_ = os.Unsetenv("CODDY_TEST_DOTENV_A")
	_ = os.Unsetenv("CODDY_TEST_DOTENV_B")

	loadDotEnv(dir)

	if got := os.Getenv("CODDY_TEST_DOTENV_A"); got != "hello" {
		t.Errorf("A=%q want hello", got)
	}
	if got := os.Getenv("CODDY_TEST_DOTENV_B"); got != "world" {
		t.Errorf("B=%q want world", got)
	}

	// Cleanup.
	_ = os.Unsetenv("CODDY_TEST_DOTENV_A")
	_ = os.Unsetenv("CODDY_TEST_DOTENV_B")
}

func TestLoadDotEnv_DoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	content := "CODDY_TEST_DOTENV_C=from_file\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Setenv("CODDY_TEST_DOTENV_C", "from_env"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Unsetenv("CODDY_TEST_DOTENV_C") }()

	loadDotEnv(dir)

	if got := os.Getenv("CODDY_TEST_DOTENV_C"); got != "from_env" {
		t.Errorf("C=%q want from_env (existing env must not be overridden)", got)
	}
}

func TestLoadDotEnv_MissingFile(t *testing.T) {
	// Should not panic or error when .env does not exist.
	loadDotEnv(t.TempDir())
}

func TestLoadDotEnv_EmptyHome(t *testing.T) {
	// Should not panic when home is empty.
	loadDotEnv("")
}
