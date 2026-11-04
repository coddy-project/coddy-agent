//go:build cli

package cli

// The console's first-run provider picker: the same catalog the web UI's
// picker offers (internal/providerpreset), in the same order and opening on
// the same provider, so a person who set Coddy up in a browser recognises the
// console and the other way round.
//
// It opens by itself the first time a console starts with no provider that
// can answer, and again whenever a bare /login is typed, so it is never a
// one-shot screen. Picking a provider that signs in (NeuralDeep, OpenCode,
// Codex, Devin) runs the sign-in of login.go; picking one that takes a key
// asks for its endpoint and key, lists what the provider serves and writes
// the chosen row, its model and agent.model into config.yaml.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/providerlogin"
	"github.com/EvilFreelancer/coddy-agent/internal/providerpreset"
)

// providerPickerTitle heads the picker; the tests and the capture scripts
// look for it.
const providerPickerTitle = "Set up a model provider"

// providerSetupRun carries one walk through the picker: the preset chosen and
// what has been answered for it so far. Only the UI loop touches it.
type providerSetupRun struct {
	preset  providerpreset.Preset
	apiBase string
	apiKey  string
	// models is what the provider answered when it was asked; empty when it
	// was not asked or could not answer.
	models []llm.ModelEntry
}

// openProviderPicker shows the catalog. A picker already on screen, a sign-in
// already running or another modal in the way keeps the screen as it is.
func (a *App) openProviderPicker() {
	if a.modal != nil {
		a.appendStatus(roleWarning, "Close what is on screen first, then type /login again")
		return
	}
	if a.login != nil {
		a.appendStatus(roleWarning, "A sign-in to "+a.login.title+" is already running (escape to stop it)")
		return
	}
	items := make([]tui.SelectItem, 0, len(providerpreset.Presets))
	for _, p := range providerpreset.Presets {
		items = append(items, tui.SelectItem{Value: p.ID, Label: p.Label, Description: p.Description})
	}
	sel := newSelectorModal(a.theme, providerPickerTitle, items, len(items), a.screen.RequestRender)
	sel.body = a.theme.Fg(roleMuted, "Pick the provider to run on. You can run /login again to change it.")
	sel.rebuild()
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item == nil {
			a.appendStatus(roleDim, "No provider chosen; /login opens this list again")
			a.screen.RequestRender()
			return
		}
		preset, ok := providerpreset.ByID(item.Value)
		if !ok {
			a.screen.RequestRender()
			return
		}
		a.beginProviderSetup(&providerSetupRun{preset: preset, apiBase: preset.DefaultAPIBase()})
		a.screen.RequestRender()
	}
	a.openModal(sel)
	a.screen.RequestRender()
}

// beginProviderSetup asks for the endpoint when the preset offers a choice,
// and otherwise goes straight on to the sign-in or the key.
func (a *App) beginProviderSetup(run *providerSetupRun) {
	if len(run.preset.APIBases) > 1 {
		a.askProviderEndpoint(run)
		return
	}
	a.continueProviderSetup(run)
}

// askProviderEndpoint offers the deployments of a preset that has more than
// one (NeuralDeep, OpenCode). Escape goes back to the catalog.
func (a *App) askProviderEndpoint(run *providerSetupRun) {
	items := make([]tui.SelectItem, 0, len(run.preset.APIBases))
	for _, base := range run.preset.APIBases {
		items = append(items, tui.SelectItem{Value: base.Value, Label: base.Label, Description: base.Value})
	}
	sel := newSelectorModal(a.theme, run.preset.Label+": which endpoint", items, len(items), a.screen.RequestRender)
	sel.help = "↑↓ navigate · enter select · esc back to the providers"
	sel.rebuild()
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item == nil {
			a.openProviderPicker()
			return
		}
		run.apiBase = item.Value
		a.continueProviderSetup(run)
		a.screen.RequestRender()
	}
	a.openModal(sel)
	a.screen.RequestRender()
}

// continueProviderSetup starts the sign-in of a provider that has one, and
// asks for the endpoint and the key of one that takes a key.
func (a *App) continueProviderSetup(run *providerSetupRun) {
	if providerlogin.Supports(run.preset.ProviderType) {
		a.startPresetLogin(run)
		return
	}
	if run.preset.APIBaseTyped {
		a.askProviderAPIBase(run)
		return
	}
	a.askProviderKey(run)
}

