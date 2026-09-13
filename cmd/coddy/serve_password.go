package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

// minPasswordLength is the shortest password this command will write. It is not
// a policy, it is a typo guard: a three-character password on a machine that is
// on a network is almost always a slip of the hand.
const minPasswordLength = 8

// runServeSetPassword implements `coddy serve set-password`: it writes the web
// sign-in account into config.yaml.
//
// The hash is written here rather than typed by the operator for two reasons.
// One, nobody should have to run an argon2 tool to close their own agent. Two,
// every "$" of a hash has to be doubled to survive this file's environment
// expansion, and a hash pasted in by hand comes back as rubble on the next
// load - which is exactly the kind of failure that ends with the password
// being blamed.
func runServeSetPassword(args []string) error {
	fs := flag.NewFlagSet("serve set-password", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	user := fs.String("user", "", "account name for the sign-in form (default: the one already configured, else $USER)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(),
			"Usage of serve set-password (write the web UI sign-in account into config.yaml):\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cli := config.CLIPaths{Home: strings.TrimSpace(*homeDir), Config: strings.TrimSpace(*cfgPath)}
	paths, err := config.Resolve(cli)
	if err != nil {
		return err
	}
	// The current file decides the default account name, and a file that does
	// not load yet is not a reason to refuse: setting a password is often the
	// first thing done to a fresh install.
	account := strings.TrimSpace(*user)
	if account == "" {
		if cfg, err := config.LoadWithPaths(paths); err == nil {
			account = cfg.HTTPServer.Login.User
		}
	}
	if account == "" {
		account = defaultAccountName()
	}
	if err := checkAccountName(account); err != nil {
		return err
	}

	password, err := readNewPassword(os.Stdin, os.Stderr)
	if err != nil {
		return err
	}
	hash, err := webauth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// A surgical edit rather than a rewrite: the operator's comments, key order
	// and their own schema modeline all stay exactly where they were, and the
	// previous file is kept as a snapshot the same way every other config
	// commit keeps one.
	cmds := []config.UCICommand{
		{Op: config.UCIOpSet, Path: "httpserver.login.enable", Value: "true"},
		{Op: config.UCIOpSet, Path: "httpserver.login.user", Value: account},
		{Op: config.UCIOpSet, Path: "httpserver.login.password_hash", Value: escapeConfigDollar(hash)},
	}
	result, err := config.CommitUCICommands(paths, cmds)
	if err != nil {
		return fmt.Errorf("write %s: %w", paths.ConfigPath, err)
	}

	fmt.Printf("web sign-in is on for %q\n  config    %s\n  previous  %s\n", account, paths.ConfigPath, result.SnapshotPath)
	fmt.Printf("  restart `coddy serve` (or wait for it to pick the file up) and sign in at the web UI\n")
	fmt.Printf("note: the form is for browsers. API clients (coddy --remote, coddy acp --remote,\n" +
		"      a swarm relay reaching this node, scripts) still present a bearer token:\n" +
		"      set httpserver.auth_token, --auth-token or " + httpserver.TokenEnvVar + " for them.\n")
	return nil
}

// checkAccountName refuses a name this file cannot carry.
//
// `login.user` is one of the values that may be an environment reference
// (`user: "${CODDY_HTTP_USER}"`), so it is written literally rather than
// escaped - which means a "$" inside a name would be read as a reference on the
// next load and the account would quietly change. Saying so here is better than
// writing a name that stops working at the next restart.
func checkAccountName(name string) error {
	if strings.ContainsAny(name, "$") {
		return fmt.Errorf("the account name %q contains a %q, which config.yaml reads as an environment reference; pick a name without one", name, "$")
	}
	if strings.ContainsAny(name, "\n\r\t") {
		return fmt.Errorf("the account name %q contains whitespace that config.yaml cannot carry", name)
	}
	return nil
}

// escapeConfigDollar doubles every "$" so the value survives the load-time
// environment expansion as a literal. It mirrors what the config package does
// for the secrets it writes itself.
func escapeConfigDollar(s string) string { return strings.ReplaceAll(s, "$", "$$") }

// defaultAccountName is the operator's own login name when nothing else says
// otherwise, so the common case is one prompt and no flags.
func defaultAccountName() string {
	for _, v := range []string{os.Getenv("USER"), os.Getenv("USERNAME")} {
		if name := strings.TrimSpace(v); name != "" {
			return name
		}
	}
	return "admin"
}

// readNewPassword asks for the password twice on a terminal and reads one line
// when standard input is a pipe, so the command works in a shell script and in
// a container's entrypoint as well as by hand.
func readNewPassword(in *os.File, out io.Writer) (string, error) {
	if !term.IsTerminal(int(in.Fd())) {
		reader := bufio.NewReader(in)
		line, err := reader.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		return validatedPassword(strings.TrimRight(line, "\r\n"))
	}
	_, _ = fmt.Fprint(out, "New web UI password: ")
	first, err := term.ReadPassword(int(in.Fd()))
	_, _ = fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	_, _ = fmt.Fprint(out, "Repeat it: ")
	second, err := term.ReadPassword(int(in.Fd()))
	_, _ = fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if string(first) != string(second) {
		return "", errors.New("the two passwords do not match, nothing was written")
	}
	return validatedPassword(string(first))
}

func validatedPassword(s string) (string, error) {
	if strings.TrimSpace(s) == "" {
		return "", errors.New("the password is empty, nothing was written")
	}
	if len(s) < minPasswordLength {
		return "", fmt.Errorf("the password is shorter than %d characters, nothing was written", minPasswordLength)
	}
	return s, nil
}
