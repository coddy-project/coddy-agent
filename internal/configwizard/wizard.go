// Package configwizard provides an interactive CLI wizard for initial agent setup.
package configwizard

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// ANSI color helpers.
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
	colorYellow = "\033[33m"
)

func bold(s string) string   { return colorBold + s + colorReset }
func cyan(s string) string   { return colorCyan + s + colorReset }
func green(s string) string  { return colorGreen + s + colorReset }
func yellow(s string) string { return colorYellow + s + colorReset }
func dim(s string) string    { return colorDim + s + colorReset }

// provider describes a supported LLM provider.
type provider struct {
	id          string
	name        string
	description string
	models      []modelPreset
	needsAPIKey bool
	needsURL    bool
	urlLabel    string
	urlHint     string
	urlDefault  string
}

type modelPreset struct {
	id    string
	label string
}

var providers = []provider{
	{
		id:          "openai",
		name:        "OpenAI",
		description: "GPT-5.4, GPT-5.4-mini and other OpenAI models",
		needsAPIKey: true,
		models: []modelPreset{
			{"gpt-5.4", "GPT-5.4 (flagship, best reasoning)"},
			{"gpt-5.4-mini", "GPT-5.4 mini (fast, balanced)"},
			{"gpt-5.4-nano", "GPT-5.4 nano (fastest, cheapest)"},
			{"gpt-4o", "GPT-4o (previous gen, stable)"},
			{"gpt-4o-mini", "GPT-4o mini (previous gen, cheap)"},
		},
	},
	{
		id:          "anthropic",
		name:        "Anthropic",
		description: "Claude Opus / Sonnet / Haiku models",
		needsAPIKey: true,
		models: []modelPreset{
			{"claude-opus-4-6", "Claude Opus 4.6 (most capable)"},
			{"claude-sonnet-4-6", "Claude Sonnet 4.6 (recommended, 1M ctx)"},
			{"claude-sonnet-4-5", "Claude Sonnet 4.5 (previous, stable)"},
			{"claude-haiku-4-5", "Claude Haiku 4.5 (fast, lightweight)"},
		},
	},
	{
		id:          "ollama",
		name:        "Ollama",
		description: "Local models via Ollama (llama3, qwen2.5-coder, phi4, etc.)",
		needsAPIKey: false,
		needsURL:    true,
		urlLabel:    "Ollama base URL",
		urlHint:     "include /v1 suffix",
		urlDefault:  "http://localhost:11434/v1",
		models:      nil,
	},
	{
		id:          "openai_compatible",
		name:        "OpenAI-compatible API",
		description: "LM Studio, Together AI, Groq, Fireworks, DeepSeek, etc.",
		needsAPIKey: true,
		needsURL:    true,
		urlLabel:    "API base URL",
		urlHint:     "usually ends with /v1",
		urlDefault:  "",
		models:      nil,
	},
}

// existing holds values read from an already present config file.
type existing struct {
	providerID string
	model      string
	apiKey     string
	baseURL    string
	proxy      string
}

// wizard holds shared state for the interactive session.
type wizard struct {
	sc       *bufio.Scanner
	isTTY    bool
	existing existing
}

// wizardResult holds everything collected during the wizard.
type wizardResult struct {
	provider   provider
	model      string
	apiKey     string
	baseURL    string
	proxy      string
	configPath string
}

// Run runs the interactive configuration wizard using the default config path.
func Run() error {
	return RunWithPath("")
}

// RunWithPath runs the wizard, loading defaults from the given config path.
// If path is empty, the default location (~/.config/coddy-agent/config.yaml) is used.
func RunWithPath(path string) error {
	if path == "" {
		path = defaultConfigFilePath()
	}
	w := &wizard{
		sc:       bufio.NewScanner(os.Stdin),
		isTTY:   term.IsTerminal(syscall.Stdin),
		existing: loadExisting(path),
	}
	return w.run(path)
}

