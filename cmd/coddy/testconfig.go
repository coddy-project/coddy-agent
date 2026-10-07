package main

import (
	"io"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// configTestOutput is where -t / --test-config prints its report. A variable
// so the harness can read what the operator would see.
var configTestOutput io.Writer = os.Stdout

// runConfigTest checks the config file the flags select and returns an error
// when it has problems, so the process exits non-zero. Every entrypoint that
// takes the flag - the console, acp, serve - ends up here, so the report reads
// the same whichever command printed it.
func runConfigTest(cli config.CLIPaths) error {
	return runConfigTestWith(cli, config.ExtraTokens{})
}

// runConfigTestWith is runConfigTest for a command that holds credentials the
// file does not show - the values of --auth-token, --swarm-auth-token and
// --swarm-pairing-token on `coddy serve` - so that the rules that compare token
// classes and ask for a credential in front of shared models see what a start
// would see. The environment is read by the check itself.
func runConfigTestWith(cli config.CLIPaths, extra config.ExtraTokens) error {
	return config.RunCheckWith(configTestOutput, cli, extra)
}
