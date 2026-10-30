// Command modelsgen refreshes internal/llm/models/models.json from
// models.dev: the id, window, max_tokens and image support of every model of
// the providers Coddy has (OpenAI, Anthropic, OpenCode), nothing else. It
// writes the file only when those values changed and says which models moved.
//
//	go run ./cmd/modelsgen          # refresh (make models-catalog)
//	go run ./cmd/modelsgen -check   # exit 1 when the copy is behind (make models-catalog-check)
//
// The commit gate (scripts/checks.sh) runs the check with -unreachable-ok and a
// short -timeout, so a commit does not depend on models.dev answering.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
)

// maxCatalogBytes bounds the download: api.json is about 6 MB.
const maxCatalogBytes = 64 << 20

// The exit statuses of a run past 0: up to date, refreshed, or not checked.
const (
	exitBehind = 1
	exitFailed = 2
)

type options struct {
	check         bool
	unreachableOK bool
	source        string
	out           string
	timeout       time.Duration
}

func main() {
	var o options
	flag.BoolVar(&o.check, "check", false, "only report whether models.json is behind the catalog; exit 1 when it is")
	flag.BoolVar(&o.unreachableOK, "unreachable-ok", false, "with -check, a catalog that cannot be read is a warning and exit 0, not a failure")
	flag.StringVar(&o.source, "source", models.SourceURL, "the catalog: an https address or a local api.json")
	flag.StringVar(&o.out, "out", models.Path, "models.json to compare with and write")
	flag.DurationVar(&o.timeout, "timeout", 2*time.Minute, "how long the download may take")
	flag.Parse()
	os.Exit(run(o, os.Stdout, os.Stderr))
}

func run(o options, stdout, stderr io.Writer) int {
	fresh, err := fetch(o.source, o.timeout)
	if err != nil {
		if o.check && o.unreachableOK {
			_, _ = fmt.Fprintf(stderr, "modelsgen: the catalog was not checked: %v\n", err)
			return 0
		}
		return fail(stderr, err)
	}
	current, err := os.ReadFile(o.out)
	if err != nil && !os.IsNotExist(err) {
		return fail(stderr, err)
	}
	if bytes.Equal(current, fresh) {
		_, _ = fmt.Fprintf(stdout, "modelsgen: %s is up to date\n", o.out)
		return 0
	}
	changes, err := models.Changes(current, fresh)
	if err != nil {
		// A file a merge left conflict markers in is what a refresh repairs.
		changes = fmt.Sprintf("%v; a refresh writes it anew\n", err)
	}
	_, _ = fmt.Fprint(stdout, "modelsgen: ", changes)
	if o.check {
		_, _ = fmt.Fprintln(stderr, "modelsgen: run make models-catalog")
		return exitBehind
	}
	if err := os.WriteFile(o.out, fresh, 0o644); err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintf(stdout, "modelsgen: wrote %s\n", o.out)
	return 0
}

// fetch reads the catalog and turns it into models.json.
func fetch(source string, timeout time.Duration) ([]byte, error) {
	api, err := read(source, timeout)
	if err != nil {
		return nil, err
	}
	return models.Compact(api)
}

func read(source string, timeout time.Duration) ([]byte, error) {
	if !strings.HasPrefix(source, "https://") {
		return os.ReadFile(source)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", source, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err == nil && len(body) > maxCatalogBytes {
		return nil, fmt.Errorf("%s: larger than %d MiB", source, maxCatalogBytes>>20)
	}
	return body, err
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, "modelsgen:", err)
	return exitFailed
}
