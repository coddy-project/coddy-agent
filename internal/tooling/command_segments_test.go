package tooling

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Issue #646: an entry covers its own command with any arguments, never a
// second command written after it. Every refusal below was allowed by the
// bare prefix match, since each command starts with an entry and a space.
func TestCommandAllowedOnAPOSIXShellMatchesEveryCommandOfTheLine(t *testing.T) {
	env := &Env{
		Shell:            platform.ShellBash,
		CommandAllowlist: []string{"git status", "go test", "tail", "echo"},
	}
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"one command", "git status --short", true},
		{"two allowed commands chained", "git status && go test ./...", true},
		{"allowed commands piped", "go test ./... | tail -n 20", true},
		{"allowed commands in a list", "git status; go test", true},
		{"allowed commands on two lines", "git status\ngo test ./...", true},
		{"an operator in single quotes", "echo 'a; rm -rf ~'", true},
		{"an operator in double quotes", `echo "a && rm -rf ~"`, true},
		{"an escaped operator", `echo a\;rm`, true},
		{"a quote inside double quotes", `echo "it's"`, true},
		{"an escaped dollar in double quotes", `echo "\$(rm -rf ~)"`, true},
		{"a line continuation", "git status \\\n--short", true},
		{"a hash inside a word", "echo a#b", true},
		{"a variable", "go test $PKG", true},

		{"and", "git status && rm -rf ~", false},
		{"or", "git status || rm -rf ~", false},
		{"list", "git status; rm -rf ~", false},
		{"list without a space", "git status -s;rm -rf ~", false},
		{"background", "git status & rm -rf ~", false},
		{"pipe", "git status | sh", false},
		{"pipe with stderr", "git status |& sh", false},
		{"newline", "git status -s\nrm -rf ~", false},
		{"command substitution", "echo $(rm -rf ~)", false},
		{"arithmetic", "echo $((1+1))", false},
		{"backticks", "echo `rm -rf ~`", false},
		{"command substitution in double quotes", `echo "$(rm -rf ~)"`, false},
		{"backticks in double quotes", "echo \"`rm -rf ~`\"", false},
		{"output redirection", "git status > ~/.bashrc", false},
		{"append redirection", "echo x >> ~/.bashrc", false},
		{"stderr duplication", "go test ./... 2>&1", false},
		{"input redirection", "tail < /etc/passwd", false},
		{"here document", "tail <<EOF\nx\nEOF", false},
		{"process substitution", "tail <(rm -rf ~)", false},
		{"subshell", "(rm -rf ~)", false},
		{"subshell after an allowed command", "git status && (rm -rf ~)", false},
		// The quote in the comment would otherwise hide the next line, which
		// the shell runs, inside a quoted string.
		{"comment hiding a line", "git status #'\nrm -rf ~\n#'", false},
		// $'\'' is one quote character to bash; a reader that ends the string
		// at the escaped quote would see the next line as quoted.
		{"ANSI-C string hiding a line", "git status $'\\''\nrm -rf ~\n'", false},
		{"unterminated quote", "echo 'a", false},
		{"only operators", "; &&", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := env.CommandAllowed(tc.command); got != tc.want {
				t.Fatalf("CommandAllowed(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

// PowerShell and cmd.exe quote and escape unlike a POSIX shell, so there a
// prefix covers a command with no metacharacter at all.
func TestCommandAllowedOnWindowsShellsCoversOnlyAPlainCommand(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"one command", "git status --short", true},
		// A backslash escapes nothing in PowerShell, so the ; runs Remove-Item.
		{"a backslash before an operator", `git status \; Remove-Item -Recurse ~`, false},
		// cmd.exe has no single quotes, so the & runs calc.
		{"an operator in single quotes", "git status '&' calc", false},
		{"two allowed commands chained", "git status && go test ./...", false},
		{"a variable of cmd.exe", "go test %PKG%", false},
		{"a subexpression of PowerShell", "go test $(Remove-Item ~)", false},
	}
	for _, shell := range []platform.ShellKind{platform.ShellPwsh, platform.ShellPowerShell, platform.ShellCmd} {
		env := &Env{Shell: shell, CommandAllowlist: []string{"git status", "go test"}}
		for _, tc := range cases {
			t.Run(string(shell)+"/"+tc.name, func(t *testing.T) {
				if got := env.CommandAllowed(tc.command); got != tc.want {
					t.Fatalf("CommandAllowed(%q) = %v, want %v", tc.command, got, tc.want)
				}
			})
		}
	}

	// The same backslash protects the operator on bash, where the line is one
	// git status with an argument.
	posix := &Env{Shell: platform.ShellBash, CommandAllowlist: []string{"git status"}}
	if !posix.CommandAllowed(`git status \; Remove-Item -Recurse ~`) {
		t.Fatal("an escaped operator must stay an argument on bash")
	}
}

// A literal entry and "*" keep covering a whole line, whatever it holds.
func TestCommandAllowedKeepsWholeLineEntries(t *testing.T) {
	for _, shell := range []platform.ShellKind{platform.ShellBash, platform.ShellPwsh} {
		literal := &Env{Shell: shell, CommandAllowlist: []string{"make build && make test"}}
		if !literal.CommandAllowed("make build && make test") {
			t.Fatalf("%s: a line equal to an entry must be allowed", shell)
		}
		if literal.CommandAllowed("make build && make test && rm -rf ~") {
			t.Fatalf("%s: a whole-line entry must not cover a line that adds a command", shell)
		}
		all := &Env{Shell: shell, CommandAllowlist: []string{"*"}}
		if !all.CommandAllowed("git status && rm -rf ~ > /dev/null") {
			t.Fatalf("%s: * must allow every command", shell)
		}
	}
}
