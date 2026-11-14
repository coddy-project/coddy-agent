package providerlogin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

var kimiCatalogID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// catalogSnapshot reads without the normal loader's backup recovery. A config
// whose only model-reference defect is a missing default can be repaired by
// the same CAS that adds the catalog; the CAS validates the resulting file.
func catalogSnapshot(paths config.Paths) (*config.Config, []byte, error) {
	snapshot, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	current, read, err := config.LoadReadOnly(config.CLIPaths{Home: paths.Home, CWD: paths.CWD, Config: paths.ConfigPath})
	if err == nil {
		if !bytes.Equal(snapshot, read) {
			return nil, nil, errors.New("config changed while reading Kimi catalog")
		}
		current.Paths = paths
		return current, snapshot, nil
	}
	var candidate config.Config
	if yamlErr := yaml.Unmarshal(snapshot, &candidate); yamlErr != nil {
		return nil, nil, err
	}
	ref := strings.TrimSpace(candidate.Agent.Model)
	if ref == "" || strings.Contains(ref, "$") || candidate.FindModelEntry(ref) != nil {
		return nil, nil, err
	}
	for _, model := range candidate.Models {
		if strings.Contains(model.Model, "$") {
			return nil, nil, err
		}
	}
	for _, p := range candidate.Providers {
		if p.Type == "kimi" && strings.ContainsAny(p.Name+p.Mode+p.Region+p.KimiIdentity+p.Proxy, "$") {
			return nil, nil, err
		}
	}
	candidate.Agent.Model = ""
	if validationErr := candidate.ValidateModelsProvidersAndAgent(); validationErr != nil {
		return nil, nil, err
	}
	candidate.Agent.Model = ref
	candidate.Paths = paths
	latest, readErr := os.ReadFile(paths.ConfigPath)
	if readErr != nil || !bytes.Equal(snapshot, latest) {
		return nil, nil, errors.New("config changed while reading Kimi catalog")
	}
	return &candidate, snapshot, nil
}

// ApplyKimiLoginCatalog reads the managed credential and adds the models the
// signed-in provider advertises without replacing existing model choices.
func ApplyKimiLoginCatalog(ctx context.Context, cfg *config.Config, provider config.ProviderConfig) ([]string, error) {
	if cfg == nil {
		return nil, errors.New("kimi catalog: no configuration")
	}
	if provider.Type != "kimi" || (provider.Mode != "" && provider.Mode != "subscription") {
		return nil, errors.New("kimi catalog requires subscription mode")
	}
	current, _, err := catalogSnapshot(cfg.Paths)
	if err != nil {
		return nil, err
	}
	saved := current.FindProvider(provider.Name)
	region, err := llm.KimiCanonicalRegion(provider.Region)
	if err != nil {
		return nil, err
	}
	if saved == nil || saved.Type != "kimi" || saved.KimiIdentity != provider.KimiIdentity || !config.ValidKimiIdentity(provider.KimiIdentity) || (saved.Mode != "" && saved.Mode != "subscription") {
		return nil, errors.New("kimi provider identity or mode changed during login")
	}
	savedRegion, err := llm.KimiCanonicalRegion(saved.Region)
	if err != nil || savedRegion != region || saved.Proxy != provider.Proxy {
		return nil, errors.New("kimi provider region or proxy changed during login")
	}
	store := llm.NewKimiCredentialStore(config.KimiIdentityDir(cfg.Paths.Home, provider.KimiIdentity))
	credential, state, err := store.Load(region)
	if err != nil {
		return nil, err
	}
	if state != llm.KimiCredentialValid || credential.AccessToken == "" {
		return nil, errors.New("kimi managed credential unavailable")
	}
	models, err := kimiListForLogin(ctx, "subscription", region, credential.AccessToken, provider.Proxy)
	if err != nil {
		return nil, fmt.Errorf("fetch Kimi model catalog: %w", err)
	}
	latest, state, err := store.Load(region)
	if err != nil {
		return nil, err
	}
	if state != llm.KimiCredentialValid || latest.AccessToken != credential.AccessToken || latest.RefreshToken != credential.RefreshToken {
		return nil, errors.New("kimi managed credential changed during catalog fetch")
	}
	return applyKimiCatalog(ctx, cfg.Paths, provider, models)
}

