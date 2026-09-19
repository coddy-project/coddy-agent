//go:build cli

package cli

// The prompt of a one-shot run (issue #220): typed after -p, read from a file
// named by -i, or read from stdin, with what is piped under a typed prompt
// riding along as an attachment - the rules Codex's `exec` follows, so a
// prompt larger than the operating system's argument limit never passes
// through argv.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/term"
)

// MaxPromptInputBytes caps what one print run reads for its prompt: a prompt
// file and stdin together. Past it the run stops before anything is sent; the
// input is never cut short to fit.
const MaxPromptInputBytes = 8 << 20

// stdinWaitNotice is how long a run waits for the first byte of a pipe before
// it says on stderr what it is waiting for.
const stdinWaitNotice = 3 * time.Second

// barePrompt is the value expandBarePrompt gives a -p that came without one.
// No argument can hold a NUL byte, so it cannot be mistaken for a prompt.
const barePrompt = "\x00"

// onceString is a string flag that refuses a second value, so two spellings
// of one flag (-p and --prompt) cannot silently override each other the way
// flag.String lets the last one win.
type onceString struct {
	names string
	value string
	set   bool
}

func (o *onceString) String() string {
	if o == nil {
		return ""
	}
	return o.value
}

func (o *onceString) Set(v string) error {
	if o.set {
		return fmt.Errorf("%s is given more than once", o.names)
	}
	o.value, o.set = v, true
	return nil
}

// promptRequest is what the command line says about the prompt of a run.
type promptRequest struct {
	// text is -p/--prompt: the prompt itself, "-" for stdin, or barePrompt
	// when the flag came without a value and the prompt is read from -i or
	// from a piped stdin. An empty value ("-p \"\"") is an empty prompt.
	text onceString
	// file is -i/--prompt-file: a path, or "-" for stdin.
	file onceString
	// noStdin (--no-stdin) keeps stdin out of the run: nothing is attached
	// from it and it cannot be the prompt.
	noStdin bool
}

func newPromptRequest() *promptRequest {
	return &promptRequest{
		text: onceString{names: "-p/--prompt"},
		file: onceString{names: "-i/--prompt-file"},
	}
}

func (r *promptRequest) register(fs *flag.FlagSet) {
	const promptUsage = "run one `prompt` non-interactively, print the answer, and exit; - (or -p with no value when stdin is not a terminal) reads the prompt from stdin, and data piped under a typed prompt is attached to it"
	fs.Var(&r.text, "prompt", promptUsage)
	fs.Var(&r.text, "p", "shorthand for --`prompt`")
	const fileUsage = "run one prompt read from this `file` (- for stdin), like -p but without passing it through the command line; data piped on stdin is attached to it"
	fs.Var(&r.file, "prompt-file", fileUsage)
	fs.Var(&r.file, "i", "shorthand for --prompt-`file`")
	fs.BoolVar(&r.noStdin, "no-stdin", false, "never read stdin in a one-shot run: nothing piped is attached (for loops such as while read ...; done < list, ssh, CI runners that feed the job script on stdin)")
}

// printMode reports whether the run is a one-shot print: -p in any form, or
// -i.
func (r *promptRequest) printMode() bool { return r.text.set || r.file.set }

// expandBarePrompt lets -p stand without a value, as `claude -p` does: a -p
// that ends the arguments, or is followed by a flag this command knows, is
// given the barePrompt value before flag.Parse, which would otherwise take
// the next flag as the prompt. Everything else is left for flag.Parse, and the walk
// stops where it stops: at "--" or at the first argument that is not a flag.
func expandBarePrompt(fs *flag.FlagSet, args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || a == "-" || !strings.HasPrefix(a, "-") {
			return append(out, args[i:]...)
		}
		name, inline := flagName(a)
		f := fs.Lookup(name)
		switch {
		case f == nil || inline || isBoolFlag(f):
			out = append(out, a)
		case name == "p" || name == "prompt":
			if i+1 == len(args) || namesFlag(fs, args[i+1]) {
				out = append(out, a+"="+barePrompt)
				continue
			}
			out = append(out, a, args[i+1])
			i++
		default:
			// A flag that takes the next argument as its value.
			out = append(out, a)
			if i+1 < len(args) {
				out = append(out, args[i+1])
				i++
			}
		}
	}
	return out
}

