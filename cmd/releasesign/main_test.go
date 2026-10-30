package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/internal/minisig"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestSignRejectsAnUntrustedKey(t *testing.T) {
	secret, _, err := minisig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "SHA256SUMS")
	if err := os.WriteFile(path, []byte("the digest list"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sign("2.0.0", path, secret); err == nil {
		t.Fatal("signed a release with a key the updater does not trust")
	}
	if _, err := os.Stat(path + ".minisig"); !os.IsNotExist(err) {
		t.Fatalf("an untrusted signature was published: %v", err)
	}
}

func TestReleaseWorkflowRequiresSigning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the release job runs bash on Linux")
	}
	data, err := os.ReadFile("../../.github/workflows/release-binaries.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	signIndex, uploadIndex := -1, -1
	for i, step := range workflow.Jobs["release-binaries"].Steps {
		switch step.Name {
		case "Sign the digest list":
			script, signIndex = step.Run, i
		case "Upload assets to GitHub Release":
			uploadIndex = i
		}
	}
	if script == "" || uploadIndex < signIndex || signIndex < 0 || uploadIndex < 0 {
		t.Fatal("release assets must be signed before they are uploaded")
	}
	for _, tc := range []struct {
		name, repo, secret string
		wantError          bool
		wantSigning        bool
	}{
		{"official release without a key", "coddy-project/coddy-agent", "", true, false},
		{"fork without a key", "someone/fork", "", false, false},
		{"signing failure", "coddy-project/coddy-agent", "test-key", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "signed")
			if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\ntouch \"$SIGNING_MARKER\"\nexit 17\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "RELEASE_SIGNING_KEY="+tc.secret, "GITHUB_REPOSITORY="+tc.repo, "TAG=2.0.0", "SIGNING_MARKER="+marker)
			platform.AdaptCommand(cmd)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantError {
				t.Fatalf("workflow error = %v, want error %v: %s", err, tc.wantError, output)
			}
			_, err = os.Stat(marker)
			if (err == nil) != tc.wantSigning {
				t.Fatalf("signing invoked = %v, want %v: %s", err == nil, tc.wantSigning, output)
			}
			if tc.wantError && !tc.wantSigning && !strings.Contains(string(output), "RELEASE_SIGNING_KEY") {
				t.Fatalf("missing key error does not name the secret: %s", output)
			}
		})
	}
}
