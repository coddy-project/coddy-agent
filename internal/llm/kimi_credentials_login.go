package llm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

var ErrKimiLoginAttemptStale = errors.New("kimi credentials: login attempt is no longer current")

func (s *KimiCredentialStore) loginPath(region string) (string, error) {
	r, err := KimiCanonicalRegion(region)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir, "kimi-"+r+"-login.attempt"), nil
}

// BeginLogin records a unique attempt before device authorization starts.
func (s *KimiCredentialStore) BeginLogin(ctx context.Context, region string) (string, error) {
	cred, lock, _, err := s.paths(region)
	if err != nil {
		return "", err
	}
	scoped, release, err := s.acquireScopedLock(ctx, lock, cred)
	if err != nil {
		return "", err
	}
	defer release()
	s = scoped
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	path, err := s.loginPath(region)
	if err != nil {
		return "", err
	}
	token := hex.EncodeToString(id[:])
	if err := s.writeDurable(path, []byte(token)); err != nil {
		return "", err
	}
	return token, nil
}

func (s *KimiCredentialStore) invalidateLogin(region string) error {
	path, err := s.loginPath(region)
	if err != nil {
		return err
	}
	return s.writeDurable(path, nil)
}

func (s *KimiCredentialStore) checkLogin(region, attempt string) error {
	path, err := s.loginPath(region)
	if err != nil {
		return err
	}
	data, err := s.readPrivate(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrKimiLoginAttemptStale
	}
	if err != nil {
		return err
	}
	if attempt == "" || string(data) != attempt {
		return ErrKimiLoginAttemptStale
	}
	return nil
}

// CancelLogin invalidates an attempt only if it is still current.
func (s *KimiCredentialStore) CancelLogin(ctx context.Context, region, attempt string) error {
	cred, lock, _, err := s.paths(region)
	if err != nil {
		return err
	}
	scoped, release, err := s.acquireScopedLock(ctx, lock, cred)
	if err != nil {
		return err
	}
	defer release()
	s = scoped
	if err := s.checkLogin(region, attempt); err != nil {
		return err
	}
	return s.invalidateLogin(region)
}

// CompleteLogin consumes the current attempt before persisting the credential.
func (s *KimiCredentialStore) CompleteLogin(ctx context.Context, region, attempt string, credential KimiCredential) error {
	cred, lock, intent, err := s.paths(region)
	if err != nil {
		return err
	}
	if credential.AccessToken == "" {
		return errors.New("kimi credentials: empty access token")
	}
	scoped, release, err := s.acquireScopedLock(ctx, lock, cred)
	if err != nil {
		return err
	}
	defer release()
	s = scoped
	if err := s.checkLogin(region, attempt); err != nil {
		return err
	}
	if err := s.invalidateLogin(region); err != nil {
		return fmt.Errorf("consume login attempt: %w", err)
	}
	generation, err := s.currentGeneration(cred)
	if errors.Is(err, ErrKimiCredentialCorrupt) {
		data, readErr := s.readPrivate(cred)
		if readErr != nil {
			return readErr
		}
		generation = 0
		if json.Valid(data) {
			var previous struct {
				Generation uint64 `json:"generation"`
			}
			if decodeErr := json.Unmarshal(data, &previous); decodeErr != nil {
				return err
			}
			generation = previous.Generation
		}
	} else if err != nil {
		return err
	}
	if generation == math.MaxUint64 {
		return errors.New("kimi credentials: generation overflow")
	}
	credential.Generation = generation + 1
	intentData, readErr := s.readPrivate(intent)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if readErr == nil {
		var previous kimiRefreshIntent
		if json.Unmarshal(intentData, &previous) == nil && previous.Target == credential.Generation {
			if credential.Generation == math.MaxUint64 {
				return errors.New("kimi credentials: generation overflow")
			}
			credential.Generation++
		}
	}
	data, err := json.Marshal(credential) //nolint:gosec // G117: the credential is serialized solely for its private store
	if err != nil {
		return err
	}
	if err := s.writeDurable(cred, data); err != nil {
		return err
	}
	return s.removeIntent(intent)
}