// flagName is the name an argument spells, without its dashes or its
// "=value"; inline is true when the value came along.
func flagName(arg string) (name string, inline bool) {
	name = strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
	if i := strings.IndexByte(name, '='); i >= 0 {
		return name[:i], true
	}
	return name, false
}

func namesFlag(fs *flag.FlagSet, arg string) bool {
	// "--" ends the flags: a -p before it has no value either.
	if arg == "--" {
		return true
	}
	if arg == "-" || !strings.HasPrefix(arg, "-") {
		return false
	}
	name, _ := flagName(arg)
	// flag.Parse answers -h and -help itself without registering them.
	return name == "h" || name == "help" || (name != "" && fs.Lookup(name) != nil)
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// printInput is what a one-shot run sends: the prompt, and what was piped in
// under it when the prompt itself came from elsewhere.
type printInput struct {
	Prompt string
	Stdin  string
}

// printInputEnv is the process around readPrintInput, injected by tests.
type printInputEnv struct {
	Stdin *os.File
	// Notices receives the lines a run prints about its input (stderr).
	Notices io.Writer
	// IsTerminal reports whether a file is a terminal; nil asks the OS.
	IsTerminal func(*os.File) bool
	// Limit is the byte budget of the whole input; zero means
	// MaxPromptInputBytes.
	Limit int
	// WaitNotice is how long a pipe may stay silent before the run says it is
	// waiting; zero means stdinWaitNotice.
	WaitNotice time.Duration
}

func (e printInputEnv) isTerminal(f *os.File) bool {
	if e.IsTerminal != nil {
		return e.IsTerminal(f)
	}
	return term.IsTerminal(int(f.Fd()))
}

// readPrintInput reads the prompt of a one-shot run. It runs before the
// configuration is loaded and before a session exists, so input that cannot
// be sent - a missing file, two prompts, bytes that are not text, more than
// the limit - stops the run with nothing sent anywhere.
//
// The prompt comes from exactly one place: -p TEXT, stdin (-p -, -i -, or -p
// with no value and a pipe on stdin), or the file -i names. When it did not
// come from stdin and stdin is a pipe or a redirected file, stdin is read to
// the end and attached under the prompt. A terminal, /dev/null (any
// character device) and a socket are never attached: only a prompt asked for
// on stdin reads them - a terminal only with "-", the rest with a bare -p as
// well, since a program that spawns coddy hands it a socket to write into.
func readPrintInput(req *promptRequest, env printInputEnv) (printInput, error) {
	text, file := req.text.value, req.file.value
	bare := text == barePrompt
	inline := req.text.set && !bare && text != "-"
	stdinPrompt := text == "-" || file == "-"
	switch {
	case inline && text == "":
		return printInput{}, errors.New(`empty prompt: -p was given "" (pass the prompt after -p, or -p alone to read it from a pipe or from -i)`)
	case req.file.set && file == "":
		return printInput{}, errors.New("-i needs a path (or - to read the prompt from stdin)")
	case inline && req.file.set:
		return printInput{}, fmt.Errorf("-p gives the prompt as text and -i reads it from %s: pass one of them", inputName(file))
	case text == "-" && req.file.set:
		return printInput{}, fmt.Errorf("-p - reads the prompt from stdin and -i reads it from %s: pass one of them", inputName(file))
	case stdinPrompt && req.noStdin:
		return printInput{}, errors.New("--no-stdin leaves no prompt: - asks to read it from stdin")
	}
	budget := env.Limit
	if budget <= 0 {
		budget = MaxPromptInputBytes
	}
	r := &inputReader{env: env, limit: budget, budget: budget, noStdin: req.noStdin}

	var in printInput
	fromStdin := false
	switch {
	case inline:
		in.Prompt = text
	case file != "" && file != "-":
		prompt, isStdin, err := r.readFile(file)
		if err != nil {
			return printInput{}, err
		}
		in.Prompt = prompt
		// -i /dev/stdin is stdin under another name: it is not read again.
		fromStdin = isStdin
	default:
		// Only "-" reads a terminal: a bare -p there has nothing to read.
		if req.noStdin || env.Stdin == nil || (!stdinPrompt && env.isTerminal(env.Stdin)) {
			return printInput{}, errors.New(`no prompt: pass it after -p ("..."), pipe it on stdin, or name a file with -i`)
		}
		if env.Notices != nil && env.isTerminal(env.Stdin) {
			_, _ = fmt.Fprintf(env.Notices, "reading the prompt from the terminal; end it with %s\n", terminalEOF())
		}
		prompt, err := r.readStdin()
		if err != nil {
			return printInput{}, err
		}
		in.Prompt = prompt
		fromStdin = true
	}
	if strings.TrimSpace(in.Prompt) == "" {
		switch {
		case fromStdin:
			return printInput{}, errors.New("empty prompt: stdin held no text")
		case !inline:
			return printInput{}, fmt.Errorf("empty prompt: %s holds no text", file)
		}
		return printInput{}, errors.New("empty prompt")
	}

	if !fromStdin && !req.noStdin && env.Stdin != nil && pipedData(env.Stdin) && !env.isTerminal(env.Stdin) {
		data, err := r.readStdin()
		if err != nil {
			return printInput{}, err
		}
		// Blank input is no input: `git diff | coddy -p ...` with nothing
		// changed runs the prompt alone.
		if strings.TrimSpace(data) != "" {
			in.Stdin = data
			if env.Notices != nil {
				_, _ = fmt.Fprintf(env.Notices, "attached %s from stdin to the prompt (pass --no-stdin to leave it out)\n", humanSize(len(data)))
			}
		}
	}
	return in, nil
}

// pipedData reports whether stdin carries data someone meant for this run: a
// pipe, or a file redirected into it. A terminal, /dev/null and other
// character devices, and sockets do not count.
func pipedData(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	m := info.Mode()
	return m.IsRegular() || m&os.ModeNamedPipe != 0
}

func inputName(file string) string {
	if file == "-" {
		return "stdin"
	}
	return file
}

// inputReader reads the parts of one run's input against a shared budget.
type inputReader struct {
	env printInputEnv
	// limit is the whole budget, budget what is left of it.
	limit, budget int
	// noStdin is --no-stdin: not even a path that names stdin is read.
	noStdin bool
}

// readFile reads the prompt file; isStdin reports that the path names the
// run's own stdin (/dev/stdin, /proc/self/fd/0), which is then not attached
// a second time.
func (r *inputReader) readFile(path string) (text string, isStdin bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return "", false, fmt.Errorf("prompt file %s: %w", path, pe.Err)
		}
		return "", false, fmt.Errorf("prompt file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", false, fmt.Errorf("prompt file %s: %w", path, err)
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("prompt file %s is a directory", path)
	}
	// A device (/dev/tty, CON) is typing, not a file: -p - reads a terminal.
	if !info.Mode().IsRegular() && info.Mode()&os.ModeNamedPipe == 0 {
		return "", false, fmt.Errorf("prompt file %s is not a regular file or a FIFO (use -p - to read the prompt from a terminal)", path)
	}
	if r.env.Stdin != nil {
		if in, err := r.env.Stdin.Stat(); err == nil && os.SameFile(info, in) {
			if r.noStdin {
				return "", false, fmt.Errorf("--no-stdin leaves no prompt: %s is stdin", path)
			}
			isStdin = true
		}
	}
	// A FIFO (-i <(cmd)) can stay silent like a pipe on stdin.
	text, err = r.read(f, path, info.Mode()&os.ModeNamedPipe != 0)
	return text, isStdin, err
}

