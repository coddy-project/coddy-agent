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
// Account sign-in uses Console's device flow. Service accounts may still use
// API keys. Both credentials use the same proxy-aware inference transport.

// opencodeAuthFile stores either a service account key or account tokens.
// Account metadata is used for organization headers and credential status.
type opencodeAuthFile struct {
	APIKey       string `json:"api_key,omitempty"`
	APIBase      string `json:"api_base"`
	ObtainedAt   string `json:"obtained_at"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	OrgID        string `json:"org_id,omitempty"`
	OrgName      string `json:"org_name,omitempty"`
	Email        string `json:"email,omitempty"`
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
	refresh := opencodeRefreshLock(path)
	refresh.Lock()
	defer refresh.Unlock()
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
	refresh := opencodeRefreshLock(path)
	refresh.Lock()
	defer refresh.Unlock()
	opencodeAuthMu.Lock()
	defer opencodeAuthMu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// OpenCodeAuthStatus reports the stored login without exposing the key.
type OpenCodeAuthStatus struct {
	Connected    bool
	Masked       string
	APIBase      string
	Account      bool
	Organization string
}

// InspectOpenCodeAuth reads the credential file into a display-safe status.
func InspectOpenCodeAuth(path string) (OpenCodeAuthStatus, error) {
	f, err := loadOpenCodeAuth(path)
	if err != nil {
		return OpenCodeAuthStatus{}, err
	}
	if f == nil || (strings.TrimSpace(f.APIKey) == "" && strings.TrimSpace(f.RefreshToken) == "") {
		return OpenCodeAuthStatus{}, nil
	}
	return OpenCodeAuthStatus{
		Connected:    true,
		Masked:       maskOpenCodeKey(f.APIKey),
		APIBase:      f.APIBase,
		Account:      f.RefreshToken != "",
		Organization: f.OrgName,
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
// into config.yaml after an account or service account login, mirroring
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
		AuthPath: config.OpenCodeAuthPath(cfg.Paths.Home, name),
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
	// The first id that survives the check below is the default: the catalog is
	// already sorted by id, so this is the first model the operator can pick.
	var defaultRef string
	for _, m := range models {
		id := strings.TrimSpace(m.ID)
		if !catalogModelIDRe.MatchString(id) {
			// The id feeds a config path; the gateway (or a stand-in) must not
			// be able to smuggle path syntax into the staged commands.
			continue
		}
		ref := name + "/" + id
		if defaultRef == "" {
			defaultRef = ref
		}
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
	if strings.TrimSpace(cfg.Agent.Model) == "" && defaultRef != "" {
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: "agent.model", Value: defaultRef})
		added = append(added, "agent.model "+defaultRef)
	}
	if len(cmds) == 0 {
		return nil, nil
	}
	if _, err := config.CommitUCICommands(cfg.Paths, cmds); err != nil {
		return nil, err
	}
	return added, nil
}