func (w *wizard) run(configPath string) error {
	printHeader(w.existing.providerID != "")

	// Step 1 - choose provider.
	p, err := w.chooseProvider()
	if err != nil {
		return err
	}

	res := wizardResult{provider: p}

	// Step 2 - choose or enter model.
	// If the existing model belongs to a different provider, don't carry it over.
	existingModel := ""
	if w.existing.providerID == p.id {
		existingModel = w.existing.model
	}
	res.model, err = w.chooseModel(p, existingModel)
	if err != nil {
		return err
	}

	// Step 3 - API key.
	if p.needsAPIKey {
		// Carry over existing key only if same provider.
		existingKey := ""
		if w.existing.providerID == p.id {
			existingKey = w.existing.apiKey
		}
		res.apiKey, err = w.askSecret(p.name+" API key", existingKey)
		if err != nil {
			return err
		}
	}

	// Step 4 - base URL.
	if p.needsURL {
		urlDefault := p.urlDefault
		if w.existing.providerID == p.id && w.existing.baseURL != "" {
			urlDefault = w.existing.baseURL
		}
		res.baseURL, err = w.askWithDefault(
			fmt.Sprintf("%s %s", p.urlLabel, dim("("+p.urlHint+")")),
			urlDefault,
		)
		if err != nil {
			return err
		}
	}

	// Step 5 - optional proxy.
	res.proxy, err = w.askWithDefault(
		"HTTP/HTTPS proxy URL "+dim("(optional, leave empty to skip)"),
		w.existing.proxy,
	)
	if err != nil {
		return err
	}

	// Step 6 - config path.
	res.configPath, err = w.askWithDefault("Save config to", configPath)
	if err != nil {
		return err
	}
	res.configPath = expandHome(res.configPath)

	// Summary.
	fmt.Println()
	printSection("Summary")
	fmt.Printf("  Provider   : %s\n", bold(p.name))
	fmt.Printf("  Model      : %s\n", bold(res.model))
	if res.apiKey != "" {
		fmt.Printf("  API key    : %s\n", bold(maskSecret(res.apiKey)))
	}
	if res.baseURL != "" {
		fmt.Printf("  Base URL   : %s\n", bold(res.baseURL))
	}
	if res.proxy != "" {
		fmt.Printf("  Proxy      : %s\n", bold(res.proxy))
	}
	fmt.Printf("  Config file: %s\n", bold(res.configPath))
	fmt.Println()

	if !w.confirm("Write configuration?") {
		fmt.Println(yellow("Aborted."))
		return nil
	}

	if err := writeConfig(res); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(green("Configuration saved to " + bold(res.configPath)))
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  %s  %s\n", cyan("coddy acp"), dim("# start the ACP server"))
	fmt.Printf("  %s  %s\n", cyan("coddy list-skills"), dim("# see available skills"))
	fmt.Println()

	return nil
}

// ---- existing config loader ----

// loadExisting reads the first model definition from an existing config file.
// Returns zero-value existing{} if the file is absent or unreadable.
func loadExisting(path string) existing {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return existing{}
	}

	// Parse only the fields we care about - use a minimal struct to avoid
	// importing the full config package and creating a circular dependency.
	var raw struct {
		Models struct {
			Definitions []struct {
				Provider string `yaml:"provider"`
				Model    string `yaml:"model"`
				APIKey   string `yaml:"api_key"`
				BaseURL  string `yaml:"base_url"`
			} `yaml:"definitions"`
		} `yaml:"models"`
	}

	if err := yaml.Unmarshal(data, &raw); err != nil || len(raw.Models.Definitions) == 0 {
		return existing{}
	}

	def := raw.Models.Definitions[0]
	ex := existing{
		providerID: def.Provider,
		model:      def.Model,
		apiKey:     def.APIKey,
		baseURL:    def.BaseURL,
	}

	// Try to read proxy from companion proxy.env file.
	ex.proxy = loadProxyFromEnvFile(path)

	return ex
}

// loadProxyFromEnvFile reads HTTPS_PROXY from a proxy.env file next to the config.
func loadProxyFromEnvFile(configPath string) string {
	envFile := filepath.Join(filepath.Dir(expandHome(configPath)), "proxy.env")
	data, err := os.ReadFile(envFile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "export HTTPS_PROXY=") {
			return strings.TrimPrefix(line, "export HTTPS_PROXY=")
		}
	}
	return ""
}

// ---- wizard steps ----

func printHeader(isUpdate bool) {
	fmt.Println()
	fmt.Println(bold("  Coddy Agent - Setup Wizard"))
	if isUpdate {
		fmt.Println(dim("  Updating existing configuration"))
	} else {
		fmt.Println(dim("  Configure your LLM provider"))
	}
	fmt.Println()
}

