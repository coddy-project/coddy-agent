package update

import (
	"fmt"
	"io"
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
}

func newDownloadProgress(out io.Writer, name string) *consoleDownloadProgress {
	return &consoleDownloadProgress{last: -1, name: name, out: out}
}

func (p *consoleDownloadProgress) Progress(downloaded, total int64) {
	if total <= 0 {
		return
	}
	percent := int(downloaded * 100 / total)
	if percent > 100 {
		percent = 100
	}
	if percent == p.last {
		return
	}
	p.last = percent
	filled := percent / 5
	_, _ = fmt.Fprintf(p.out, "\rDownloading %s [%s%s] %3d%% (%s/%s)", p.name, repeat("#", filled), repeat("-", 20-filled), percent, formatBytes(downloaded), formatBytes(total))
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

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	result := make([]byte, len(s)*n)
	for i := 0; i < n; i++ {
		copy(result[i*len(s):], s)
	}
	return string(result)
}
