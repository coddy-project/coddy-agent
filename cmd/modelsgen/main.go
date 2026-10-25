// Command modelsgen refreshes internal/llm/models/models.json from
// models.dev: the id, window and max_tokens of every model of the providers
// Coddy has (OpenAI, Anthropic, OpenCode), nothing else. It
// writes the file only when those numbers changed and says which models moved.
//
//	go run ./cmd/modelsgen          # refresh (make models-catalog)
//	go run ./cmd/modelsgen -check   # exit 1 when the copy is behind (make models-catalog-check)
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

func main() {
	check := flag.Bool("check", false, "only report whether models.json is behind the catalog; exit 1 when it is")
	source := flag.String("source", models.SourceURL, "the catalog: an https address or a local api.json")
	out := flag.String("out", models.Path, "models.json to compare with and write")
	flag.Parse()

	api, err := read(*source)
	if err != nil {
		fail(err)
	}
	fresh, err := models.Compact(api)
	if err != nil {
		fail(err)
	}
	current, err := os.ReadFile(*out)
	if err != nil && !os.IsNotExist(err) {
		fail(err)
	}
	if bytes.Equal(current, fresh) {
		fmt.Printf("modelsgen: %s is up to date\n", *out)
		return
	}
	changes, err := models.Changes(current, fresh)
	if err != nil {
		// A file a merge left conflict markers in is what a refresh repairs.
		changes = fmt.Sprintf("%v; a refresh writes it anew\n", err)
	}
	fmt.Print("modelsgen: ", changes)
	if *check {
		fmt.Fprintln(os.Stderr, "modelsgen: run make models-catalog")
		os.Exit(1)
	}
	if err := os.WriteFile(*out, fresh, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("modelsgen: wrote %s\n", *out)
}

func read(source string) ([]byte, error) {
	if !strings.HasPrefix(source, "https://") {
		return os.ReadFile(source)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "modelsgen:", err)
	os.Exit(2)
}
