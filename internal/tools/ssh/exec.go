package ssh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	gossh "golang.org/x/crypto/ssh"
)

// SSHRunCommandTool returns the ssh_run_command built-in tool.
func SSHRunCommandTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "ssh_run_command",
			Description: "Execute a shell command on a remote host via SSH. " +
				"Returns the combined stdout and stderr output. " +
				"Authentication tries the SSH agent (SSH_AUTH_SOCK) first, then falls back to private key files in the configured .ssh directory.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"host": map[string]interface{}{
						"type":        "string",
						"description": "Remote host in the form user@hostname. Port is specified separately.",
					},
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to execute on the remote host.",
					},
					"port": map[string]interface{}{
						"type":        "integer",
						"description": "SSH port (default: 22).",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Command timeout in seconds (default: 30).",
					},
					"permission_rationale": map[string]interface{}{
						"type":        "string",
						"description": "Optional text shown in the permission dialog.",
					},
				},
				"required": []interface{}{"host", "command"},
			},
		},
		RequiresPermission: true,
		Execute:            executeSSHExec,
	}
}

type sshExecArgs struct {
	Host                string `json:"host"`
	Command             string `json:"command"`
	Port                int    `json:"port"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
	PermissionRationale string `json:"permission_rationale"`
}

func executeSSHExec(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[sshExecArgs](argsJSON)
	if err != nil {
		return "", err
	}

	port := 22
	if args.Port > 0 {
		port = args.Port
	}

	timeout := env.SSHConnectTimeout
	if timeout <= 0 {
		timeout = 30
	}
	if args.TimeoutSeconds > 0 {
		timeout = args.TimeoutSeconds
	}

	user, host := parseHostUser(args.Host, "")
	if user == "" {
		return "", fmt.Errorf("ssh: no user specified; include user@ in host")
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	sshDir := resolveSSHDir()
	mode := knownHostsModeFrom(env.PermissionMode)

	knownHostsPath := ""
	if sshDir != "" {
		knownHostsPath = filepath.Join(sshDir, "known_hosts")
	}

	hkCB, err := hostKeyCallback(mode, knownHostsPath)
	if err != nil {
		return "", err
	}

	authMethods, agentCloser := buildAuthMethods(sshDir)
	if agentCloser != nil {
		defer func() { _ = agentCloser.Close() }()
	}
	if len(authMethods) == 0 {
		return "", fmt.Errorf("ssh: no authentication methods available: "+
			"SSH agent not found (SSH_AUTH_SOCK not set) and no usable key files in %s", sshDir)
	}

	clientCfg := &gossh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hkCB,
		Timeout:         time.Duration(timeout) * time.Second,
	}

	dialCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	client, err := dialContext(dialCtx, addr, clientCfg)
	if err != nil {
		return "", fmt.Errorf("ssh: connect to %s: %w", addr, err)
	}
	defer func() { _ = client.Close() }()

	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh: open session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	var buf bytes.Buffer
	sess.Stdout = &buf
	sess.Stderr = &buf

	cmdCtx, cmdCancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cmdCancel()

	done := make(chan error, 1)
	go func() {
		done <- sess.Run(args.Command)
	}()

	select {
	case <-cmdCtx.Done():
		_ = sess.Signal(gossh.SIGTERM)
		return "", fmt.Errorf("ssh: command timed out after %d seconds", timeout)
	case runErr := <-done:
		output := buf.String()
		if runErr != nil {
			if output == "" {
				return fmt.Sprintf("command failed: %v", runErr), nil
			}
			return fmt.Sprintf("command failed: %v\n%s", runErr, output), nil
		}
		if output == "" {
			return "(no output)", nil
		}
		return output, nil
	}
}

// parseHostUser splits a "user@host" string into user and host parts.
// If no "@" is present, the defaultUser is used.
func parseHostUser(host, defaultUser string) (user, hostname string) {
	if idx := strings.Index(host, "@"); idx >= 0 {
		return host[:idx], host[idx+1:]
	}
	return defaultUser, host
}

// buildAuthMethods returns SSH auth methods and an optional closer for the agent connection.
// Priority: SSH agent (SSH_AUTH_SOCK) first, then file-based keys from dir.
// The caller must call Close() on the returned io.Closer when done (if non-nil).
func buildAuthMethods(dir string) ([]gossh.AuthMethod, io.Closer) {
	var methods []gossh.AuthMethod

	// 1. SSH agent — preferred when SSH_AUTH_SOCK is set.
	agentMethod, agentCloser, _ := agentAuth()
	if agentMethod != nil {
		methods = append(methods, agentMethod)
	}

	// 2. File-based keys — fallback when no agent or as additional source.
	if signers := LoadSigners(dir); len(signers) > 0 {
		methods = append(methods, gossh.PublicKeys(signers...))
	}

	return methods, agentCloser
}

// resolveSSHDir returns the user's ~/.ssh directory.
func resolveSSHDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh")
}

// knownHostsModeFrom maps the global permission mode to an SSH host-key verification mode.
// "bypass" → insecure (skip verification); everything else → accept_new (TOFU).
func knownHostsModeFrom(permissionMode string) string {
	if permissionMode == "bypass" {
		return "insecure"
	}
	return "accept_new"
}

// dialContext dials an SSH server and returns a client, respecting ctx cancellation.
func dialContext(ctx context.Context, addr string, cfg *gossh.ClientConfig) (*gossh.Client, error) {
	type result struct {
		client *gossh.Client
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := gossh.Dial("tcp", addr, cfg)
		ch <- result{c, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.client, r.err
	}
}
