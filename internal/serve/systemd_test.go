package serve

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSetupUserServiceEnablesAndChecksStatus(t *testing.T) {
	var calls [][]string
	var output bytes.Buffer
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		if len(args) > 1 && args[1] == "status" {
			return []byte("● coddy.service - active (running)\n"), nil
		}
		return nil, nil
	}
	if err := setupUserService(run, &output); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", "coddy.service"},
		{"--user", "status", "--no-pager", "coddy.service"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("systemctl calls = %v, want %v", calls, want)
	}
	if !strings.Contains(output.String(), "active (running)") {
		t.Fatalf("status was not shown: %q", output.String())
	}
}

func TestSetupUserServiceStopsOnSystemctlFailure(t *testing.T) {
	var calls [][]string
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[1] == "enable" {
			return []byte("Unit coddy.service not found"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if err := setupUserService(run, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "Unit coddy.service not found") {
		t.Fatalf("error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("systemctl calls after failure = %v", calls)
	}
}
