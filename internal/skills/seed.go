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
//   - a copy on disk older than the release is replaced, and one that declares
//     no version at all counts as older: it predates these skills carrying one;
//   - a copy that is newer is left exactly as it is.
//
// The marketplace those skills are published from needs no receipt: it is a
// system source (config.SystemSkillsSource), listed and synced beside whatever
// skills.sources names, so no config file is ever written here.

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
// the name and deliberately wrote nothing.
type deliveryReceipt struct {
	Version int               `json:"version"`
	Skills  map[string]string `json:"skills,omitempty"`
}

// SeedResult summarizes one delivery run. An empty result is the normal
// outcome: the delivery only acts the first time it sees something.
type SeedResult struct {
	Installed []string `json:"installed"`
	Updated   []string `json:"updated"`
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
	// A swap that a killed process left half-finished is repaired before
	// anything decides what is on disk.
	RecoverInterruptedInstalls(managedDir)

	receipt, err := readDeliveryReceipt(managedDir)
	if err != nil {
		return nil, err
	}
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
	on := inspectInstalledSkill(dst)

	switch {
	case !on.present && seen:
		// Handed over once and gone since: the operator deleted it.
		return false, nil

	case !on.present:
		if err := installBundledSkill(entry, managedDir, name); err != nil {
			return false, err
		}
		receipt.Skills[name] = entry.Version
		res.Installed = append(res.Installed, name)
		return true, nil

	case !on.readable:
		// There, but Coddy cannot read it. Record the name so the question is
		// settled and leave every byte of it where it is.
		if seen {
			return false, nil
		}
		receipt.Skills[name] = ""
		return true, nil

	// compareVersions ranks an absent version below every declared one, so a
	// copy with no version: - installed before these skills declared one - is
	// replaced along with the copies that name an older release.
	case entry.Version != "" && compareVersions(entry.Version, on.version) > 0:
		if err := installBundledSkill(entry, managedDir, name); err != nil {
			return false, err
		}
		receipt.Skills[name] = entry.Version
		res.Updated = append(res.Updated, name)
		return true, nil

	default:
		// The copy on disk is the same or newer.
		if seen && receipt.Skills[name] == on.version {
			return false, nil
		}
		receipt.Skills[name] = on.version
		return true, nil
	}
}

// installedSkill describes what is at a skill's place in the managed dir. The
// three states are not two: a SKILL.md that is there but cannot be read is
// neither an absent skill nor one with no version. Calling it absent would hand
// the fresh-install branch a directory holding the operator's work, and calling
// its version empty would rank it below every release and replace it. It is
// simply not ours to judge, so it is left alone.
type installedSkill struct {
	present  bool
	readable bool
	version  string
}

func inspectInstalledSkill(dir string) installedSkill {
	path := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		return installedSkill{}
	}
	sk, err := loadFile(path)
	if err != nil {
		return installedSkill{present: true}
	}
	return installedSkill{present: true, readable: true, version: strings.TrimSpace(sk.Version)}
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

// DeliveredAndDeleted returns the skills the delivery handed to managedDir and
// the operator has since removed. The loader leaves those out of the catalogue:
// a skill that is gone from disk but still read out of the binary would keep
// answering its slash command and could not be deleted a second time.
func DeliveredAndDeleted(managedDir string) map[string]struct{} {
	receipt, err := readDeliveryReceipt(managedDir)
	if err != nil || len(receipt.Skills) == 0 {
		// A receipt that cannot be read withholds nothing: the copies in the
		// binary answer, which is the safe direction for a loader.
		return nil
	}
	out := make(map[string]struct{})
	for name := range receipt.Skills {
		// Presence only - this runs for every session that loads skills, and
		// reading each SKILL.md to learn it exists would be a file read per
		// delivered skill per session.
		if _, err := os.Stat(filepath.Join(managedDir, name, "SKILL.md")); err != nil {
			out[name] = struct{}{}
		}
	}
	return out
}

func deliveryReceiptPath(managedDir string) string {
	return filepath.Join(managedDir, deliveryReceiptFile)
}

// readDeliveryReceipt reads the record of what has been handed over. A missing
// file is a home that has never seen the delivery. A file that is there but
// cannot be read is something else entirely and is reported as an error: read
// as an empty record it would say nothing was ever delivered, and the next run
// would write back every skill the operator had deleted.
func readDeliveryReceipt(managedDir string) (*deliveryReceipt, error) {
	fresh := &deliveryReceipt{Version: 1, Skills: map[string]string{}}
	data, err := os.ReadFile(deliveryReceiptPath(managedDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fresh, nil
		}
		return nil, fmt.Errorf("read %s: %w", deliveryReceiptFile, err)
	}
	var parsed deliveryReceipt
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse %s: %w", deliveryReceiptFile, err)
	}
	if parsed.Skills == nil {
		parsed.Skills = map[string]string{}
	}
	if parsed.Version == 0 {
		parsed.Version = 1
	}
	return &parsed, nil
}

func writeDeliveryReceipt(managedDir string, r *deliveryReceipt) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	final := deliveryReceiptPath(managedDir)
	tmp := fmt.Sprintf("%s.%d.tmp", final, os.Getpid())
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
