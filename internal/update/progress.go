package update

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type downloadReporter interface {
	Complete(downloaded int64)
	Progress(downloaded, total int64)
	Retry(attempt, maxAttempts int, err error)
}

type consoleDownloadProgress struct {
	drew bool
	last int
	name string
	out  io.Writer
	// bar is false when the writer is a file or a pipe. Redrawing a bar with
	// carriage returns there produces one line per percent in the log instead
	// of one line that moves.
	bar bool
}

func newDownloadProgress(out io.Writer, name string) *consoleDownloadProgress {
	return &consoleDownloadProgress{last: -1, name: name, out: out, bar: isTerminalWriter(out)}
}

func isTerminalWriter(out io.Writer) bool {
	f, ok := out.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (p *consoleDownloadProgress) Progress(downloaded, total int64) {
	if !p.bar || total <= 0 {
		return
	}
	percent := int(downloaded * 100 / total)
	if percent > 100 {
		percent = 100
	}
	if percent == p.last {
		return
	}
	// A server that ignored the Range header restarts the transfer, and the bar
	// with it. Break the line so the shrinking bar does not overwrite what the
	// previous attempt already reported.
	if percent < p.last && p.drew {
		_, _ = fmt.Fprintln(p.out)
		p.drew = false
	}
	p.last = percent
	filled := percent / 5
	_, _ = fmt.Fprintf(p.out, "\rDownloading %s [%s%s] %3d%% (%s/%s)", p.name, strings.Repeat("#", filled), strings.Repeat("-", 20-filled), percent, formatBytes(downloaded), formatBytes(total))
	p.drew = true
}

func (p *consoleDownloadProgress) Retry(attempt, maxAttempts int, err error) {
	if p.drew {
		_, _ = fmt.Fprintln(p.out)
	}
	_, _ = fmt.Fprintf(p.out, "Download interrupted (%v); resuming, attempt %d of %d.\n", err, attempt, maxAttempts)
	p.last = -1
	p.drew = false
}

func (p *consoleDownloadProgress) Complete(downloaded int64) {
	if p.drew {
		_, _ = fmt.Fprintln(p.out)
		return
	}
	_, _ = fmt.Fprintf(p.out, "Downloaded %s.\n", formatBytes(downloaded))
}

func formatBytes(n int64) string {
	const mib = 1024 * 1024
	if n >= mib {
		return fmt.Sprintf("%.1f MiB", float64(n)/mib)
	}
	return fmt.Sprintf("%d KiB", n/1024)
}
