package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Keys that left config.yaml move on load into the files that hold them now,
// so an old config keeps working after an update without anyone editing it.
// The move runs under the config file lock and only when it can take it: a
// load inside a config transaction (which holds the lock) leaves it for the
// next load. The key's block is cut out of the text; the rest of the file
// stays byte for byte, and the old file is kept beside it as
// <config>.bak-<time>.

// legacyMove is one key that left config.yaml: where it sits in the file and
// how its value is carried into the file that holds it now. move reports
// what it moved and what the target had already, for the log line.
type legacyMove struct {
	path  string
	key   *yaml.Node
	value *yaml.Node
	move  func(value *yaml.Node) (moved, kept []string, err error)
}

// migrateLegacyKeys moves the keys that left config.yaml: the old top-level
// mcp_servers into <home>/mcp.json, every value meaning what it meant in
// config.yaml (legacyMCPValue: an environment reference stays one, which
// mcp.json resolves when the server starts), a name the file declares
// already left alone; and skills.sources into the sources of
// <home>/marketplaces.json, a source the file has already (in any case) and
// the system source not repeated. A move that cannot write its target leaves
// its key where it is; the others go ahead, in one rewrite of config.yaml
// with one backup of the old file.
func migrateLegacyKeys(paths Paths, data []byte) []byte {
	if strings.TrimSpace(paths.ConfigPath) == "" {
		return data
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data
	}
	root := doc.Content[0]
	var moves []legacyMove
	if k, v := mappingEntry(root, "mcp_servers"); k != nil {
		moves = append(moves, legacyMove{path: "mcp_servers", key: k, value: v,
			move: func(v *yaml.Node) ([]string, []string, error) { return moveLegacyMCPServers(paths, v) }})
	}
	if _, skills := mappingEntry(root, "skills"); skills != nil && skills.Kind == yaml.MappingNode {
		if k, v := mappingEntry(skills, "sources"); k != nil {
			moves = append(moves, legacyMove{path: "skills.sources", key: k, value: v,
				move: func(v *yaml.Node) ([]string, []string, error) { return moveLegacySkillSources(paths, v) }})
		}
	}
	if _, sched := mappingEntry(root, "scheduler"); sched != nil && sched.Kind == yaml.MappingNode {
		if k, v := mappingEntry(sched, "dir"); k != nil {
			moves = append(moves, legacyMove{path: "scheduler.dir", key: k, value: v,
				move: func(v *yaml.Node) ([]string, []string, error) { return moveLegacySchedulerDir(paths, v) }})
		}
	}
	if len(moves) == 0 {
		return data
	}
	if paths.ConfigFromWorkspace {
		// A config.yaml found in the workspace may have come with a checkout:
		// moving its servers or sources into the home files would make them
		// the operator's own, ungated in every workspace. It is read as it is.
		for _, m := range moves {
			slog.Default().Warn("config: the workspace's config.yaml, read because the Coddy home has none, still has a key that left config.yaml; it is not used and not moved out of a file that may have come with a checkout", "key", m.path, "config", paths.ConfigPath, "home", paths.Home)
		}
		return data
	}
	if !configPathWriteMu.TryLock() {
		return data
	}
	defer configPathWriteMu.Unlock()

	log := slog.Default()
	var done []legacyMove
	for _, m := range moves {
		moved, kept, err := m.move(m.value)
		if err != nil {
			log.Warn("config: a key that left config.yaml could not be moved; left in place and not used", "key", m.path, "path", paths.ConfigPath, "error", err)
			continue
		}
		log.Info("config: moved a key out of config.yaml", "key", m.path, "moved", moved, "already_there", kept, "config", paths.ConfigPath)
		// The cut is by lines. A key inside a flow-style mapping, or a flow
		// value whose closing bracket shares the key's indentation, is not a
		// block of lines: cutting it would take its neighbours along or leave
		// a bracket behind. What it held is moved all the same (a repeat is
		// kept as it is the next time); the key stays for the operator.
		if !cutKeepsTheRest(data, cutKeyBlocks(data, root, m.key), [][]string{strings.Split(m.path, ".")}) {
			log.Warn("config: moved a key out of config.yaml but it cannot be cut out of the file without touching its neighbours; delete it by hand", "key", m.path, "config", paths.ConfigPath)
			continue
		}
		done = append(done, m)
	}
	if len(done) == 0 {
		return data
	}
	// Every block is measured on the text as it was read and all are cut in
	// one pass, so no cut is measured on lines another one moved.
	keys := make([]*yaml.Node, 0, len(done))
	cut := make([][]string, 0, len(done))
	for _, m := range done {
		keys = append(keys, m.key)
		cut = append(cut, strings.Split(m.path, "."))
	}
	next := cutKeyBlocks(data, root, keys...)
	if !cutKeepsTheRest(data, next, cut) {
		log.Warn("config: keys moved but config.yaml would not read the same without them; left as it is", "config", paths.ConfigPath)
		return data
	}
	if err := rewriteConfigKeepingBackup(paths.ConfigPath, data, next); err != nil {
		log.Warn("config: keys moved but config.yaml could not be rewritten; they are ignored", "path", paths.ConfigPath, "error", err)
		return data
	}
	return next
}

