package ssh

import (
	"testing"
)

func TestParseHostUser(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		defaultUser string
		wantUser    string
		wantHost    string
	}{
		{
			name:     "user@host extracted",
			host:     "alice@example.com",
			wantUser: "alice",
			wantHost: "example.com",
		},
		{
			name:        "bare host uses default user",
			host:        "example.com",
			defaultUser: "ubuntu",
			wantUser:    "ubuntu",
			wantHost:    "example.com",
		},
		{
			name:     "bare host with no default user",
			host:     "example.com",
			wantUser: "",
			wantHost: "example.com",
		},
		{
			name:     "user@host with port-style addr",
			host:     "root@192.168.1.1",
			wantUser: "root",
			wantHost: "192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUser, gotHost := parseHostUser(tt.host, tt.defaultUser)
			if gotUser != tt.wantUser {
				t.Errorf("user = %q, want %q", gotUser, tt.wantUser)
			}
			if gotHost != tt.wantHost {
				t.Errorf("host = %q, want %q", gotHost, tt.wantHost)
			}
		})
	}
}

func TestKnownHostsModeFrom(t *testing.T) {
	tests := []struct {
		perm string
		want string
	}{
		{"bypass", "insecure"},
		{"ask", "accept_new"},
		{"accept_edits", "accept_new"},
		{"", "accept_new"},
	}
	for _, tt := range tests {
		t.Run(tt.perm, func(t *testing.T) {
			got := knownHostsModeFrom(tt.perm)
			if got != tt.want {
				t.Errorf("knownHostsModeFrom(%q) = %q, want %q", tt.perm, got, tt.want)
			}
		})
	}
}

func TestSSHRunCommandToolDefinition(t *testing.T) {
	tool := SSHRunCommandTool()
	if tool == nil {
		t.Fatal("SSHRunCommandTool() returned nil")
		return
	}
	if tool.Definition.Name != "ssh_run_command" {
		t.Errorf("tool name = %q, want %q", tool.Definition.Name, "ssh_run_command")
	}
	if !tool.RequiresPermission {
		t.Error("ssh_run_command must require permission")
	}
	schema, ok := tool.Definition.InputSchema.(map[string]interface{})
	if !ok {
		t.Fatal("InputSchema is not map[string]interface{}")
	}
	props, _ := schema["properties"].(map[string]interface{})
	for _, required := range []string{"host", "command"} {
		if _, exists := props[required]; !exists {
			t.Errorf("InputSchema missing required property %q", required)
		}
	}
}
