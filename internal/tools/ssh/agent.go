package ssh

import (
	"context"
	"io"
	"net"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentAuth attempts to connect to the SSH agent via SSH_AUTH_SOCK.
// Returns an auth method, a closer for the connection, and an error.
// All three are nil when the agent is unavailable (socket not set or dial fails).
func agentAuth() (gossh.AuthMethod, io.Closer, error) {
	return agentAuthContext(context.Background())
}

func agentAuthContext(ctx context.Context) (gossh.AuthMethod, io.Closer, error) {
	sock := environmentValue(platform.UserEnvironment(ctx, ""), "SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		// Agent socket configured but unreachable — not fatal, fall back to file keys.
		return nil, nil, nil
	}
	ag := agent.NewClient(conn)
	return gossh.PublicKeysCallback(ag.Signers), conn, nil
}

func environmentValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
