package skills

// The standard delivery. Coddy carries a set of skills inside its binary and
// hands them to ${CODDY_HOME}/skills, so a fresh install has them without a
// network round trip and a skill that reads files beside its SKILL.md finds
// them on disk. Once handed over they are ordinary managed skills: the operator
// can edit, disable, delete or update them from the marketplace.
//
// What the delivery may and may not do is written down in one receipt,
// .bundled.json, beside the skills it wrote:
//
//   - a skill it has never handed over is written;
//   - a skill it has handed over and the operator then deleted stays deleted;
//   - a copy on disk older than the release is replaced;
//   - a copy that is newer, or that carries no version to compare against, is
//     left exactly as it is;
//   - the marketplace is registered in the config file once, and an operator
//     who removes it keeps it removed.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// deliveryReceiptFile records what the delivery has already handed over. It
// lives beside .remote.json in the managed skills dir.
const deliveryReceiptFile = ".bundled.json"

// deliveryReceipt is the on-disk form of that record. Skills maps a skill name
// to the version handed over for it - an empty value meaning the delivery saw
// the name and deliberately wrote nothing. Sources lists the marketplaces it
// has offered, so one an operator removed is not offered again.
type deliveryReceipt struct {
	Version int               `json:"version"`
	Skills  map[string]string `json:"skills,omitempty"`
	Sources []string          `json:"sources,omitempty"`
}

// SeedResult summarizes one delivery run. An empty result is the normal
// outcome: the delivery only acts the first time it sees something.
type SeedResult struct {
	Installed   []string `json:"installed"`
	Updated     []string `json:"updated"`
	SourceAdded bool     `json:"source_added"`
}

// SeedDelivery hands the standard delivery to the home cfg points at. It is
// idempotent and safe to call at every start; callers treat a failure as
// non-fatal, because the embedded copies still answer through Bundled().
func SeedDelivery(cfg *config.Config) (*SeedResult, error) {
	if cfg == nil {
		return nil, errors.New("nil config")
	}
	syncMu.Lock()
	defer syncMu.Unlock()

	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create managed dir: %w", err)
	}
	receipt := readDeliveryReceipt(managedDir)
	res := &SeedResult{}

	var firstErr error
	changed := false
	for _, entry := range BundledEntries() {
		acted, err := deliverSkill(entry, managedDir, receipt, res)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		changed = changed || acted
	}
	if deliverSource(cfg, receipt, res) {
		changed = true
	}

	if changed {
		if err := writeDeliveryReceipt(managedDir, receipt); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return res, firstErr
}

// deliverSkill applies the delivery rules to one skill and reports whether the
// receipt needs writing.
func deliverSkill(entry BundledEntry, managedDir string, receipt *deliveryReceipt, res *SeedResult) (bool, error) {
	name, err := sanitizeSkillName(entry.Name)
	if err != nil {
		return false, err
	}
	dst := filepath.Join(managedDir, name)
	_, seen := receipt.Skills[name]
	onDisk, present := installedSkillVersion(dst)

	switch {
	case !present && seen:
		// Handed over once and gone since: the operator deleted it.
		return false, nil

	case !present:
		if err := installBundledSkill(entry, managedDir, name); err != nil {
			return false, err
		}
		receipt.Skills[name] = entry.Version
		res.Installed = append(res.Installed, name)
		return true, nil

	case onDisk == "":
		// A copy Coddy cannot compare itself against - hand-written, or from a
		// source that declares no version. Record the name so the question is
		// settled, and leave the files alone.
		if seen {
			return false, nil
		}
		receipt.Skills[name] = ""
		return true, nil

	case entry.Version != "" && compareVersions(entry.Version, onDisk) > 0:
		if err := installBundledSkill(entry, managedDir, name); err != nil {
			return false, err
		}
		receipt.Skills[name] = entry.Version
		res.Updated = append(res.Updated, name)
		return true, nil

	default:
		// The copy on disk is the same or newer.
		if seen && receipt.Skills[name] == onDisk {
			return false, nil
		}
		receipt.Skills[name] = onDisk
		return true, nil
	}
}

// deliverSource registers the default marketplace once and reports whether the
// receipt needs writing. The address is only written into a config file that
// already exists: creating one would change which file a later start loads, and
// a home without a file already has the marketplace from the loader's default.
func deliverSource(cfg *config.Config, receipt *deliveryReceipt, res *SeedResult) bool {
	source := config.DefaultSkillsSource
	for _, offered := range receipt.Sources {
		if strings.EqualFold(strings.TrimSpace(offered), source) {
			return false
		}
	}
	receipt.Sources = append(receipt.Sources, source)

	if configured(cfg.Skills.Sources, source) {
		return true
	}
	path := strings.TrimSpace(cfg.Paths.ConfigPath)
	if path == "" {
		return true
	}
	if _, err := os.Stat(path); err != nil {
		return true
	}
	if added, err := AddSource(cfg, source); err == nil && added {
		res.SourceAdded = true
	}
	return true
}

func configured(sources []string, source string) bool {
	for _, s := range sources {
		if strings.EqualFold(strings.TrimSpace(s), source) {
			return true
		}
	}
	return false
}

// installedSkillVersion reads the version of the skill installed at dir and
// reports whether a skill is there at all.
func installedSkillVersion(dir string) (version string, present bool) {
	sk, err := loadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(sk.Version), true
}

// installBundledSkill materializes one embedded skill into the managed dir,
// through the same staging swap the marketplace installer uses.
func installBundledSkill(entry BundledEntry, managedDir, name string) error {
	staged := stagingDir(managedDir, name)
	_ = os.RemoveAll(staged)
	if err := copyEmbeddedDir(entry.Dir, staged); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("stage skill %q: %w", name, err)
	}
	return replaceSkillDir(managedDir, name, staged)
}

// copyEmbeddedDir writes an embedded subtree to dst.
func copyEmbeddedDir(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := src.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, modeForEmbedded(p))
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

// modeForEmbedded keeps a vendored helper script executable. embed.FS reports
// 0444 for everything it carries, so the bit has to be decided here; the
// reference trees of a skill ship hook scripts an operator copies and runs.
func modeForEmbedded(p string) os.FileMode {
	switch strings.ToLower(path.Ext(p)) {
	case ".sh", ".py", ".bash", ".zsh":
		return 0o755
	default:
		return 0o644
	}
}

func deliveryReceiptPath(managedDir string) string {
	return filepath.Join(managedDir, deliveryReceiptFile)
}

func readDeliveryReceipt(managedDir string) *deliveryReceipt {
	r := &deliveryReceipt{Version: 1, Skills: map[string]string{}}
	data, err := os.ReadFile(deliveryReceiptPath(managedDir))
	if err != nil {
		return r
	}
	var parsed deliveryReceipt
	if err := json.Unmarshal(data, &parsed); err != nil {
		return r
	}
	if parsed.Skills == nil {
		parsed.Skills = map[string]string{}
	}
	if parsed.Version == 0 {
		parsed.Version = 1
	}
	return &parsed
}

func writeDeliveryReceipt(managedDir string, r *deliveryReceipt) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(deliveryReceiptPath(managedDir), append(data, '\n'), 0o644)
}
