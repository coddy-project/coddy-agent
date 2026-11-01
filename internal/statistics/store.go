// Package statistics keeps usage independently of conversation bundles.
package statistics

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Statistics accepts IANA zones on hosts without a zone database.

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Event contains counters and identities only, never prompts or responses.
// Input includes Cached; reasoning output is already part of Output.
type Event struct {
	ID         string    `json:"id"`
	Dataset    string    `json:"dataset"`
	Session    string    `json:"session"`
	Project    string    `json:"project,omitempty"`
	Source     string    `json:"source,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Model      string    `json:"model,omitempty"`
	Reasoning  string    `json:"reasoning,omitempty"`
	Kind       string    `json:"kind"`
	At         time.Time `json:"at"`
	RecordedAt time.Time `json:"recordedAt"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	Input      *int64    `json:"inputTokens"`
	Output     *int64    `json:"outputTokens"`
	Cached     *int64    `json:"cachedTokens"`
	ActiveMS   *int64    `json:"activeMs"`
	CostUSD    *float64  `json:"costUsd"`
	Requests   int64     `json:"requests"`
	Skills     []string  `json:"skills,omitempty"`
	Imported   bool      `json:"imported,omitempty"`
}

type Price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Cached float64 `json:"cached"`
}

type ImportStatus struct {
	State  string `json:"state"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	Failed int    `json:"failed"`
	Error  string `json:"error,omitempty"`
}

type Settings struct {
	Dataset  string           `json:"dataset"`
	Timezone string           `json:"timezone"`
	Prices   map[string]Price `json:"prices"`
	Import   ImportStatus     `json:"import"`
	Cutoff   time.Time        `json:"cutoff"`
}

type Store struct{ Dir string }

var locks sync.Map

func New(home string) *Store { return &Store{Dir: filepath.Join(home, "statistics")} }
func Int(n int64) *int64     { return &n }

// locked also serializes independent Coddy processes using the same home.
func (s *Store) locked(fn func() error) error {
	key, err := filepath.Abs(s.Dir)
	if err != nil {
		return err
	}
	key = strings.ToLower(key)
	mu, _ := locks.LoadOrStore(key, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	unlock, err := platform.LockFile(filepath.Join(s.Dir, ".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func (s *Store) settings() (Settings, error) {
	var st Settings
	b, err := os.ReadFile(filepath.Join(s.Dir, "settings.json"))
	if err == nil {
		err = json.Unmarshal(b, &st)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return st, err
	}
	if st.Dataset == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return st, err
		}
		st.Dataset = hex.EncodeToString(id[:])
		st.Import.State = "pending"
		st.Prices = map[string]Price{}
		if err := s.write("settings.json", st); err != nil {
			return st, err
		}
	}
	if st.Prices == nil {
		st.Prices = map[string]Price{}
	}
	return st, nil
}

func (s *Store) write(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = platform.WriteFileReplacing(filepath.Join(s.Dir, name), b, 0o600)
	return err
}

func (s *Store) Settings() (Settings, error) {
	var st Settings
	err := s.locked(func() error {
		var err error
		st, err = s.settings()
		if err != nil {
			return err
		}
		return nil
	})
	return st, err
}

func (s *Store) UpdateSettings(update func(*Settings) error) error {
	return s.locked(func() error {
		st, err := s.settings()
		if err != nil {
			return err
		}
		if err := update(&st); err != nil {
			return err
		}
		return s.write("settings.json", st)
	})
}

// Append journals a checkpoint by stable id. Readers keep its last value.
// Monthly journals keep a long history from becoming millions of tiny files.
func (s *Store) Append(e Event) error {
	if e.ID == "" || e.At.IsZero() {
		return fmt.Errorf("statistics event needs an id and timestamp")
	}
	return s.locked(func() error {
		st, err := s.settings()
		if err != nil {
			return err
		}
		if e.At.Before(st.Cutoff) {
			return nil
		}
		e.Dataset = st.Dataset
		e.RecordedAt = time.Now().UTC()
		if e.Kind == "call" {
			ApplyPrice(&e, st.Prices)
		}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		path := filepath.Join(s.Dir, e.At.UTC().Format("2006-01")+".jsonl")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		// A crash can leave an unterminated last record. Drop only that tail
		// before appending, rather than joining the next event onto it.
		info, err := f.Stat()
		if err != nil {
			return err
		}
		end := info.Size()
		if end > 0 {
			var last [1]byte
			if _, err := f.ReadAt(last[:], end-1); err != nil {
				return err
			}
			if last[0] != '\n' {
				pos := end
				for pos > 0 {
					start := max(int64(0), pos-8192)
					chunk := make([]byte, pos-start)
					if _, err := f.ReadAt(chunk, start); err != nil {
						return err
					}
					if n := bytes.LastIndexByte(chunk, '\n'); n >= 0 {
						end = start + int64(n) + 1
						break
					}
					pos = start
					end = start
				}
				if err := f.Truncate(end); err != nil {
					return err
				}
			}
		}
		if _, err := f.Seek(end, io.SeekStart); err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
		return f.Sync()
	})
}

func journalEvents(path string, visit func(Event) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return nil
		} // An unfinished checkpoint was never committed.
		if err != nil {
			return err
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("statistics %s: %w", filepath.Base(path), err)
		}
		if err := visit(e); err != nil {
			return err
		}
	}
}

func (s *Store) journals() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if _, err := time.Parse("2006-01.jsonl", e.Name()); err == nil {
			paths = append(paths, filepath.Join(s.Dir, e.Name()))
		}
	}
	return paths, nil
}

func (s *Store) events(cutoff time.Time) ([]Event, error) {
	paths, err := s.journals()
	if err != nil {
		return nil, err
	}
	last := map[string]Event{}
	for _, path := range paths {
		if filepath.Base(path) < cutoff.UTC().Format("2006-01")+".jsonl" {
			continue
		}
		if err := journalEvents(path, func(e Event) error {
			if !e.At.Before(cutoff) {
				last[e.Dataset+":"+e.ID] = e
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	out := make([]Event, 0, len(last))
	for _, e := range last {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID < out[j].ID
		}
		return out[i].At.Before(out[j].At)
	})
	return out, nil
}

func (s *Store) Events() ([]Event, error) {
	var out []Event
	err := s.locked(func() error {
		st, err := s.settings()
		if err != nil {
			return err
		}
		out, err = s.events(st.Cutoff)
		return err
	})
	return out, err
}

// Clear commits the cutoff before rewriting journals. An interrupted cleanup
// or an overlapping historical import cannot resurrect the removed records.
func (s *Store) Clear(cutoff time.Time) error {
	return s.locked(func() error {
		st, err := s.settings()
		if err != nil {
			return err
		}
		if cutoff.Before(st.Cutoff) {
			cutoff = st.Cutoff
		}
		st.Cutoff = cutoff.UTC()
		if st.Import.State != "running" {
			st.Import.State = "skipped"
		}
		if err := s.write("settings.json", st); err != nil {
			return err
		}
		paths, err := s.journals()
		if err != nil {
			return err
		}
		for _, path := range paths {
			// Last checkpoint wins, so cleanup also compacts the kept tail.
			kept := map[string]Event{}
			if err := journalEvents(path, func(e Event) error {
				if !e.At.Before(cutoff) {
					kept[e.Dataset+":"+e.ID] = e
				}
				return nil
			}); err != nil {
				return err
			}
			if len(kept) == 0 {
				if err := os.Remove(path); err != nil {
					return err
				}
				continue
			}
			var b bytes.Buffer
			enc := json.NewEncoder(&b)
			keys := make([]string, 0, len(kept))
			for k := range kept {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if err := enc.Encode(kept[k]); err != nil {
					return err
				}
			}
			if _, err := platform.WriteFileReplacing(path, b.Bytes(), 0o600); err != nil {
				return err
			}
		}
		return nil
	})
}