// startPresetLogin resolves the preset's row and runs the sign-in of
// login.go on it, aimed at the endpoint that was picked.
func (a *App) startPresetLogin(run *providerSetupRun) {
	prov, err := providerlogin.Resolve(a.config(), run.preset.ProviderName, run.preset.ProviderType)
	if err != nil {
		a.appendStatus(roleWarning, "/login: "+err.Error())
		return
	}
	// Resolve hands back the live row when config.yaml lists one; the picked
	// endpoint belongs to this sign-in, not to the configuration until the
	// sign-in writes it.
	row := *prov
	if run.apiBase != "" {
		row.APIBase = run.apiBase
	}
	a.startLogin(row)
}

// askProviderAPIBase asks for the endpoint of a provider that has none of its
// own: an Ollama server (prefilled) or any other OpenAI-compatible address,
// which the setup refuses to go on without.
func (a *App) askProviderAPIBase(run *providerSetupRun) {
	hint := "Enter the address of the OpenAI-compatible API, for example https://api.example.com/v1"
	if run.preset.APIBaseDefault != "" {
		hint = "Enter the address of the server, or keep the one offered"
	}
	a.askProviderValue(run.preset.Label+": API base URL", hint, run.preset.APIBaseDefault, func(text string) {
		base := strings.TrimSpace(text)
		if base == "" {
			a.appendStatus(roleWarning, run.preset.Label+" needs an API base URL; /login opens the list again")
			return
		}
		run.apiBase = base
		a.askProviderKey(run)
	})
}

// askProviderKey asks for the API key. An empty answer is a deliberate
// choice: the row then reads its NAME_API_KEY variable, which is how a local
// server with no key works.
func (a *App) askProviderKey(run *providerSetupRun) {
	hint := "Paste the API key, or send an empty line to read it from the environment"
	if run.preset.EnvKey != "" {
		hint = "Paste the API key, or send an empty line to read it from " + run.preset.EnvKey
	}
	a.askProviderValue(run.preset.Label+": API key", hint, "", func(text string) {
		run.apiKey = strings.TrimSpace(text)
		a.fetchProviderModels(run)
	})
}

// askProviderValue opens a one-line editor over the chat: a title, what the
// line is for, and the answer. Escape goes back to the catalog.
func (a *App) askProviderValue(title, hint, prefill string, done func(string)) {
	m := newValuePromptModal(a.theme, title, hint, prefill, a.screen.RequestRender)
	m.OnDone = func(text string, ok bool) {
		a.closeModal()
		if !ok {
			a.openProviderPicker()
			return
		}
		done(text)
		a.screen.RequestRender()
	}
	a.openModal(m)
	a.screen.RequestRender()
}

// fetchProviderModels asks the provider what it serves, on a worker, and
// offers the answer. The row is not written yet: nothing lands in
// config.yaml until a model has been chosen.
func (a *App) fetchProviderModels(run *providerSetupRun) {
	a.appendStatus(roleDim, "Asking "+run.preset.Label+" for its models...")
	cfg := a.config()
	in := llm.ProviderInput{
		Name:     run.preset.ProviderName,
		Type:     run.preset.ProviderType,
		APIKey:   run.apiKey,
		BaseURL:  run.apiBase,
		ProxyURL: providerRowProxy(cfg, run.preset.ProviderName),
	}
	ctx := a.workCtx
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		models, err := llm.ListModels(ctx, in)
		a.postUI(func() { a.offerProviderModels(run, models, err) })
	}()
}