func printSection(title string) {
	fmt.Printf("%s %s\n", cyan(">>"), bold(title))
}

func (w *wizard) chooseProvider() (provider, error) {
	printSection("Choose LLM provider")

	// Find the current provider index to mark it.
	currentIdx := -1
	for i, p := range providers {
		if p.id == w.existing.providerID {
			currentIdx = i
			break
		}
	}

	for i, p := range providers {
		current := ""
		if i == currentIdx {
			current = " " + dim("(current)")
		}
		fmt.Printf("  %s  %s%s\n", cyan(fmt.Sprintf("[%d]", i+1)), bold(p.name), current)
		fmt.Printf("      %s\n", dim(p.description))
	}
	fmt.Println()

	prompt := fmt.Sprintf("Provider [1-%d]", len(providers))
	if currentIdx >= 0 {
		prompt = fmt.Sprintf("Provider [1-%d] %s", len(providers), dim(fmt.Sprintf("[%d]", currentIdx+1)))
	}

	for {
		raw, err := w.ask(prompt)
		if err != nil {
			return provider{}, err
		}
		raw = strings.TrimSpace(raw)

		// Empty input + existing -> keep current.
		if raw == "" && currentIdx >= 0 {
			fmt.Println()
			return providers[currentIdx], nil
		}

		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 || n > len(providers) {
			fmt.Printf("  %s Please enter a number between 1 and %d\n", yellow("!"), len(providers))
			continue
		}
		fmt.Println()
		return providers[n-1], nil
	}
}

func (w *wizard) chooseModel(p provider, existingModel string) (string, error) {
	printSection("Model")

	if len(p.models) == 0 {
		hint := "e.g. llama3.2, qwen2.5-coder:14b"
		if p.id == "openai_compatible" {
			hint = "e.g. gpt-oss-120b, meta-llama/llama-3.3-70b-instruct"
		}
		model, err := w.askWithDefault(fmt.Sprintf("Model name %s", dim("("+hint+")")), existingModel)
		if err != nil {
			return "", err
		}
		fmt.Println()
		return model, nil
	}

	// Find current model index to mark it.
	currentIdx := -1
	for i, m := range p.models {
		if m.id == existingModel {
			currentIdx = i
			break
		}
	}

	for i, m := range p.models {
		current := ""
		if i == currentIdx {
			current = " " + dim("(current)")
		}
		fmt.Printf("  %s  %s%s\n", cyan(fmt.Sprintf("[%d]", i+1)), m.label, current)
	}
	fmt.Println()

	prompt := fmt.Sprintf("Model [1-%d]", len(p.models))
	if currentIdx >= 0 {
		prompt = fmt.Sprintf("Model [1-%d] %s", len(p.models), dim(fmt.Sprintf("[%d]", currentIdx+1)))
	}

	for {
		raw, err := w.ask(prompt)
		if err != nil {
			return "", err
		}
		raw = strings.TrimSpace(raw)

		if raw == "" && currentIdx >= 0 {
			fmt.Println()
			return p.models[currentIdx].id, nil
		}

		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 || n > len(p.models) {
			fmt.Printf("  %s Please enter a number between 1 and %d\n", yellow("!"), len(p.models))
			continue
		}
		fmt.Println()
		return p.models[n-1].id, nil
	}
}

// ---- I/O helpers ----

func (w *wizard) ask(prompt string) (string, error) {
	fmt.Printf("  %s: ", bold(prompt))
	if !w.sc.Scan() {
		if err := w.sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("unexpected EOF")
	}
	return w.sc.Text(), nil
}

func (w *wizard) askWithDefault(prompt, def string) (string, error) {
	display := prompt
	if def != "" {
		display = fmt.Sprintf("%s %s", prompt, dim("["+def+"]"))
	}
	val, err := w.ask(display)
	if err != nil {
		return "", err
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return def, nil
	}
	return val, nil
}