// cutKeepsTheRest reports whether next, data with the keys at paths cut out
// of its text, reads as exactly the document data reads as without them. A
// mapping left without keys reads as an empty one either way.
func cutKeepsTheRest(data, next []byte, paths [][]string) bool {
	var want, got any
	if yaml.Unmarshal(data, &want) != nil || yaml.Unmarshal(next, &got) != nil {
		return false
	}
	for _, path := range paths {
		dropYAMLPath(want, path)
	}
	return reflect.DeepEqual(emptyMapsAsNil(want), emptyMapsAsNil(got))
}

// dropYAMLPath deletes the key path names from a decoded document.
func dropYAMLPath(doc any, path []string) {
	m, ok := doc.(map[string]any)
	if !ok || len(path) == 0 {
		return
	}
	if len(path) == 1 {
		delete(m, path[0])
		return
	}
	dropYAMLPath(m[path[0]], path[1:])
}

// emptyMapsAsNil makes a mapping with no keys and an empty value the same,
// as `skills:` left with nothing under it reads as null.
func emptyMapsAsNil(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = emptyMapsAsNil(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = emptyMapsAsNil(val)
		}
		return out
	default:
		return v
	}
}

// mappingEntry finds key in a mapping node, returning its key and value nodes.
func mappingEntry(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// moveLegacyMCPServers writes the servers of an old mcp_servers list into
// <home>/mcp.json, leaving a name the file declares already alone.
func moveLegacyMCPServers(paths Paths, value *yaml.Node) (moved, kept []string, err error) {
	var servers []MCPServerConfig
	if err := value.Decode(&servers); err != nil {
		return nil, nil, fmt.Errorf("mcp_servers does not read as a list of servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, nil, nil
	}
	target := GlobalMCPJSONPath(paths.Home)
	entries, err := ReadMCPJSONFile(target)
	if err != nil {
		return nil, nil, err
	}
	for _, srv := range servers {
		name := strings.TrimSpace(srv.Name)
		if name == "" {
			continue
		}
		if _, ok := entries[name]; ok {
			kept = append(kept, name)
			continue
		}
		entries[name] = legacyMCPServerToJSON(srv, paths.Home)
		moved = append(moved, name)
	}
	if len(moved) > 0 {
		if err := writeMCPJSONFileEntries(target, entries); err != nil {
			return nil, nil, err
		}
	}
	return moved, kept, nil
}

// moveLegacySkillSources appends the sources of an old skills.sources list to
// the sources of <home>/marketplaces.json. A source the file has already, in
// any case, and the system source, which is in effect without any file, are
// not repeated.
func moveLegacySkillSources(paths Paths, value *yaml.Node) (moved, kept []string, err error) {
	var sources []string
	if err := value.Decode(&sources); err != nil {
		return nil, nil, fmt.Errorf("skills.sources does not read as a list of sources: %w", err)
	}
	target := GlobalMarketplacesPath(paths.Home)
	file, err := ReadMarketplacesFile(target)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]bool{strings.ToLower(SystemSkillsSource): true}
	for _, s := range file.Sources {
		known[strings.ToLower(strings.TrimSpace(s))] = true
	}
	for _, s := range sources {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if known[strings.ToLower(s)] {
			kept = append(kept, s)
			continue
		}
		known[strings.ToLower(s)] = true
		file.Sources = append(file.Sources, s)
		moved = append(moved, s)
	}
	if len(moved) > 0 {
		if err := WriteMarketplacesFile(target, file); err != nil {
			return nil, nil, err
		}
	}
	return moved, kept, nil
}

// moveLegacySchedulerDir carries the jobs of an old scheduler.dir into the
// fixed user jobs folder, ${CODDY_HOME}/scheduler. Each *.md job and its .state
// sidecar is copied, never moved: the old folder stays as it was, so nothing
// is lost when it was shared or kept under version control. A job the target
// has already with the same bytes is skipped and reported as kept; one it has
// with other bytes is copied as <id>-migrated.md (with <id>-migrated.state),
// so no job silently stops running. A folder that is the target itself, or
// that does not exist, has nothing to carry.
func moveLegacySchedulerDir(paths Paths, value *yaml.Node) (moved, kept []string, err error) {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return nil, nil, fmt.Errorf("scheduler.dir does not read as a path: %w", err)
	}
	raw = strings.TrimSpace(raw)
	target := SchedulerUserDirFor(paths)
	if raw == "" || target == "" {
		return nil, nil, nil
	}
	src := filepath.Clean(ExpandPathVars(raw, paths))
	if sameDir(src, target) {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".md") {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		dstBase := base
		if existing, err := os.ReadFile(filepath.Join(target, name)); err == nil {
			mine, err := os.ReadFile(filepath.Join(src, name))
			if err != nil {
				return moved, kept, err
			}
			if string(existing) == string(mine) {
				// Same job: carry its checkpoint along when the home has
				// none yet.
				if _, err := os.Stat(filepath.Join(target, base+".state")); os.IsNotExist(err) {
					if err := copyRegularFile(filepath.Join(src, base+".state"), filepath.Join(target, base+".state")); err != nil && !os.IsNotExist(err) {
						return moved, kept, err
					}
				}
				kept = append(kept, name)
				continue
			}
			dstBase = freeMigratedBase(target, base)
		}
		if err := copyRegularFile(filepath.Join(src, name), filepath.Join(target, dstBase+".md")); err != nil {
			return moved, kept, err
		}
		if _, err := os.Stat(filepath.Join(target, dstBase+".state")); os.IsNotExist(err) {
			if err := copyRegularFile(filepath.Join(src, base+".state"), filepath.Join(target, dstBase+".state")); err != nil && !os.IsNotExist(err) {
				return moved, kept, err
			}
		}
		moved = append(moved, dstBase+".md")
	}
	return moved, kept, nil
}

