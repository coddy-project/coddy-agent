package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// UCI-style staged configuration editing. Paths are dotted like OpenWrt's uci
// CLI ("agent.max_turns", "mcp_servers[name=context7].command") and edits are
// expressed as commands (set / add_list / del_list / delete) that are staged by
// the session and only applied together by CommitUCICommands.

// UCIOp names one supported command verb.
const (
	UCIOpSet     = "set"
	UCIOpAddList = "add_list"
	UCIOpDelList = "del_list"
	UCIOpDelete  = "delete"
)

// ErrConfigStateUncertain marks transaction failures where the automatic file
// restore also failed: the on-disk configuration may match neither the
// previous nor the new state, so callers must not assume a clean revert.
var ErrConfigStateUncertain = errors.New("config state is uncertain")

// UCICommand is one staged configuration edit in uci-like notation.
type UCICommand struct {
	Op    string
	Path  string
	Value string
}

// String renders the command in its canonical single-line form.
func (c UCICommand) String() string {
	if c.Op == UCIOpDelete {
		return UCIOpDelete + " " + c.Path
	}
	return c.Op + " " + c.Path + "=" + c.Value
}

// RedactedString renders the command for display (permission prompts, change
// listings), masking values addressed at secret-shaped paths and secret-shaped
// fields inside JSON object values. The stored command keeps the original value.
func (c UCICommand) RedactedString() string {
	if c.Op == UCIOpDelete {
		return c.String()
	}
	var keys []string
	if tokens, err := parseDottedConfigPath(c.Path); err == nil {
		keys = configPathKeys(tokens)
	}
	if configSecretPath(keys) {
		return c.Op + " " + c.Path + "=" + redactedConfigValue
	}
	var decoded interface{}
	if json.Unmarshal([]byte(c.Value), &decoded) == nil && redactSecretJSON(&decoded, keys) {
		// A plain Encoder keeps "<redacted>" readable instead of <-escaping it.
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(decoded); err == nil {
			return c.Op + " " + c.Path + "=" + strings.TrimRight(buf.String(), "\n")
		}
	}
	return c.String()
}

// redactSecretJSON masks secret-shaped fields inside a decoded JSON value.
func redactSecretJSON(v *interface{}, path []string) bool {
	switch node := (*v).(type) {
	case map[string]interface{}:
		changed := false
		for key, child := range node {
			next := appendPath(path, key)
			if configSecretPath(next) {
				node[key] = redactedConfigValue
				changed = true
				continue
			}
			c := child
			if redactSecretJSON(&c, next) {
				node[key] = c
				changed = true
			}
		}
		return changed
	case []interface{}:
		changed := false
		for i := range node {
			c := node[i]
			if redactSecretJSON(&c, path) {
				node[i] = c
				changed = true
			}
		}
		return changed
	}
	return false
}

// ParseUCICommand parses one "verb path[=value]" line.
func ParseUCICommand(line string) (UCICommand, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return UCICommand{}, fmt.Errorf("config command is empty")
	}
	verb, rest, found := strings.Cut(trimmed, " ")
	if !found {
		return UCICommand{}, fmt.Errorf("config command %q must be \"<verb> <path>[=<value>]\"", trimmed)
	}
	rest = strings.TrimSpace(rest)
	cmd := UCICommand{Op: verb}
	switch verb {
	case UCIOpDelete:
		cmd.Path = rest
	case UCIOpSet, UCIOpAddList, UCIOpDelList:
		path, value, err := splitUCIAssignment(rest)
		if err != nil {
			return UCICommand{}, fmt.Errorf("config command %q: %w", trimmed, err)
		}
		cmd.Path = path
		cmd.Value = value
	default:
		return UCICommand{}, fmt.Errorf("config command verb %q is not supported (set, add_list, del_list, delete)", verb)
	}
	if _, err := parseDottedConfigPath(cmd.Path); err != nil {
		return UCICommand{}, fmt.Errorf("config command %q: %w", trimmed, err)
	}
	return cmd, nil
}

// ParseUCICommands parses a batch, keeping line order.
func ParseUCICommands(lines []string) ([]UCICommand, error) {
	cmds := make([]UCICommand, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cmd, err := ParseUCICommand(line)
		if err != nil {
			return nil, err
		}
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil, fmt.Errorf("no config commands were given")
	}
	return cmds, nil
}