// terminalEOF names the keys that end input typed on this platform's
// terminal.
func terminalEOF() string {
	if runtime.GOOS == "windows" {
		return "ctrl+z and enter"
	}
	return "ctrl+d on an empty line"
}

func (r *inputReader) readStdin() (string, error) {
	return r.read(r.env.Stdin, "stdin", !r.env.isTerminal(r.env.Stdin) && !pipedRegular(r.env.Stdin))
}

func pipedRegular(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode().IsRegular()
}

// read reads src to its end, at most the bytes left in the budget, and
// decodes it. watch prints one line on stderr when the source stays silent,
// so a pipe nobody closes is visible instead of a run that seems to hang.
func (r *inputReader) read(src io.Reader, name string, watch bool) (string, error) {
	if watch && r.env.Notices != nil {
		first := &firstRead{r: src, done: make(chan struct{})}
		src = first
		wait := r.env.WaitNotice
		if wait <= 0 {
			wait = stdinWaitNotice
		}
		// The watcher is gone before read returns, so its line never lands
		// after the ones the caller writes next.
		var watcher sync.WaitGroup
		watcher.Add(1)
		defer watcher.Wait()
		go func() {
			defer watcher.Done()
			t := time.NewTimer(wait)
			defer t.Stop()
			select {
			case <-first.done:
			case <-t.C:
				// Data that came as the timer fired is no wait to report.
				select {
				case <-first.done:
					return
				default:
				}
				_, _ = fmt.Fprintf(r.env.Notices, "waiting for input on %s; if none is coming, pass --no-stdin or redirect it from %s\n", name, os.DevNull)
			}
		}()
	}
	data, err := io.ReadAll(io.LimitReader(src, int64(r.budget)+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	if len(data) > r.budget {
		return "", fmt.Errorf("%s: the prompt input is larger than %s, the limit of one run (a prompt file and stdin together); nothing was sent",
			name, humanSize(r.limit))
	}
	r.budget -= len(data)
	return decodePromptText(name, data)
}

// firstRead closes done once the first read returns, with data or without.
type firstRead struct {
	r    io.Reader
	once sync.Once
	done chan struct{}
}

func (f *firstRead) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	f.once.Do(func() { close(f.done) })
	return n, err
}

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// decodePromptText turns the bytes of a prompt into text. The text is taken
// as it is - line endings, trailing newlines, quotes - except for a byte
// order mark: a UTF-8 one is dropped, and UTF-16 with one is decoded, as a
// Windows editor or PowerShell may write it. Anything else must be UTF-8
// without NUL bytes; there is no guessing at legacy code pages.
func decodePromptText(name string, data []byte) (string, error) {
	// skipped is how many bytes a UTF-8 mark took, so an offset in an error
	// names the byte in the input as it was given; wide is true for decoded
	// UTF-16, whose bytes no longer line up with the input's.
	skipped, wide := 0, false
	switch {
	case bytes.HasPrefix(data, bomUTF8):
		data, skipped = data[len(bomUTF8):], len(bomUTF8)
	case bytes.HasPrefix(data, bomUTF16LE), bytes.HasPrefix(data, bomUTF16BE):
		body := data[2:]
		if len(body)%2 != 0 {
			return "", fmt.Errorf("%s starts with a UTF-16 byte order mark but has an odd number of bytes", name)
		}
		units := make([]uint16, len(body)/2)
		for i := range units {
			if data[0] == 0xFF {
				units[i] = uint16(body[2*i]) | uint16(body[2*i+1])<<8
			} else {
				units[i] = uint16(body[2*i])<<8 | uint16(body[2*i+1])
			}
		}
		data, wide = []byte(string(utf16.Decode(units))), true
	}
	if i := invalidUTF8Offset(data); i >= 0 {
		return "", fmt.Errorf("%s is not UTF-8 text (invalid byte at offset %d); convert it to UTF-8 first", name, i+skipped)
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		if wide {
			return "", fmt.Errorf("%s holds a NUL character: binary input is not a prompt", name)
		}
		return "", fmt.Errorf("%s holds a NUL byte at offset %d: binary input is not a prompt (UTF-16 text needs a byte order mark)", name, i+skipped)
	}
	return string(data), nil
}

func invalidUTF8Offset(data []byte) int {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}
	return -1
}

// humanSize writes a byte count the way the notices read it.
func humanSize(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	case n == 1:
		return "1 byte"
	}
	return fmt.Sprintf("%d bytes", n)
}
