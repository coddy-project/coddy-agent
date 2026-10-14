package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// OpenCode Go / Console sign-in.
//
// OpenCode has no OAuth or device flow: a person subscribes in the web console
// (https://opencode.ai/console), copies an API key, and pastes it. `coddy
// providers login opencode` is that paste - it stores the key here and, unless
// asked not to, writes the provider row and the gateway's models into
// config.yaml. The key is an ordinary Bearer credential; the catalog and the
// per-model usage limits are enforced by the gateway, so Coddy only stores and
// presents it.

// opencodeAuthFile is the stored credential. The key is the only secret; the
// rest exists so status displays and logout know where it came from.
type opencodeAuthFile struct {
	APIKey     string `json:"api_key"`
	APIBase    string `json:"api_base"`
	ObtainedAt string `json:"obtained_at"`
}

// opencodeAuthMu serializes credential file access process-wide, mirroring
// neuralDeepAuthMu.
var opencodeAuthMu sync.Mutex

// SaveOpenCodeAuth writes the credential file atomically: a temp file in the
// destination directory gets 0600 before content, then replaces the target via
// rename, so a crash never leaves a truncated credential and the key never
// exists on disk with looser permissions.
func SaveOpenCodeAuth(path, apiKey, apiBase string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("opencode auth: credential path is empty")
	}
	if strings.TrimSpace(apiKey) == "" {
		return errors.New("opencode auth: refusing to store an empty key")
	}
	data, err := json.MarshalIndent(opencodeAuthFile{
		APIKey:     apiKey,
		APIBase:    strings.TrimRight(apiBase, "/"),
		ObtainedAt: time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	opencodeAuthMu.Lock()
	defer opencodeAuthMu.Unlock()
	return writePrivateFileAtomic(path, data)
}

func loadOpenCodeAuth(path string) (*opencodeAuthFile, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	opencodeAuthMu.Lock()
	defer opencodeAuthMu.Unlock()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f opencodeAuthFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("opencode auth: parse %s: %w", path, err)
	}
	return &f, nil
}

// LoadOpenCodeKey returns the stored key, or "" when no login exists.
func LoadOpenCodeKey(path string) (string, error) {
	f, err := loadOpenCodeAuth(path)
	if err != nil || f == nil {
		return "", err
	}
	return strings.TrimSpace(f.APIKey), nil
}

// RemoveOpenCodeAuth deletes the credential file; a missing file is fine.
func RemoveOpenCodeAuth(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("opencode auth: credential path is empty")
	}
	opencodeAuthMu.Lock()
	defer opencodeAuthMu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// OpenCodeAuthStatus reports the stored login without exposing the key.
type OpenCodeAuthStatus struct {
	Connected bool
	Masked    string
	APIBase   string
}

// InspectOpenCodeAuth reads the credential file into a display-safe status.
func InspectOpenCodeAuth(path string) (OpenCodeAuthStatus, error) {
	f, err := loadOpenCodeAuth(path)
	if err != nil {
		return OpenCodeAuthStatus{}, err
	}
	if f == nil || strings.TrimSpace(f.APIKey) == "" {
		return OpenCodeAuthStatus{}, nil
	}
	return OpenCodeAuthStatus{
		Connected: true,
		Masked:    maskOpenCodeKey(f.APIKey),
		APIBase:   f.APIBase,
	}, nil
}

// maskOpenCodeKey renders a short prefix and the last four characters without
// revealing the key. Short keys get the fixed mask only: with a window this
// wide a short key would leak most of its entropy.
func maskOpenCodeKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) < 20 {
		return "…"
	}
	return key[:5] + "…" + key[len(key)-4:]
}

var opencodeSecretRe = regexp.MustCompile(`(?i)opencode[_-]?[A-Za-z0-9_-]+`)

// RedactOpenCodeSecrets masks anything that looks like an OpenCode key in a
// message that may be logged or shown, so a pasted key never lands in a log.
func RedactOpenCodeSecrets(s string) string {
	return opencodeSecretRe.ReplaceAllString(s, "opencode-…")
}

// ApplyOpenCodeLoginToConfig writes the provider row and the gateway's models
// into config.yaml after a paste-and-store login, mirroring
// ApplyNeuralDeepLoginToConfig: it only ever adds, never rewrites a row or a
// model the operator already configured, and stages every change as a UCI
// command committed under the config file lock. proxySetting is the row's
// providers[].proxy, which the model list resolves its own client from (the
// gateway is reached through CODDY_OPENCODE_BASE_URL in tests). It returns what
// it added.
func ApplyOpenCodeLoginToConfig(ctx context.Context, cfg *config.Config, name, apiBase, key, proxySetting string) ([]string, error) {
	models, err := ListModels(ctx, ProviderInput{
		Name:     name,
		Type:     "opencode",
		APIKey:   key,
		BaseURL:  apiBase,
		ProxyURL: proxySetting,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch the model catalog: %w", err)
	}
	var cmds []config.UCICommand
	var added []string
	loginBase, loginBaseKnown := NormalizeOpenCodeAPIBase(apiBase)
	if existing := cfg.FindProvider(name); existing == nil {
		prov := map[string]string{"name": name, "type": "opencode"}
		// Only a non-default gateway needs recording: a row without api_base
		// keeps meaning the default (Go), exactly as it did before.
		if loginBaseKnown && loginBase != opencodeGoBaseURL {
			prov["api_base"] = loginBase
		}
		provJSON, _ := json.Marshal(prov)
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("providers[name=%s]", name), Value: string(provJSON)})
		added = append(added, "provider "+name)
	} else if loginBaseKnown {
		current, ok := NormalizeOpenCodeAPIBase(existing.APIBase)
		if !ok && strings.TrimSpace(existing.APIBase) == "" {
			current = opencodeGoBaseURL
		}
		if current != loginBase {
			path := fmt.Sprintf("providers[name=%s].api_base", name)
			if loginBase == opencodeGoBaseURL {
				cmds = append(cmds, config.UCICommand{Op: config.UCIOpDelete, Path: path})
				added = append(added, "provider "+name+" api_base -> default ("+loginBase+")")
			} else {
				cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: path, Value: loginBase})
				added = append(added, "provider "+name+" api_base -> "+loginBase)
			}
		}
	}
	for _, m := range models {
		id := strings.TrimSpace(m.ID)
		if !catalogModelIDRe.MatchString(id) {
			// The id feeds a config path; the gateway (or a stand-in) must not
			// be able to smuggle path syntax into the staged commands.
			continue
		}
		ref := name + "/" + id
		if cfg.FindModelEntry(ref) != nil {
			continue
		}
		entry := map[string]any{"model": ref}
		if m.ContextWindow > 0 {
			entry["max_context_tokens"] = m.ContextWindow
		}
		entryJSON, _ := json.Marshal(entry)
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("models[model=%s]", ref), Value: string(entryJSON)})
		added = append(added, "model "+ref)
	}
	if strings.TrimSpace(cfg.Agent.Model) == "" && len(models) > 0 {
		def := name + "/" + models[0].ID
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: "agent.model", Value: def})
		added = append(added, "agent.model "+def)
	}
	if len(cmds) == 0 {
		return nil, nil
	}
	if _, err := config.CommitUCICommands(cfg.Paths, cmds); err != nil {
		return nil, err
	}
	return added, nil
}
