// Command modelsgen refreshes internal/llm/models/models.json: the id, window
// and max_tokens of every model of the providers Coddy has, from models.dev
// and from NeuralDeep's own model list, nothing else. It writes the file only
// when those numbers changed and says which models moved.
//
// NeuralDeep lists its models only for a key: by default the command asks
// with the key and proxy of the neuraldeep row of your Coddy configuration
// (`coddy providers login neuraldeep` writes one), as Coddy itself would.
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

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
)

// maxCatalogBytes bounds the download: api.json is about 6 MB.
const maxCatalogBytes = 64 << 20

func main() {
	check := flag.Bool("check", false, "only report whether models.json is behind the catalog; exit 1 when it is")
	source := flag.String("source", models.SourceURL, "models.dev's catalog: an https address or a local api.json")
	neuralDeep := flag.String("neuraldeep", "", "NeuralDeep's model list: an https address or a local file; empty asks GET /models of the neuraldeep row of the Coddy configuration")
	out := flag.String("out", models.Path, "models.json to compare with and write")
	flag.Parse()

	api, err := read(*source)
	if err != nil {
		fail(err)
	}
	var nd []byte
	if *neuralDeep != "" {
		nd, err = read(*neuralDeep)
	} else {
		nd, err = neuralDeepModels()
	}
	if err != nil {
		fail(err)
	}
	fresh, err := models.Compact(api, nd)
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
	req, err := http.NewRequest(http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	return fetch(&http.Client{Timeout: 2 * time.Minute}, req)
}

// neuralDeepModels asks NeuralDeep's GET /models with the first neuraldeep
// row of the Coddy configuration: its endpoint, its key (or the key its
// login stored) and its proxy.
func neuralDeepModels() ([]byte, error) {
	cfg, _, err := config.LoadReadOnly(config.CLIPaths{})
	if err != nil {
		return nil, err
	}
	for i := range cfg.Providers {
		prov := &cfg.Providers[i]
		if prov.Type != "neuraldeep" {
			continue
		}
		key := prov.EffectiveAPIKey()
		if strings.TrimSpace(key) == "" {
			if key, err = llm.LoadNeuralDeepKey(config.NeuralDeepAuthPath(cfg.Paths.Home, prov.Name)); err != nil {
				return nil, fmt.Errorf("provider %s has no NeuralDeep key: %w", prov.Name, err)
			}
		}
		client, err := llm.HTTPClientForProviderProxy(prov.Proxy)
		if err != nil {
			return nil, err
		}
		base, ok := llm.NormalizeNeuralDeepAPIBase(prov.APIBase)
		if !ok {
			base = llm.NeuralDeepDefaultAPIBase()
		}
		req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		return fetch(client, req)
	}
	return nil, fmt.Errorf("%s has no neuraldeep provider: sign in with coddy providers login neuraldeep, or pass -neuraldeep with NeuralDeep's model list", cfg.Paths.ConfigPath)
}

func fetch(client *http.Client, req *http.Request) ([]byte, error) {
	source := req.URL.String()
	resp, err := client.Do(req)
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
