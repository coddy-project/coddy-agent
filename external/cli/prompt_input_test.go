//go:build cli

package cli

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// promptFlags registers the console's prompt flags next to a few of its
// others, the way Run does, for the pre-pass and the parser to work on.
func promptFlags() (*flag.FlagSet, *promptRequest) {
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	req := newPromptRequest()
	req.register(fs)
	fs.String("model", "", "")
	fs.String("mode", "", "")
	fs.Bool("c", false, "")
	fs.Bool("plain", false, "")
	return fs, req
}

func TestExpandBarePromptOnlyWhenNoValueFollows(t *testing.T) {
	bare := "-p=" + barePrompt
	cases := []struct {
		args, want []string
	}{
		{[]string{"-p"}, []string{bare}},
		{[]string{"--prompt"}, []string{"--prompt=" + barePrompt}},
		{[]string{"-p", "-i", "brief.md"}, []string{bare, "-i", "brief.md"}},
		{[]string{"-p", "--mode", "ask"}, []string{bare, "--mode", "ask"}},
		{[]string{"-c", "-p"}, []string{"-c", bare}},
		{[]string{"--plain", "-p", "--model=x"}, []string{"--plain", bare, "--model=x"}},
		// flag.Parse answers -h itself; a bare -p before it is no prompt "-h".
		{[]string{"-p", "-h"}, []string{bare, "-h"}},
		// "--" ends the flags, so it is no prompt either.
		{[]string{"-p", "--"}, []string{bare, "--"}},
		// A value of its own: text, "-" for stdin, anything that is not a flag
		// this command knows.
		{[]string{"-p", "text", "--model", "x"}, []string{"-p", "text", "--model", "x"}},
		{[]string{"-p", "-"}, []string{"-p", "-"}},
		{[]string{"-p", "-not-a-flag"}, []string{"-p", "-not-a-flag"}},
		{[]string{"-p=", "-c"}, []string{"-p=", "-c"}},
		// The value of another flag is never read as a flag.
		{[]string{"--model", "-p"}, []string{"--model", "-p"}},
		// The flags end at the first argument that is not one, as for flag.Parse.
		{[]string{"stray", "-p"}, []string{"stray", "-p"}},
		{[]string{"--", "-p"}, []string{"--", "-p"}},
	}
	for _, tc := range cases {
		fs, _ := promptFlags()
		if got := expandBarePrompt(fs, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("expandBarePrompt(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestPromptFlagsRefuseARepeat(t *testing.T) {
	for _, args := range [][]string{
		{"-p", "one", "--prompt", "two"},
		{"-p", "one", "-p", "two"},
		{"-i", "a.md", "--prompt-file", "b.md"},
	} {
		fs, _ := promptFlags()
		err := fs.Parse(expandBarePrompt(fs, args))
		if err == nil || !strings.Contains(err.Error(), "more than once") {
			t.Errorf("Parse(%q) = %v, want a repeat error", args, err)
		}
	}
}

func TestPromptFlagsSetPrintMode(t *testing.T) {
	cases := []struct {
		args  []string
		print bool
	}{
		{nil, false},
		{[]string{"-c"}, false},
		{[]string{"-p", "hi"}, true},
		{[]string{"-p"}, true},
		{[]string{"-p", ""}, true},
		{[]string{"-i", "brief.md"}, true},
	}
	for _, tc := range cases {
		fs, req := promptFlags()
		if err := fs.Parse(expandBarePrompt(fs, tc.args)); err != nil {
			t.Fatalf("Parse(%q): %v", tc.args, err)
		}
		if got := req.printMode(); got != tc.print {
			t.Errorf("printMode after %q = %v, want %v", tc.args, got, tc.print)
		}
	}
}

// stdinPipe is a pipe whose writer sends data and closes, as `printf | coddy`
// does.
func stdinPipe(t *testing.T, data string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = io.WriteString(w, data)
		_ = w.Close()
	}()
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// stdinRegularFile is stdin redirected from a file, as `coddy < file` does.
func stdinRegularFile(t *testing.T, data string) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stdin.txt")
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func stdinDevNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func parsedRequest(t *testing.T, args ...string) *promptRequest {
	t.Helper()
	fs, req := promptFlags()
	if err := fs.Parse(expandBarePrompt(fs, args)); err != nil {
		t.Fatalf("Parse(%q): %v", args, err)
	}
	return req
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadPrintInputSources(t *testing.T) {
	dir := t.TempDir()
	brief := writeFile(t, dir, "brief.md", "Review «this» \"now\"\r\nwith 'quotes'\n\n")

	cases := []struct {
		name       string
		args       []string
		stdin      func(*testing.T) *os.File
		wantPrompt string
		wantStdin  string
	}{
		{"text alone", []string{"-p", "hello"}, stdinDevNull, "hello", ""},
		{"text with piped data", []string{"-p", "review"}, func(t *testing.T) *os.File { return stdinPipe(t, "diff\n+x\n") }, "review", "diff\n+x\n"},
		{"text with a redirected file", []string{"-p", "review"}, func(t *testing.T) *os.File { return stdinRegularFile(t, "log line\n") }, "review", "log line\n"},
		{"text with blank piped data", []string{"-p", "review"}, func(t *testing.T) *os.File { return stdinPipe(t, " \n\n") }, "review", ""},
		{"dash reads the prompt from stdin", []string{"-p", "-"}, func(t *testing.T) *os.File { return stdinPipe(t, "brief\n\n") }, "brief\n\n", ""},
		{"bare -p reads a piped prompt", []string{"-p"}, func(t *testing.T) *os.File { return stdinPipe(t, "piped brief\n") }, "piped brief\n", ""},
		{"--no-stdin leaves piped data out", []string{"-p", "review", "--no-stdin"}, func(t *testing.T) *os.File { return stdinPipe(t, "not for this run\n") }, "review", ""},
		{"--no-stdin with a prompt file", []string{"-i", brief, "--no-stdin"}, func(t *testing.T) *os.File { return stdinRegularFile(t, "the rest of a loop's list\n") }, "Review «this» \"now\"\r\nwith 'quotes'\n\n", ""},
		{"-i names the prompt file", []string{"-p", "-i", brief}, stdinDevNull, "Review «this» \"now\"\r\nwith 'quotes'\n\n", ""},
		{"-i alone", []string{"-i", brief}, stdinDevNull, "Review «this» \"now\"\r\nwith 'quotes'\n\n", ""},
		{"-i with piped data", []string{"-i", brief}, func(t *testing.T) *os.File { return stdinPipe(t, "data\n") }, "Review «this» \"now\"\r\nwith 'quotes'\n\n", "data\n"},
		{"-i - reads the prompt from stdin", []string{"-p", "-i", "-"}, func(t *testing.T) *os.File { return stdinPipe(t, "brief\n") }, "brief\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var notices bytes.Buffer
			in, err := readPrintInput(parsedRequest(t, tc.args...), printInputEnv{
				Stdin:      tc.stdin(t),
				Notices:    &notices,
				IsTerminal: func(*os.File) bool { return false },
				Limit:      MaxPromptInputBytes,
			})
			if err != nil {
				t.Fatalf("readPrintInput: %v", err)
			}
			if in.Prompt != tc.wantPrompt {
				t.Errorf("prompt = %q, want %q", in.Prompt, tc.wantPrompt)
			}
			if in.Stdin != tc.wantStdin {
				t.Errorf("stdin = %q, want %q", in.Stdin, tc.wantStdin)
			}
			if tc.wantStdin != "" && !strings.Contains(notices.String(), "attached") {
				t.Errorf("attaching stdin must say so on stderr, got %q", notices.String())
			}
			if tc.wantStdin == "" && notices.Len() != 0 {
				t.Errorf("nothing attached, yet stderr says %q", notices.String())
			}
		})
	}
}

func TestReadPrintInputRefusals(t *testing.T) {
	dir := t.TempDir()
	brief := writeFile(t, dir, "brief.md", "brief\n")
	empty := writeFile(t, dir, "empty.md", " \n")
	bomOnly := writeFile(t, dir, "bom.md", "\xEF\xBB\xBF\r\n")
	cases := []struct {
		name     string
		args     []string
		stdin    func(*testing.T) *os.File
		terminal bool
		want     string
	}{
		{"an empty -p", []string{"-p", ""}, func(t *testing.T) *os.File { return stdinPipe(t, "must not become the prompt\n") }, false, "empty prompt"},
		{"an empty -p with a file", []string{"-p", "", "-i", brief}, stdinDevNull, false, "empty prompt"},
		{"text and a file", []string{"-p", "text", "-i", brief}, stdinDevNull, false, "pass one of them"},
		{"stdin with --no-stdin", []string{"-p", "-", "--no-stdin"}, stdinDevNull, false, "--no-stdin"},
		{"bare -p with --no-stdin", []string{"-p", "--no-stdin"}, func(t *testing.T) *os.File { return stdinPipe(t, "x\n") }, false, "no prompt"},
		{"stdin and a file", []string{"-p", "-", "-i", brief}, stdinDevNull, false, "pass one of them"},
		{"stdin twice", []string{"-p", "-", "-i", "-"}, stdinDevNull, false, "pass one of them"},
		{"an empty -i", []string{"-i", ""}, stdinDevNull, false, "-i needs a path"},
		{"bare -p on a terminal", []string{"-p"}, stdinDevNull, true, "no prompt"},
		{"a missing file", []string{"-i", filepath.Join(dir, "missing.md")}, stdinDevNull, false, "missing.md"},
		{"a directory", []string{"-i", dir}, stdinDevNull, false, "is a directory"},
		{"an empty file", []string{"-i", empty}, stdinDevNull, false, "empty prompt"},
		{"a file holding only a BOM", []string{"-i", bomOnly}, stdinDevNull, false, "empty prompt"},
		{"empty stdin", []string{"-p", "-"}, stdinDevNull, false, "empty prompt"},
		{"invalid UTF-8 on stdin", []string{"-p", "-"}, func(t *testing.T) *os.File { return stdinPipe(t, "ok\xff\xfe") }, false, "offset 2"},
		{"invalid UTF-8 under a text prompt", []string{"-p", "review"}, func(t *testing.T) *os.File { return stdinPipe(t, "ab\xc3") }, false, "not UTF-8"},
		{"a NUL byte", []string{"-p", "-"}, func(t *testing.T) *os.File { return stdinPipe(t, "a\x00b") }, false, "NUL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readPrintInput(parsedRequest(t, tc.args...), printInputEnv{
				Stdin:      tc.stdin(t),
				Notices:    io.Discard,
				IsTerminal: func(*os.File) bool { return tc.terminal },
				Limit:      MaxPromptInputBytes,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// The prompt a terminal gives is typed, not piped: it is never attached, and
// only an explicit "-" reads it.
func TestReadPrintInputLeavesATerminalAlone(t *testing.T) {
	in, err := readPrintInput(parsedRequest(t, "-p", "hello"), printInputEnv{
		Stdin:      stdinPipe(t, "must not be read"),
		Notices:    io.Discard,
		IsTerminal: func(*os.File) bool { return true },
		Limit:      MaxPromptInputBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Stdin != "" {
		t.Fatalf("a terminal was read as piped data: %q", in.Stdin)
	}
	var notices bytes.Buffer
	in, err = readPrintInput(parsedRequest(t, "-p", "-"), printInputEnv{
		Stdin:      stdinPipe(t, "typed until ctrl+d\n"),
		Notices:    &notices,
		IsTerminal: func(*os.File) bool { return true },
		Limit:      MaxPromptInputBytes,
	})
	if err != nil || in.Prompt != "typed until ctrl+d\n" {
		t.Fatalf("-p - on a terminal = %q, %v", in.Prompt, err)
	}
	// A terminal waiting for typing says so, instead of looking hung.
	if !strings.Contains(notices.String(), "reading the prompt from the terminal") {
		t.Fatalf("stderr = %q", notices.String())
	}
}

// -i naming the run's own stdin (/dev/stdin < brief.md) is one source, not a
// prompt plus the same bytes attached again.
func TestReadPrintInputPromptFileThatIsStdin(t *testing.T) {
	path := writeFile(t, t.TempDir(), "brief.md", "the brief, once\n")
	stdin, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	var notices bytes.Buffer
	in, err := readPrintInput(parsedRequest(t, "-i", path), printInputEnv{
		Stdin: stdin, Notices: &notices, IsTerminal: func(*os.File) bool { return false }, Limit: MaxPromptInputBytes,
	})
	if err != nil || in.Prompt != "the brief, once\n" || in.Stdin != "" {
		t.Fatalf("prompt %q, stdin %q, err %v", in.Prompt, in.Stdin, err)
	}
	if notices.Len() != 0 {
		t.Fatalf("stderr claims an attachment: %q", notices.String())
	}

	// --no-stdin holds for stdin under another name too.
	if _, err := stdin.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	_, err = readPrintInput(parsedRequest(t, "-i", path, "--no-stdin"), printInputEnv{
		Stdin: stdin, Notices: io.Discard, IsTerminal: func(*os.File) bool { return false }, Limit: MaxPromptInputBytes,
	})
	if err == nil || !strings.Contains(err.Error(), "--no-stdin") {
		t.Fatalf("-i naming stdin under --no-stdin: %v", err)
	}
}

// -i reads files: a device such as the null device or a terminal is refused
// instead of being read as one.
func TestReadPrintInputPromptFileMustBeAFile(t *testing.T) {
	_, err := readPrintInput(parsedRequest(t, "-i", os.DevNull), printInputEnv{
		Stdin: stdinDevNull(t), Notices: io.Discard, IsTerminal: func(*os.File) bool { return false }, Limit: MaxPromptInputBytes,
	})
	if err == nil || !strings.Contains(err.Error(), "not a regular file or a FIFO") {
		t.Fatalf("-i %s: %v", os.DevNull, err)
	}
}

func TestReadPrintInputFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "real.md", "through the link\n")
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	in, err := readPrintInput(parsedRequest(t, "-i", link), printInputEnv{
		Stdin: stdinDevNull(t), Notices: io.Discard, IsTerminal: func(*os.File) bool { return false }, Limit: MaxPromptInputBytes,
	})
	if err != nil || in.Prompt != "through the link\n" {
		t.Fatalf("prompt = %q, %v", in.Prompt, err)
	}
}

// A prompt larger than the per-argument limit of the OS (128 KiB on Linux)
// arrives whole, byte for byte.
func TestReadPrintInputCarriesALargePromptWhole(t *testing.T) {
	var b strings.Builder
	for b.Len() < 200<<10 {
		b.WriteString("строка «brief» with \"quotes\" and 'more'\r\n")
	}
	b.WriteString("\n\n")
	body := b.String()
	path := writeFile(t, t.TempDir(), "brief.md", body)
	in, err := readPrintInput(parsedRequest(t, "-p", "-i", path), printInputEnv{
		Stdin: stdinDevNull(t), Notices: io.Discard, IsTerminal: func(*os.File) bool { return false }, Limit: MaxPromptInputBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Prompt != body {
		t.Fatalf("the prompt changed on the way: %d bytes in, %d bytes out", len(body), len(in.Prompt))
	}
}

// The limit covers what one run reads, the prompt file and stdin together,
// and is never met by cutting the input short.
func TestReadPrintInputLimit(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "brief.md", strings.Repeat("a", 60))
	env := func(stdin *os.File) printInputEnv {
		return printInputEnv{Stdin: stdin, Notices: io.Discard, IsTerminal: func(*os.File) bool { return false }, Limit: 100}
	}

	if in, err := readPrintInput(parsedRequest(t, "-i", path), env(stdinPipe(t, strings.Repeat("b", 40)))); err != nil || len(in.Stdin) != 40 {
		t.Fatalf("exactly at the limit: stdin %d bytes, err %v", len(in.Stdin), err)
	}
	_, err := readPrintInput(parsedRequest(t, "-i", path), env(stdinPipe(t, strings.Repeat("b", 41))))
	if err == nil || !strings.Contains(err.Error(), "limit") || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("one byte over, err = %v", err)
	}
	_, err = readPrintInput(parsedRequest(t, "-p", "-"), env(stdinPipe(t, strings.Repeat("b", 101))))
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("a prompt over the limit, err = %v", err)
	}
}

func TestDecodePromptText(t *testing.T) {
	utf16LE := func(s string, bom bool) []byte {
		var out []byte
		if bom {
			out = append(out, 0xFF, 0xFE)
		}
		for _, u := range utf16.Encode([]rune(s)) {
			out = append(out, byte(u), byte(u>>8))
		}
		return out
	}
	utf16BE := func(s string) []byte {
		out := []byte{0xFE, 0xFF}
		for _, u := range utf16.Encode([]rune(s)) {
			out = append(out, byte(u>>8), byte(u))
		}
		return out
	}
	ok := []struct {
		name string
		in   []byte
		want string
	}{
		{"plain", []byte("line\r\nnext\n\n"), "line\r\nnext\n\n"},
		{"UTF-8 BOM dropped", append([]byte{0xEF, 0xBB, 0xBF}, "привет\n"...), "привет\n"},
		{"UTF-16LE with BOM", utf16LE("héllo 😀\r\n", true), "héllo 😀\r\n"},
		{"UTF-16BE with BOM", utf16BE("héllo\n"), "héllo\n"},
		{"BOM only", []byte{0xEF, 0xBB, 0xBF}, ""},
	}
	for _, tc := range ok {
		got, err := decodePromptText("stdin", tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	bad := []struct {
		name string
		in   []byte
		want string
	}{
		{"invalid UTF-8", []byte("abc\xffdef"), "offset 3"},
		{"invalid UTF-8 after a BOM", append([]byte{0xEF, 0xBB, 0xBF}, "ab\xff"...), "offset 5"},
		{"UTF-16 without BOM", utf16LE("hi", false), "NUL"},
		{"odd UTF-16", append(utf16LE("hi", true), 'x'), "UTF-16"},
		{"NUL", []byte("a\x00"), "offset 1"},
	}
	for _, tc := range bad {
		if _, err := decodePromptText("stdin", tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

// A pipe that stays open without a byte gets one line on stderr saying what
// the run waits for, and the run keeps waiting: data that comes late is
// still attached.
func TestReadPrintInputSaysWhatItWaitsFor(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	notices := &syncBuffer{}
	done := make(chan struct{})
	var in printInput
	var readErr error
	req := parsedRequest(t, "-p", "review")
	go func() {
		defer close(done)
		in, readErr = readPrintInput(req, printInputEnv{
			Stdin: r, Notices: notices, IsTerminal: func(*os.File) bool { return false },
			Limit: MaxPromptInputBytes, WaitNotice: 20 * time.Millisecond,
		})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(notices.String(), "waiting for input on stdin") {
		if time.Now().After(deadline) {
			t.Fatalf("no waiting notice, stderr = %q", notices.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, _ = io.WriteString(w, "late data\n")
	_ = w.Close()
	<-done
	if readErr != nil || in.Stdin != "late data\n" {
		t.Fatalf("stdin = %q, err = %v", in.Stdin, readErr)
	}
}