// splitUCIAssignment cuts "path=value" at the first "=" outside a [selector].
func splitUCIAssignment(input string) (string, string, error) {
	depth := 0
	for i := 0; i < len(input); i++ {
		switch input[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 {
				path := strings.TrimSpace(input[:i])
				value := strings.TrimSpace(input[i+1:])
				if path == "" {
					return "", "", fmt.Errorf("path before \"=\" is empty")
				}
				return path, unquoteUCIValue(value), nil
			}
		}
	}
	return "", "", fmt.Errorf("expected \"<path>=<value>\"")
}

// unquoteUCIValue strips one pair of matching surrounding quotes (uci style).
func unquoteUCIValue(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if first == last && (first == '\'' || first == '"') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// parseDottedConfigPath tokenizes a uci-like dotted path. Selector segments
// ("mcp_servers[name=context7]") reuse the slash-path selector grammar, and
// dots inside a selector belong to the selector value.
func parseDottedConfigPath(input string) ([]configPathToken, error) {
	path := strings.TrimSpace(input)
	if path == "" || path == "." {
		return nil, nil
	}
	var tokens []configPathToken
	depth := 0
	start := 0
	flush := func(end int) error {
		part := path[start:end]
		if part == "" {
			return fmt.Errorf("config path %q contains an empty segment", input)
		}
		token, err := parseConfigSegment(part)
		if err != nil {
			return fmt.Errorf("config path %q: %w", input, err)
		}
		tokens = append(tokens, token)
		return nil
	}
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '.':
			if depth == 0 {
				if err := flush(i); err != nil {
					return nil, err
				}
				start = i + 1
			}
		}
	}
	if err := flush(len(path)); err != nil {
		return nil, err
	}
	return tokens, nil
}

// parseAnyConfigPath accepts both dotted uci paths and legacy slash paths.
func parseAnyConfigPath(input string) ([]configPathToken, error) {
	trimmed := strings.TrimSpace(input)
	if strings.HasPrefix(trimmed, "/") {
		return parseConfigPath(trimmed)
	}
	return parseDottedConfigPath(trimmed)
}

// applyUCICommand mutates the parsed config document according to cmd.
func applyUCICommand(root *yaml.Node, cmd UCICommand) error {
	tokens, err := parseDottedConfigPath(cmd.Path)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("config root cannot be edited; address a specific path")
	}
	switch cmd.Op {
	case UCIOpSet:
		target, err := configSchemaType(tokens)
		if err != nil {
			return err
		}
		replacement, err := decodeUCIValue(cmd.Value, target)
		if err != nil {
			return fmt.Errorf("%s: %w", cmd.RedactedString(), err)
		}
		if _, err := mutateConfigNode(root, tokens, replacement, false); err != nil {
			return fmt.Errorf("%s: %w", cmd.RedactedString(), err)
		}
		return nil
	case UCIOpDelete:
		if _, err := configSchemaType(tokens); err != nil {
			return err
		}
		mutated, err := mutateConfigNode(root, tokens, nil, true)
		if err != nil {
			return fmt.Errorf("%s: %w", cmd.RedactedString(), err)
		}
		if !mutated {
			return fmt.Errorf("%s: path does not exist", cmd.RedactedString())
		}
		return nil
	case UCIOpAddList:
		appendTokens := append(append([]configPathToken(nil), tokens...), configPathToken{key: "-"})
		target, err := configSchemaType(appendTokens)
		if err != nil {
			return err
		}
		replacement, err := decodeUCIValue(cmd.Value, target)
		if err != nil {
			return fmt.Errorf("%s: %w", cmd.RedactedString(), err)
		}
		if _, err := mutateConfigNode(root, appendTokens, replacement, false); err != nil {
			return fmt.Errorf("%s: %w", cmd.RedactedString(), err)
		}
		return nil
	case UCIOpDelList:
		if _, err := configSchemaType(append(append([]configPathToken(nil), tokens...), configPathToken{key: "0"})); err != nil {
			return err
		}
		node, exists, err := findConfigNode(root, tokens)
		if err != nil {
			return fmt.Errorf("%s: %w", cmd.RedactedString(), err)
		}
		if !exists || node.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s: path is not an existing list", cmd.RedactedString())
		}
		kept := node.Content[:0]
		removed := 0
		for _, entry := range node.Content {
			if entry.Kind == yaml.ScalarNode && entry.Value == cmd.Value {
				removed++
				continue
			}
			kept = append(kept, entry)
		}
		if removed == 0 {
			return fmt.Errorf("%s: no list entry equals %q", cmd.RedactedString(), cmd.Value)
		}
		node.Content = kept
		return nil
	default:
		return fmt.Errorf("config command verb %q is not supported", cmd.Op)
	}
}