// freeMigratedBase is the first <base>-migrated, <base>-migrated-2, ... name
// with no job file in target.
func freeMigratedBase(target, base string) string {
	for i := 1; ; i++ {
		name := base + "-migrated"
		if i > 1 {
			name = fmt.Sprintf("%s-migrated-%d", base, i)
		}
		if _, err := os.Stat(filepath.Join(target, name+".md")); os.IsNotExist(err) {
			return name
		}
	}
}

// sameDir reports whether two folder paths name the same folder, links
// resolved where they exist.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// copyRegularFile copies src to dst, creating dst's folder; dst must not
// exist yet.
func copyRegularFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// legacyMCPServerToJSON is the mcp.json entry of a declaration read from the
// raw text of config.yaml, every value carried over by legacyMCPValue.
func legacyMCPServerToJSON(s MCPServerConfig, home string) MCPJSONServer {
	value := func(v string) string { return legacyMCPValue(v, home) }
	s.Command, s.URL = value(s.Command), value(s.URL)
	s.Args = append([]string(nil), s.Args...)
	for i := range s.Args {
		s.Args[i] = value(s.Args[i])
	}
	s.Env = append([]EnvVarConfig(nil), s.Env...)
	for i := range s.Env {
		s.Env[i].Value = value(s.Env[i].Value)
	}
	s.Headers = append([]HTTPHeaderConfig(nil), s.Headers...)
	for i := range s.Headers {
		s.Headers[i].Value = value(s.Headers[i].Value)
	}
	return MCPJSONFromServer(s)
}