// askSecret reads a secret without echoing when stdin is a TTY.
// existingVal is shown masked as the default (press Enter to keep).
func (w *wizard) askSecret(prompt, existingVal string) (string, error) {
	display := bold(prompt)
	if existingVal != "" {
		display = fmt.Sprintf("%s %s", display, dim("["+maskSecret(existingVal)+"]"))
	}
	fmt.Printf("  %s: ", display)

	if w.isTTY {
		data, err := term.ReadPassword(syscall.Stdin)
		fmt.Println()
		if err != nil {
			return "", err
		}
		val := strings.TrimSpace(string(data))
		// Empty input -> keep existing.
		if val == "" && existingVal != "" {
			return existingVal, nil
		}
		return val, nil
	}

	// Non-TTY (piped input) - use the shared scanner.
	if !w.sc.Scan() {
		if err := w.sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("unexpected EOF")
	}
	val := strings.TrimSpace(w.sc.Text())
	if val == "" && existingVal != "" {
		return existingVal, nil
	}
	return val, nil
}

func (w *wizard) confirm(prompt string) bool {
	fmt.Printf("  %s %s: ", bold(prompt), dim("[Y/n]"))
	if !w.sc.Scan() {
		return false
	}
	ans := strings.TrimSpace(strings.ToLower(w.sc.Text()))
	return ans == "" || ans == "y" || ans == "yes"
}

func maskSecret(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

// ---- config writing ----

func writeConfig(res wizardResult) error {
	dir := filepath.Dir(res.configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	modelID := res.provider.id + "/" + res.model

	modelDef := map[string]interface{}{
		"id":          modelID,
		"provider":    res.provider.id,
		"model":       res.model,
		"max_tokens":  8192,
		"temperature": 0.2,
	}
	if res.apiKey != "" {
		modelDef["api_key"] = res.apiKey
	}
	if res.baseURL != "" {
		modelDef["base_url"] = res.baseURL
	}

	cfg := map[string]interface{}{
		"agent": map[string]interface{}{
			"name":    "coddy-agent",
			"version": "0.1.0",
		},
		"models": map[string]interface{}{
			"default":     modelID,
			"definitions": []interface{}{modelDef},
		},
		"react": map[string]interface{}{
			"max_turns":           30,
			"max_tokens_per_turn": 200000,
		},
		"tools": map[string]interface{}{
			"require_permission_for_commands": true,
			"require_permission_for_writes":   false,
			"restrict_to_cwd":                 true,
			"command_allowlist": []string{
				"go build", "go test", "go vet",
				"git status", "git log", "git diff",
			},
		},
		"skills": map[string]interface{}{
			"install_dir": "~/.config/coddy-agent/skills",
			"dirs": []string{
				"~/.config/coddy-agent/skills",
				"~/.cursor/skills",
				"~/.cursor/skills-cursor",
				"${WORKSPACE}/.cursor/rules",
				"${WORKSPACE}/.cursor/skills",
			},
		},
		"log": map[string]interface{}{
			"level": "info",
		},
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("# Coddy Agent configuration\n")
	sb.WriteString("# Generated by: coddy config\n")
	if res.proxy != "" {
		sb.WriteString("#\n")
		sb.WriteString("# Proxy is configured via environment variables.\n")
		sb.WriteString("# Add to your shell profile (~/.bashrc or ~/.zshrc):\n")
		sb.WriteString(fmt.Sprintf("#   export HTTPS_PROXY=%s\n", res.proxy))
		sb.WriteString(fmt.Sprintf("#   export HTTP_PROXY=%s\n", res.proxy))
		sb.WriteString("#   export NO_PROXY=localhost,127.0.0.1\n")
	}
	sb.WriteString("\n")
	sb.WriteString(string(data))

	if err := os.WriteFile(res.configPath, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Write a proxy.env helper file if proxy was provided.
	if res.proxy != "" {
		snippetPath := filepath.Join(filepath.Dir(res.configPath), "proxy.env")
		snippet := fmt.Sprintf(
			"export HTTPS_PROXY=%s\nexport HTTP_PROXY=%s\nexport NO_PROXY=localhost,127.0.0.1\n",
			res.proxy, res.proxy,
		)
		_ = os.WriteFile(snippetPath, []byte(snippet), 0o600)
		fmt.Printf("\n  %s proxy env snippet saved to %s\n", dim("hint:"), snippetPath)
		fmt.Printf("  %s\n", dim("  source "+snippetPath))
	}

	return nil
}

func defaultConfigFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./config.yaml"
	}
	return filepath.Join(home, ".config", "coddy-agent", "config.yaml")
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
