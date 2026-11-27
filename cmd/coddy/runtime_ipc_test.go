package main

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func TestRuntimeOwnerClaimSkipsWindowsService(t *testing.T) {
	t.Setenv(serve.EnvRole, serve.RoleService)
	if shouldClaimRuntimeOwner(true) {
		t.Fatal("Windows service claimed the interactive desktop IPC")
	}
	t.Setenv(serve.EnvRole, serve.RoleWorker)
	if !shouldClaimRuntimeOwner(true) {
		t.Fatal("daemon worker lost the interactive desktop IPC")
	}
	if shouldClaimRuntimeOwner(false) {
		t.Fatal("build without HTTP claimed local IPC")
	}
}