// legacyMCPValue rewrites one value as config.yaml wrote it into the
// mcp.json spelling that starts the server with the same value
// (ExpandMCPValue): config.yaml expanded $NAME and ${NAME} from the
// environment when it loaded, read "$$" as a literal "$" and put the home in
// for ${CODDY_HOME}. A reference stays a reference, so a secret kept in the
// environment is not written into the file; the home is written out.
func legacyMCPValue(raw, home string) string {
	s := strings.ReplaceAll(raw, "$$", escapedDollarSentinel)
	s = strings.ReplaceAll(s, "${CODDY_HOME}", yamlSafePath(home))
	s = strings.ReplaceAll(s, sessionCWDPlaceholder, sessionCWDSentinel)
	s = os.Expand(s, func(name string) string { return "${" + name + "}" })
	s = strings.ReplaceAll(s, sessionCWDSentinel, sessionCWDPlaceholder)
	// A literal "$" before a brace would read as a reference in mcp.json,
	// where "$${" is the escape for it.
	s = strings.ReplaceAll(s, escapedDollarSentinel+"{", "$${")
	return strings.ReplaceAll(s, escapedDollarSentinel, "$")
}

// rewriteConfigKeepingBackup writes next over the config, keeping the old
// bytes as <config>.bak-<time> with the config's own permissions.
func rewriteConfigKeepingBackup(path string, old, next []byte) error {
	perm := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	backup := fmt.Sprintf("%s.bak-%s", path, time.Now().Format("20060102-150405.000000000"))
	if err := os.WriteFile(backup, old, perm); err != nil {
		return fmt.Errorf("backup %s: %w", backup, err)
	}
	return atomicWriteFile(path, next, perm)
}

// cutKeyBlocks cuts mapping keys out of raw YAML text, each with its block
// (keyBlock), measured on raw itself: root is raw's parsed document.
func cutKeyBlocks(raw []byte, root *yaml.Node, keys ...*yaml.Node) []byte {
	lines := strings.SplitAfter(string(raw), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	drop := make([]bool, len(lines))
	for _, key := range keys {
		start, end, ok := keyBlock(lines, root, key)
		if !ok {
			continue
		}
		for i := start; i < end; i++ {
			drop[i] = true
		}
	}
	var b strings.Builder
	for i, line := range lines {
		if !drop[i] {
			b.WriteString(line)
		}
	}
	return []byte(b.String())
}

// keyBlock is the range of lines [start, end) one mapping key takes: the
// comment lines right above it at its indentation (its description; not the
// schema modeline), its line, and everything up to the next key the parser
// placed at its indentation or left of it, less the blank lines and the
// comment lines at that key's indentation that come right before it (the
// next key's description). Ending at the next key rather than at the first
// line that looks shallower keeps a comment at column 0 between list items,
// a sequence written without indentation and a closing bracket at the key's
// indentation inside the block.
func keyBlock(lines []string, root, key *yaml.Node) (int, int, bool) {
	start := key.Line - 1
	col := key.Column - 1
	if start < 0 || start >= len(lines) {
		return 0, 0, false
	}
	end, nextCol := len(lines), -1
	walkYAMLKeys(root, func(k *yaml.Node) {
		if k.Line > key.Line && k.Column <= key.Column && k.Line-1 < end {
			end, nextCol = k.Line-1, k.Column-1
		}
	})
	for end > start+1 {
		prev := strings.TrimRight(lines[end-1], "\r\n")
		indent := len(prev) - len(strings.TrimLeft(prev, " "))
		if strings.TrimSpace(prev) == "" || (nextCol >= 0 && indent == nextCol && strings.HasPrefix(prev[indent:], "#")) {
			end--
			continue
		}
		break
	}
	for start > 0 {
		prev := strings.TrimRight(lines[start-1], "\r\n")
		indent := len(prev) - len(strings.TrimLeft(prev, " "))
		rest := prev[indent:]
		if indent != col || !strings.HasPrefix(rest, "#") || strings.Contains(rest, "yaml-language-server") {
			break
		}
		start--
	}
	// A blank line on both sides of the cut would leave two in a row: the
	// sections around it keep one between them, and the file does not end on
	// a blank line it did not end on before.
	blank := func(i int) bool { return strings.TrimSpace(lines[i]) == "" }
	if start > 0 && blank(start-1) && (end == len(lines) || blank(end)) {
		start--
	}
	return start, end, true
}

// walkYAMLKeys calls fn for every mapping key of the tree under n.
func walkYAMLKeys(n *yaml.Node, fn func(*yaml.Node)) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			fn(n.Content[i])
			walkYAMLKeys(n.Content[i+1], fn)
		}
		return
	}
	for _, c := range n.Content {
		walkYAMLKeys(c, fn)
	}
}