// offerProviderModels puts the catalog on screen, or says why there is none
// and lets the model be named by hand.
func (a *App) offerProviderModels(run *providerSetupRun, models []llm.ModelEntry, err error) {
	if err != nil {
		a.appendStatus(roleWarning, run.preset.Label+" did not answer with a model catalog: "+err.Error())
		a.askProviderValue(run.preset.Label+": model", "Name the model to run on; the provider will be asked again at the first turn",
			strings.TrimPrefix(run.preset.DefaultModel, run.preset.ProviderName+"/"), func(text string) {
				a.saveProviderSetup(run, strings.TrimSpace(text), nil)
			})
		return
	}
	run.models = models
	if len(models) == 0 {
		a.appendStatus(roleWarning, run.preset.Label+" lists no model")
		a.askProviderValue(run.preset.Label+": model", "Name the model to run on",
			strings.TrimPrefix(run.preset.DefaultModel, run.preset.ProviderName+"/"), func(text string) {
				a.saveProviderSetup(run, strings.TrimSpace(text), nil)
			})
		return
	}
	items := make([]tui.SelectItem, 0, len(models))
	for _, m := range models {
		label := tui.SanitizeText(m.ID)
		desc := ""
		if m.ContextWindow > 0 {
			desc = fmt.Sprintf("%d tokens of context", m.ContextWindow)
		}
		items = append(items, tui.SelectItem{Value: m.ID, Label: label, Description: desc})
	}
	sel := newSelectorModal(a.theme, run.preset.Label+": which model", items, min(len(items), 8), a.screen.RequestRender)
	sel.help = "↑↓ navigate · enter select · esc back to the providers · type to filter"
	sel.rebuild()
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item == nil {
			a.openProviderPicker()
			return
		}
		a.saveProviderSetup(run, item.Value, findModelEntry(models, item.Value))
		a.screen.RequestRender()
	}
	a.openModal(sel)
	a.screen.RequestRender()
}

// saveProviderSetup writes the provider row, the model and, where there is
// none that answers, agent.model, then installs the new configuration so the
// next prompt runs on it.
func (a *App) saveProviderSetup(run *providerSetupRun, modelID string, listed *llm.ModelEntry) {
	ref := run.preset.ModelRef(modelID)
	if _, _, err := config.SplitModelRef(ref); err != nil {
		a.appendStatus(roleError, "Could not read "+tui.SanitizeText(ref)+" as a model: "+err.Error())
		return
	}
	cfg := a.config()
	cmds, added := providerSetupCommands(cfg, run, ref, listed)
	if len(cmds) == 0 {
		a.appendStatus(roleDim, "config.yaml already lists provider "+run.preset.ProviderName+" and "+tui.SanitizeText(ref))
	} else if _, err := config.CommitUCICommands(cfg.Paths, cmds); err != nil {
		a.appendStatus(roleError, "Could not update config.yaml: "+err.Error())
		return
	} else {
		a.appendStatus(roleSuccess, "Set up "+run.preset.Label)
		a.appendStatus(roleDim, "Added to config.yaml: "+providerlogin.SummarizeAdded(added))
	}
	if err := a.adoptLoginConfig(cfg, a.sessionID); err != nil {
		a.appendStatus(roleWarning, "The console could not load the new config.yaml ("+err.Error()+"); restart coddy to use it")
		return
	}
	// The operator picked this model: the session runs on it, whatever the
	// one it started on was.
	if a.modelID != ref {
		a.modelID = ref
		a.refreshFooterModel()
		a.setModel(ref)
		a.appendStatus(roleDim, "This session runs on "+tui.SanitizeText(ref)+" now")
	}
	a.populateHeader()
	a.refreshUsage(usageProviderOf(ref), false)
}