// decodeUCIValue turns raw command text into a YAML node for the target field.
// String-typed fields always receive the literal text; other fields parse the
// text as JSON first (objects, arrays, numbers, booleans) and fall back to a
// plain string scalar.
func decodeUCIValue(raw string, target reflect.Type) (*yaml.Node, error) {
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target != nil && target.Kind() == reflect.String {
		return scalarYAMLNode(raw), nil
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		decoded = raw
	}
	node := &yaml.Node{}
	if err := node.Encode(decoded); err != nil {
		return nil, fmt.Errorf("encode config value: %w", err)
	}
	return node, nil
}

// applyUCICommandsToBytes applies the batch to raw YAML and revalidates the
// resulting typed config without touching any file.
func applyUCICommandsToBytes(paths Paths, base []byte, cmds []UCICommand) ([]byte, error) {
	if len(bytes.TrimSpace(base)) == 0 {
		base = []byte("{}\n")
	}
	doc, err := parseConfigDocument(base)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	root := configDocumentRoot(doc)
	for _, cmd := range cmds {
		if err := applyUCICommand(root, cmd); err != nil {
			return nil, err
		}
	}
	updated, err := marshalConfigDocument(doc)
	if err != nil {
		return nil, fmt.Errorf("serialize config: %w", err)
	}
	expanded := expandEnvEscaped(ExpandPathVars(string(updated), paths))
	if _, err := parseValidateYAMLBytes(expanded, paths); err != nil {
		return nil, fmt.Errorf("resulting config is invalid: %w", err)
	}
	return updated, nil
}

// DryRunUCICommands validates that the staged batch applies cleanly to the
// active file as it is right now. Nothing is written.
func DryRunUCICommands(paths Paths, cmds []UCICommand) error {
	raw, _, err := readExistingConfigBytes(paths.ConfigPath)
	if err != nil {
		return err
	}
	_, err = applyUCICommandsToBytes(paths, raw, cmds)
	return err
}

// ConfigCommitResult records a committed batch and can restore the previous files.
type ConfigCommitResult struct {
	Config       *Config
	Changed      bool
	Applied      []string
	SnapshotPath string

	paths        Paths
	previous     []byte
	existed      bool
	prevSnapshot []byte
	prevExisted  bool
}

// Rollback restores config.yaml and its pre-commit snapshot as they existed
// before CommitUCICommands. Used when the runtime reload of the new file fails.
func (r *ConfigCommitResult) Rollback() error {
	if r == nil {
		return nil
	}
	configPathWriteMu.Lock()
	defer configPathWriteMu.Unlock()
	return r.rollbackLocked()
}

// rollbackLocked restores the files while the caller already holds
// configPathWriteMu; the transaction functions use it on their internal
// failure paths, where taking the non-reentrant mutex again would deadlock.
func (r *ConfigCommitResult) rollbackLocked() error {
	var restoreErr error
	if r.existed {
		restoreErr = errors.Join(restoreErr, AtomicWriteConfigYAML(r.paths.ConfigPath, r.previous))
	} else if err := os.Remove(r.paths.ConfigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		restoreErr = errors.Join(restoreErr, err)
	}
	snapshot := PrevConfigPath(r.paths.ConfigPath)
	if r.prevExisted {
		restoreErr = errors.Join(restoreErr, atomicWriteFile(snapshot, r.prevSnapshot, 0o600))
	} else if err := os.Remove(snapshot); err != nil && !errors.Is(err, os.ErrNotExist) {
		restoreErr = errors.Join(restoreErr, err)
	}
	return restoreErr
}

// CommitUCICommands applies the staged batch to the active YAML config:
// validate, snapshot the previous file to config.yaml.prev, write atomically,
// and reload the typed config from disk.
func CommitUCICommands(paths Paths, cmds []UCICommand) (*ConfigCommitResult, error) {
	configPathWriteMu.Lock()
	defer configPathWriteMu.Unlock()
	return commitUCICommandsLocked(paths, cmds)
}