func applyKimiCatalog(ctx context.Context, paths config.Paths, provider config.ProviderConfig, models []llm.KimiModel) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if provider.Type != "kimi" || (provider.Mode != "" && provider.Mode != "subscription") {
		return nil, errors.New("kimi catalog requires subscription mode")
	}
	current, snapshot, err := catalogSnapshot(paths)
	if err != nil {
		return nil, err
	}
	saved := current.FindProvider(provider.Name)
	region, err := llm.KimiCanonicalRegion(provider.Region)
	if err != nil {
		return nil, err
	}
	if saved == nil || saved.Type != "kimi" || saved.KimiIdentity != provider.KimiIdentity || !config.ValidKimiIdentity(provider.KimiIdentity) || (saved.Mode != "" && saved.Mode != "subscription") {
		return nil, errors.New("kimi provider identity or mode changed during login")
	}
	savedRegion, err := llm.KimiCanonicalRegion(saved.Region)
	if err != nil || savedRegion != region {
		return nil, errors.New("kimi provider region changed during login")
	}
	var commands []config.UCICommand
	var added []string
	var first string
	currentRef := strings.TrimSpace(current.Agent.Model)
	currentKimiUsable := false
	seen := map[string]bool{}
	for _, model := range models {
		if !kimiCatalogID.MatchString(model.ID) || seen[model.ID] {
			continue
		}
		switch model.Protocol {
		case "kimi", "anthropic", "openai_responses":
		default:
			continue
		}
		seen[model.ID] = true
		ref := provider.Name + "/" + model.ID
		existing := current.FindModelEntry(ref)
		if existing != nil && existing.KimiMetadata != nil {
			metadata := existing.KimiMetadata
			if metadata.Model != ref || metadata.Mode != "subscription" || metadata.Region != region || metadata.Protocol != model.Protocol {
				continue
			}
		}
		if first == "" {
			first = ref
		}
		if existing != nil && ref == currentRef {
			currentKimiUsable = true
		}
		if existing != nil {
			continue
		}
		entry := map[string]interface{}{"model": ref, "kimi_metadata": map[string]string{"model": ref, "protocol": model.Protocol, "mode": "subscription", "region": region}}
		if model.ContextWindow > 0 {
			entry["max_context_tokens"] = model.ContextWindow
		}
		if model.ImageInput != nil {
			entry["multimodal"] = *model.ImageInput
		}
		if model.ReasoningLevels != nil {
			entry["reasoning_levels"] = model.ReasoningLevels
		}
		if model.DefaultReasoning != "" {
			entry["reasoning_default"] = model.DefaultReasoning
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		commands = append(commands, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("models[model=%s]", ref), Value: string(raw)})
		added = append(added, "model "+ref)
	}
	if first == "" {
		return nil, errors.New("kimi model catalog contains no supported models")
	}
	defaultReady := false
	if currentRef != "" && current.FindModelEntry(currentRef) != nil {
		owner, _, splitErr := config.SplitModelRef(currentRef)
		if splitErr == nil {
			if owner == provider.Name {
				defaultReady = currentKimiUsable
			} else if _, resolveErr := current.ResolveLLM(currentRef); resolveErr == nil {
				defaultReady = ProviderReady(current, owner)
			}
		}
	}
	if !defaultReady {
		commands = append(commands, config.UCICommand{Op: config.UCIOpSet, Path: "agent.model", Value: first})
		added = append(added, "agent.model "+first)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(commands) == 0 {
		return nil, nil
	}
	if _, err := config.CommitUCICommandsIfUnchanged(paths, snapshot, commands); err != nil {
		return nil, fmt.Errorf("kimi catalog config conflict: %w", err)
	}
	return added, nil
}