// providerSetupCommands stages what config.yaml is missing: the provider row
// with the endpoint and the key that were answered, the model row, and
// agent.model when nothing it names can answer.
func providerSetupCommands(cfg *config.Config, run *providerSetupRun, ref string, listed *llm.ModelEntry) ([]config.UCICommand, []string) {
	var cmds []config.UCICommand
	var added []string
	name := run.preset.ProviderName
	row := map[string]string{"name": name, "type": run.preset.ProviderType}
	if base := strings.TrimSpace(run.apiBase); base != "" {
		row["api_base"] = base
	}
	// A key typed here is the one requests use; an empty answer leaves the
	// row without one, so the conventional NAME_API_KEY variable applies.
	// The placeholder of the preset is never written: the save escapes "$",
	// and the row would then send that text as the bearer token.
	if key := strings.TrimSpace(run.apiKey); key != "" {
		row["api_key"] = key
	}
	existing := cfg.FindProvider(name)
	if existing == nil || !sameProviderRow(existing, row) {
		rowJSON, _ := json.Marshal(row)
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("providers[name=%s]", name), Value: string(rowJSON)})
		added = append(added, "provider "+name)
	}
	if cfg.FindModelEntry(ref) == nil {
		entry := map[string]any{"model": ref}
		switch {
		case listed != nil && listed.ContextWindow > 0:
			entry["max_context_tokens"] = listed.ContextWindow
		case run.preset.MaxContextTokens > 0:
			entry["max_context_tokens"] = run.preset.MaxContextTokens
		}
		if run.preset.Multimodal {
			entry["multimodal"] = true
		}
		entryJSON, _ := json.Marshal(entry)
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("models[model=%s]", ref), Value: string(entryJSON)})
		added = append(added, "model "+ref)
	}
	// agent.model decides what a new session starts on. It moves only when
	// what it names cannot answer, so a working setup is never repointed by
	// adding a second provider.
	current := strings.TrimSpace(cfg.Agent.Model)
	owner, _, _ := config.SplitModelRef(current)
	if current == "" || (owner != name && !providerlogin.ProviderReady(cfg, owner)) {
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: "agent.model", Value: ref})
		added = append(added, "agent.model "+ref)
	}
	return cmds, added
}

// sameProviderRow reports a configured row that already says what the setup
// would write, so nothing is staged for it.
func sameProviderRow(existing *config.ProviderConfig, row map[string]string) bool {
	return existing.Type == row["type"] &&
		strings.TrimSpace(existing.APIBase) == row["api_base"] &&
		strings.TrimSpace(existing.APIKey) == row["api_key"]
}

// providerRowProxy is the proxy setting of the row the preset writes, so the
// model listing of a setup takes the route the row already names.
func providerRowProxy(cfg *config.Config, name string) string {
	if prov := cfg.FindProvider(name); prov != nil {
		return prov.Proxy
	}
	return ""
}

func findModelEntry(models []llm.ModelEntry, id string) *llm.ModelEntry {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

// valuePromptModal asks for one line: a title, what the line is for, and an
// editor. Escape cancels, which the picker reads as "back".
type valuePromptModal struct {
	tui.Container

	editor *tui.Editor
	OnDone func(text string, ok bool)
}

func newValuePromptModal(theme *tui.Theme, title, hint, prefill string, requestRender func()) *valuePromptModal {
	m := &valuePromptModal{}
	m.editor = tui.NewEditor(nil, tui.EditorTheme{BorderColor: theme.FgFn(roleBorderAccent)}, 0)
	m.editor.SetFocused(true)
	if prefill != "" {
		m.editor.SetText(prefill)
	}
	m.editor.OnSubmit = func(text string) {
		if m.OnDone != nil {
			m.OnDone(text, true)
		}
	}
	m.AddChild(tui.NewSpacer(1))
	m.AddChild(tui.NewDynamicBorder(theme.FgFn(roleBorderAccent)))
	m.AddChild(tui.NewText(theme.Fg(roleAccent, theme.Bold(title)), 1, 0, nil))
	m.AddChild(tui.NewText(theme.Fg(roleMuted, hint), 1, 0, nil))
	m.AddChild(m.editor)
	m.AddChild(tui.NewText(theme.Fg(roleDim, "enter confirm · esc back to the providers"), 1, 0, nil))
	m.AddChild(tui.NewDynamicBorder(theme.FgFn(roleBorderAccent)))
	_ = requestRender
	return m
}

// HandleInput routes to the editor; escape ends the prompt instead.
func (m *valuePromptModal) HandleInput(data []byte) {
	if key, ok := tui.ParseKey(data); ok {
		switch key.String() {
		case "escape", "ctrl+c":
			if m.OnDone != nil {
				m.OnDone("", false)
			}
			return
		}
	}
	m.editor.HandleInput(data)
}

// InsertPaste puts a pasted key or address into the editor.
func (m *valuePromptModal) InsertPaste(body string) { m.editor.InsertPaste(body) }

// maybeOpenProviderPicker opens the picker on a console that has no provider
// that can answer: the first run, before anything is typed. Over --remote the
// server's configuration decides and nothing opens here.
func (a *App) maybeOpenProviderPicker() {
	if !a.needsLogin() || a.modal != nil || a.login != nil {
		return
	}
	a.openProviderPicker()
}