// CommitUCICommandsAndReload runs the whole commit transaction - disk
// mutation AND runtime application via reload - under the config file lock,
// so no other writer can interleave between the write and the moment the
// runtime picks it up. On reload failure the files are restored (and reload is
// invoked again to re-apply them); if that restore fails the error wraps
// ErrConfigStateUncertain.
func CommitUCICommandsAndReload(paths Paths, cmds []UCICommand, reload func() ([]string, error)) (*ConfigCommitResult, []string, error) {
	configPathWriteMu.Lock()
	defer configPathWriteMu.Unlock()
	result, err := commitUCICommandsLocked(paths, cmds)
	if err != nil {
		return nil, nil, err
	}
	warnings, err := reload()
	if err != nil {
		rbErr := result.rollbackLocked()
		_, restoreErr := reload()
		if rbErr != nil || restoreErr != nil {
			return nil, nil, fmt.Errorf("reload committed config: %w (rollback: %v; restore reload: %v): %w", err, rbErr, restoreErr, ErrConfigStateUncertain)
		}
		return nil, nil, fmt.Errorf("reload committed config: %w (change rolled back)", err)
	}
	return result, warnings, nil
}

// commitUCICommandsLocked is the commit body; callers hold configPathWriteMu.
func commitUCICommandsLocked(paths Paths, cmds []UCICommand) (*ConfigCommitResult, error) {
	if len(cmds) == 0 {
		return nil, fmt.Errorf("no staged config commands to commit")
	}
	previous, existed, err := readExistingConfigBytes(paths.ConfigPath)
	if err != nil {
		return nil, err
	}
	prevSnapshot, prevExisted, err := readExistingConfigBytes(PrevConfigPath(paths.ConfigPath))
	if err != nil {
		return nil, err
	}
	result := &ConfigCommitResult{
		SnapshotPath: PrevConfigPath(paths.ConfigPath),
		paths:        paths,
		previous:     append([]byte(nil), previous...),
		existed:      existed,
		prevSnapshot: append([]byte(nil), prevSnapshot...),
		prevExisted:  prevExisted,
	}
	updated, err := applyUCICommandsToBytes(paths, previous, cmds)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigPath), 0o755); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	if existed {
		if err := WriteBackup(paths.ConfigPath, previous); err != nil {
			return nil, fmt.Errorf("backup config: %w", err)
		}
	}
	// The snapshot is written even for the first commit into a missing file
	// (as an empty document), so config_rollback can always return to the
	// pre-commit state.
	if err := atomicWriteFile(PrevConfigPath(paths.ConfigPath), previous, 0o600); err != nil {
		return nil, fmt.Errorf("snapshot config: %w", err)
	}
	if err := AtomicWriteConfigYAML(paths.ConfigPath, updated); err != nil {
		if rbErr := result.rollbackLocked(); rbErr != nil {
			return nil, fmt.Errorf("write config: %w (restore failed: %v): %w", err, rbErr, ErrConfigStateUncertain)
		}
		return nil, fmt.Errorf("write config: %w (previous files restored)", err)
	}
	result.Changed = !existed || !bytes.Equal(previous, updated)
	for _, cmd := range cmds {
		result.Applied = append(result.Applied, cmd.RedactedString())
	}
	reloaded, err := loadWithPathsLocked(paths)
	if err != nil {
		if rbErr := result.rollbackLocked(); rbErr != nil {
			return nil, fmt.Errorf("reload config: %w (restore failed: %v): %w", err, rbErr, ErrConfigStateUncertain)
		}
		return nil, fmt.Errorf("reload config: %w (previous files restored)", err)
	}
	result.Config = reloaded
	return result, nil
}

// loadWithPathsLocked reloads the typed config while configPathWriteMu is held.
func loadWithPathsLocked(paths Paths) (*Config, error) {
	return LoadWithPaths(paths)
}

// WithConfigFileLock runs fn while holding the process-wide config file write
// lock shared with the staged commit and rollback transactions. Every writer
// of the active config file (agent config tools, the HTTP PUT handler) must
// serialize on this mutex, or concurrent writes can interleave backup, write,
// and reload steps of two transactions.
func WithConfigFileLock(fn func() error) error {
	configPathWriteMu.Lock()
	defer configPathWriteMu.Unlock()
	return fn()
}

// ConfigRollbackResult records a snapshot restore and can undo it.
type ConfigRollbackResult struct {
	Config       *Config
	SnapshotPath string

	paths    Paths
	restored []byte // bytes now in config.yaml (the old snapshot)
	replaced []byte // bytes that were config.yaml before the rollback
}

