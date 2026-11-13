//go:build http

package httpserver

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// The ledger lives at <Coddy home>/webauth-backup-consumed.json, outside the
// config and its recovery copies. It maps each generation to consumed SHA-256
// hashes. Old generations cannot be pruned safely: an old config backup may
// restore any of them. Call only under config.WithConfigFileLock and the
// adjacent webauth-backup-consumed.json.lock cross-process lock.
func loadBackupLedger(home string) (map[string][]string, error) {
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("backup ledger home is empty")
	}
	path := filepath.Join(home, "webauth-backup-consumed.json")
	ledger := map[string][]string{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read backup ledger: %w", err)
	}
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&ledger); err != nil || ledger == nil {
			return nil, errors.New("invalid backup ledger")
		}
		var extra interface{}
		if err = dec.Decode(&extra); err != io.EOF {
			return nil, errors.New("invalid backup ledger")
		}
		for gen, hashes := range ledger {
			if gen == "" || hashes == nil {
				return nil, errors.New("invalid backup ledger")
			}
			for _, h := range hashes {
				raw, e := hex.DecodeString(h)
				if e != nil || len(raw) != 32 || h != strings.ToLower(h) {
					return nil, errors.New("invalid backup ledger hash")
				}
			}
		}
	}
	return ledger, nil
}

func recordBackupConsumption(home, generation, hash string, ledger map[string][]string) (bool, error) {
	if generation == "" {
		return false, errors.New("backup generation is empty")
	}
	path := filepath.Join(home, "webauth-backup-consumed.json")
	for _, h := range ledger[generation] {
		if h == hash {
			return false, nil
		}
	}
	ledger[generation] = append(ledger[generation], hash)
	data, err := json.Marshal(ledger)
	if err != nil {
		return false, err
	}
	report, err := platform.WritePrivateFileReplacing(path, data)
	if err != nil {
		return false, fmt.Errorf("write backup ledger: %w", err)
	}
	if report.DirSyncErr != nil {
		return false, fmt.Errorf("sync backup ledger directory: %w", report.DirSyncErr)
	}
	return true, nil
}