// Rollback undoes the snapshot restore (used when the runtime reload fails).
func (r *ConfigRollbackResult) Rollback() error {
	if r == nil {
		return nil
	}
	configPathWriteMu.Lock()
	defer configPathWriteMu.Unlock()
	return r.rollbackLocked()
}

// rollbackLocked undoes the swap while the caller already holds configPathWriteMu.
func (r *ConfigRollbackResult) rollbackLocked() error {
	var restoreErr error
	restoreErr = errors.Join(restoreErr, AtomicWriteConfigYAML(r.paths.ConfigPath, r.replaced))
	restoreErr = errors.Join(restoreErr, atomicWriteFile(r.SnapshotPath, r.restored, 0o600))
	return restoreErr
}

// RollbackConfigFromSnapshot swaps the active config with the pre-commit
// snapshot written by CommitUCICommands, validating the snapshot first. The
// replaced file goes back into the snapshot slot so a second rollback restores
// it again.
func RollbackConfigFromSnapshot(paths Paths) (*ConfigRollbackResult, error) {
	configPathWriteMu.Lock()
	defer configPathWriteMu.Unlock()
	return rollbackConfigFromSnapshotLocked(paths)
}

// RollbackConfigFromSnapshotAndReload runs the whole snapshot restore - disk
// swap AND runtime application via reload - under the config file lock. On
// reload failure the swap is undone (and reload re-applies the undone files);
// if that undo fails the error wraps ErrConfigStateUncertain.
func RollbackConfigFromSnapshotAndReload(paths Paths, reload func() ([]string, error)) (*ConfigRollbackResult, []string, error) {
	configPathWriteMu.Lock()
	defer configPathWriteMu.Unlock()
	result, err := rollbackConfigFromSnapshotLocked(paths)
	if err != nil {
		return nil, nil, err
	}
	warnings, err := reload()
	if err != nil {
		undoErr := result.rollbackLocked()
		_, restoreErr := reload()
		if undoErr != nil || restoreErr != nil {
			return nil, nil, fmt.Errorf("reload restored config: %w (undo: %v; restore reload: %v): %w", err, undoErr, restoreErr, ErrConfigStateUncertain)
		}
		return nil, nil, fmt.Errorf("reload restored config: %w (rollback undone)", err)
	}
	return result, warnings, nil
}

// rollbackConfigFromSnapshotLocked is the restore body; callers hold configPathWriteMu.
func rollbackConfigFromSnapshotLocked(paths Paths) (*ConfigRollbackResult, error) {
	snapshotPath := PrevConfigPath(paths.ConfigPath)
	snapshot, snapshotExists, err := readExistingConfigBytes(snapshotPath)
	if err != nil {
		return nil, err
	}
	if !snapshotExists {
		return nil, fmt.Errorf("no pre-commit snapshot next to %s; nothing to roll back to", paths.ConfigPath)
	}
	current, currentExists, err := readExistingConfigBytes(paths.ConfigPath)
	if err != nil {
		return nil, err
	}
	if !currentExists {
		current = []byte{}
	}
	expanded := expandEnvEscaped(ExpandPathVars(string(snapshot), paths))
	if _, err := parseValidateYAMLBytes(expanded, paths); err != nil {
		return nil, fmt.Errorf("pre-commit snapshot %s is invalid: %w", snapshotPath, err)
	}
	result := &ConfigRollbackResult{
		SnapshotPath: snapshotPath,
		paths:        paths,
		restored:     append([]byte(nil), snapshot...),
		replaced:     append([]byte(nil), current...),
	}
	if err := AtomicWriteConfigYAML(paths.ConfigPath, snapshot); err != nil {
		return nil, fmt.Errorf("restore config: %w", err)
	}
	if err := atomicWriteFile(snapshotPath, current, 0o600); err != nil {
		if rbErr := AtomicWriteConfigYAML(paths.ConfigPath, current); rbErr != nil {
			return nil, fmt.Errorf("swap snapshot: %w (restore failed: %v): %w", err, rbErr, ErrConfigStateUncertain)
		}
		return nil, fmt.Errorf("swap snapshot: %w (previous config restored)", err)
	}
	reloaded, err := loadWithPathsLocked(paths)
	if err != nil {
		if rbErr := result.rollbackLocked(); rbErr != nil {
			return nil, fmt.Errorf("reload restored config: %w (undo failed: %v): %w", err, rbErr, ErrConfigStateUncertain)
		}
		return nil, fmt.Errorf("reload restored config: %w (swap undone)", err)
	}
	result.Config = reloaded
	return result, nil
}
