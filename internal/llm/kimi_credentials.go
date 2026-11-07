package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// The Kimi credential layer keeps a subscription credential durable across
// the processes that may share it (the console, coddy serve, a scheduled
// run) while the provider rotates the refresh token on every refresh. Three
// files per identity live under the store directory:
//
//	kimi-<region>-auth.json      - the credential itself, 0600
//	kimi-<region>-auth.lock      - the inter-process lock, 0600
//	kimi-<region>-refresh.intent - the durable refresh intent, 0600
//
// The intent is written and synced before a refresh request goes out. A
// refresh that commits removes it; an intent that outlives its writer marks
// the credential uncertain, because the refresh token may already be
// rotated on the server and must never be sent again.

// KimiCredential is the persisted OAuth credential of one Kimi provider and
// region. Generation counts durable commits under the lock: a reader that
// finds a different generation than it read before knows a peer wrote.
type KimiCredential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"` // unix seconds; 0 means no expiry known
	ExpiresIn    int64  `json:"expires_in"`
	DeviceID     string `json:"device_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Generation   uint64 `json:"generation"`
}

// KimiCredentialState is what the store holds for one identity.
type KimiCredentialState int

const (
	// KimiCredentialMissing: no credential file.
	KimiCredentialMissing KimiCredentialState = iota
	// KimiCredentialValid: a usable credential.
	KimiCredentialValid
	// KimiCredentialRevoked: a tombstone; a refresh was rejected and the
	// credential needs a fresh login, not another refresh attempt.
	KimiCredentialRevoked
	// KimiCredentialUncertain: a refresh intent outlived its writer without
	// a matching committed generation; the refresh outcome is unknown and
	// the credential needs a fresh login.
	KimiCredentialUncertain
)

// Failure kinds a caller can branch on with errors.Is.
var (
	ErrKimiCredentialMissing      = errors.New("kimi credentials: no stored credential; sign in")
	ErrKimiCredentialUnauthorized = errors.New("kimi credentials: refresh rejected; sign in again")
	ErrKimiCredentialUncertain    = errors.New("kimi credentials: refresh outcome unknown; sign in again")
	ErrKimiCredentialCorrupt      = errors.New("kimi credentials: unreadable credential file")
	ErrKimiStoreUnsupported       = errors.New("kimi credentials: platform cannot guarantee a locked durable refresh")
)

// kimiProviderName constrains the provider segment of KimiCredentialDir the
// same way config validates providers[].name.
var kimiProviderName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// KimiCanonicalRegion normalizes the region selector of a Kimi provider to
// the canonical identity used for file names and locks. Anything else is
// rejected: a mistyped region must never become a second credential slot.
func KimiCanonicalRegion(region string) (string, error) {
	switch strings.TrimSpace(region) {
	case "", "global":
		return "global", nil
	case "china":
		return "china", nil
	default:
		return "", fmt.Errorf("kimi: invalid region %q", region)
	}
}

// KimiCredentialDir is the conventional store directory of one provider row:
// <home>/providers/<name>, the same place every managed credential lives.
// It returns "" for an unusable home or provider name.
func KimiCredentialDir(home, providerName string) string {
	home = strings.TrimSpace(home)
	providerName = strings.TrimSpace(providerName)
	if home == "" || !kimiProviderName.MatchString(providerName) {
		return ""
	}
	return filepath.Join(home, "providers", providerName)
}

// Platform capability switches, variables so a test can play an OS that
// cannot keep the durability promises.
var (
	kimiLockAvailable  = platform.FileLockingAvailable
	kimiDirSyncDurable = platform.DirSyncDurable
)

// KimiRefreshSupported reports whether this build can run a refresh with the
// guarantees it is written around: an inter-process lock and a durable
// directory flush. Where it is false a refresh attempt fails closed with
// ErrKimiStoreUnsupported instead of racing token rotation.
func KimiRefreshSupported() bool {
	return kimiSecureStoreAvailable && kimiLockAvailable && kimiDirSyncDurable
}

const (
	// kimiLockWait bounds how long one caller waits for the identity lock
	// before reporting contention instead of hanging a turn.
	kimiLockWait = 60 * time.Second
	// kimiLockRetry is the poll interval for the inter-process file lock.
	kimiLockRetry = 50 * time.Millisecond
)

// KimiCredentialStore is the durable credential slot of one directory for
// every Kimi region identity that may exist there. Reads are lock-free
// (writers commit by rename); every mutation - save, remove, refresh -
// runs under an in-process mutex and an inter-process file lock so a
// console login, a serve refresh and a logout serialize against each other.
type KimiCredentialStore struct {
	dir      string
	root     *os.Root
	lockWait time.Duration // test override; zero uses kimiLockWait
}

// NewKimiCredentialStore opens the store rooted at dir, for example the one
// KimiCredentialDir returns. The directory is created on first write.
func NewKimiCredentialStore(dir string) *KimiCredentialStore {
	return &KimiCredentialStore{dir: strings.TrimSpace(dir)}
}

// Dir is the directory the store was opened on.
func (s *KimiCredentialStore) Dir() string { return s.dir }

// CredentialPath is the credential file of one region identity, "" for an
// invalid region.
func (s *KimiCredentialStore) CredentialPath(region string) string {
	cred, _, _, err := s.paths(region)
	if err != nil {
		return ""
	}
	return cred
}

// paths resolves the credential, lock and intent files of one identity.
func (s *KimiCredentialStore) paths(region string) (cred, lock, intent string, err error) {
	canonical, err := KimiCanonicalRegion(region)
	if err != nil {
		return "", "", "", err
	}
	base := "kimi-" + canonical
	return filepath.Join(s.dir, base+"-auth.json"),
		filepath.Join(s.dir, base+"-auth.lock"),
		filepath.Join(s.dir, base+"-refresh.intent"), nil
}

// intentPath is the refresh-intent file of one identity, "" for an invalid
// region.
func (s *KimiCredentialStore) intentPath(region string) string {
	_, _, p, _ := s.paths(region)
	return p
}

// Load reads the credential of one identity and classifies it. A missing
// file is KimiCredentialMissing, an empty access token a tombstone
// (KimiCredentialRevoked), and a refresh intent whose target generation was
// never committed makes an otherwise valid credential
// KimiCredentialUncertain. A file that cannot be parsed fails closed with
// ErrKimiCredentialCorrupt rather than being silently treated as absent.
//
// A refresh in flight in another process also shows as uncertain here: that
// is the honest answer for a status read, while a caller about to take the
// identity lock must use the post-lock re-read instead.
func (s *KimiCredentialStore) Load(region string) (KimiCredential, KimiCredentialState, error) {
	return s.loadState(region, true)
}

// loadState is Load with a switch on intent checking. A caller that has not
// taken the identity lock must not consult the intent: a live refresh in
// another goroutine or process legitimately has one outstanding, and only
// inside the lock does an intent provably belong to a dead writer.
func (s *KimiCredentialStore) loadState(region string, checkIntent bool) (KimiCredential, KimiCredentialState, error) {
	credPath, _, intentPath, err := s.paths(region)
	if err != nil {
		return KimiCredential{}, 0, err
	}
	if s.root == nil {
		root, e := openKimiRoot(s.dir, false)
		if errors.Is(e, os.ErrNotExist) {
			return KimiCredential{}, KimiCredentialMissing, nil
		}
		if e != nil {
			return KimiCredential{}, 0, e
		}
		defer func() { _ = root.Close() }()
		scoped := *s
		scoped.root = root
		return scoped.loadState(region, checkIntent)
	}
	data, err := s.readPrivate(credPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		_, intentErr := s.readPrivate(intentPath)
		if intentErr == nil {
			return KimiCredential{}, KimiCredentialUncertain, nil
		}
		if !errors.Is(intentErr, os.ErrNotExist) {
			return KimiCredential{}, 0, intentErr
		}
		return KimiCredential{}, KimiCredentialMissing, nil
	case err != nil:
		return KimiCredential{}, 0, err
	}
	var cred KimiCredential
	if err := json.Unmarshal(data, &cred); err != nil {
		return KimiCredential{}, 0, fmt.Errorf("%w: %s", ErrKimiCredentialCorrupt, credPath)
	}
	if cred.AccessToken == "" {
		return cred, KimiCredentialRevoked, nil
	}
	if !checkIntent {
		return cred, KimiCredentialValid, nil
	}
	intentData, err := s.readPrivate(intentPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return cred, KimiCredentialValid, nil
	case err != nil:
		return cred, 0, err
	}
	var in kimiRefreshIntent
	if err := json.Unmarshal(intentData, &in); err != nil || in.Target != cred.Generation {
		// The writer cannot prove its refresh completed: fail closed.
		return cred, KimiCredentialUncertain, nil
	}
	return cred, KimiCredentialValid, nil
}

// Save is disabled: a login must use BeginLogin and CompleteLogin so that
// a stale device flow cannot overwrite a newer login or undo logout.
func (s *KimiCredentialStore) Save(region string, cred KimiCredential) error {
	return errors.New("kimi credentials: Save is disabled; use BeginLogin and CompleteLogin")
}

// Remove deletes the credential and any intent of one identity under the
// same lock a refresh takes, so a logout cannot interleave with a refresh
// mid-flight. A missing credential is not an error.
func (s *KimiCredentialStore) Remove(region string) error {
	credPath, lockPath, intentPath, err := s.paths(region)
	if err != nil {
		return err
	}
	scoped, release, err := s.acquireScopedLock(context.Background(), lockPath, credPath)
	if err != nil {
		return err
	}
	defer release()
	s = scoped
	if err := s.invalidateLogin(region); err != nil {
		return err
	}
	if err := s.removePrivate(credPath); err != nil {
		return err
	}
	if err := s.syncDir(); err != nil {
		return err
	}
	return s.removeIntent(intentPath)
}

// currentGeneration reads the generation of the stored credential while the
// caller holds the lock; only an absent credential means generation zero.
func (s *KimiCredentialStore) currentGeneration(credPath string) (uint64, error) {
	data, err := s.readPrivate(credPath)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var cred KimiCredential
	if err := json.Unmarshal(data, &cred); err != nil {
		return 0, ErrKimiCredentialCorrupt
	}
	return cred.Generation, nil
}

// saveUnlocked writes a credential without taking the lock. It exists for
// tests that simulate a writer outside this process's lock discipline and
// for nothing else; production paths go through Save or the refresher.
func (s *KimiCredentialStore) saveUnlocked(region string, cred KimiCredential) error {
	if s.root == nil {
		root, err := openKimiRoot(s.dir, true)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		scoped := *s
		scoped.root = root
		return scoped.saveUnlocked(region, cred)
	}
	credPath, _, _, err := s.paths(region)
	if err != nil {
		return err
	}
	generation, err := s.currentGeneration(credPath)
	if err != nil {
		return err
	}
	cred.Generation = generation + 1
	data, err := json.Marshal(cred) //nolint:gosec // G117: this writes the credential file itself, private (0600)
	if err != nil {
		return err
	}
	return s.writeDurable(credPath, data)
}

// writeIntent commits a refresh intent durably (temp file, fsync, rename,
// directory flush) before the refresh request it describes is allowed out.
// Callers hold the identity lock.
func (s *KimiCredentialStore) writeIntent(region string, in kimiRefreshIntent) error {
	if s.root == nil {
		root, err := openKimiRoot(s.dir, true)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		scoped := *s
		scoped.root = root
		return scoped.writeIntent(region, in)
	}
	_, _, intentPath, err := s.paths(region)
	if err != nil {
		return err
	}
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return s.writeDurable(intentPath, data)
}

// removeIntent deletes the intent file and flushes the directory so a crash
// cannot resurrect it. Callers hold the identity lock.
func (s *KimiCredentialStore) removeIntent(intentPath string) error {
	if s.root == nil {
		root, err := openKimiRoot(s.dir, false)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		scoped := *s
		scoped.root = root
		return scoped.removeIntent(intentPath)
	}
	if err := s.removePrivate(intentPath); err != nil {
		return err
	}
	return s.syncDir()
}

// writeDurable commits data to path with owner-only permissions through a
// synced temp file and an atomic rename, and treats an unconfirmed directory
// flush as a write failure rather than silently claiming durability.
func (s *KimiCredentialStore) writeDurable(path string, data []byte) error {
	if s.root == nil {
		return errors.New("kimi credentials: directory not open")
	}
	report, err := platform.WritePrivateFileInRoot(s.root, filepath.Base(path), data)
	if err != nil {
		return err
	}
	if report.DirSyncErr != nil {
		return fmt.Errorf("%w: %v", platform.ErrWriteNotDurable, report.DirSyncErr)
	}
	for dir := filepath.Dir(s.root.Name()); ; dir = filepath.Dir(dir) {
		if err := platform.SyncDir(dir); err != nil {
			return fmt.Errorf("%w: sync ancestor: %v", platform.ErrWriteNotDurable, err)
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return nil
}

// syncDir flushes the opened store directory.
func (s *KimiCredentialStore) syncDir() error {
	return platform.SyncRoot(s.root)
}

// acquireLock serializes a mutation on the identity: first the in-process
// mutex keyed by the credential path, then the inter-process file lock with
// a bounded wait. A platform without a file-locking primitive fails closed -
// proceeding unlocked would race refresh token rotation.
func (s *KimiCredentialStore) acquireLock(ctx context.Context, lockPath, credPath string) (func(), error) {
	_, release, err := s.acquireScopedLock(ctx, lockPath, credPath)
	return release, err
}

func (s *KimiCredentialStore) acquireScopedLock(ctx context.Context, lockPath, credPath string) (*KimiCredentialStore, func(), error) {
	if !kimiLockAvailable {
		return nil, nil, fmt.Errorf("%w: no inter-process file lock on %s", ErrKimiStoreUnsupported, runtime.GOOS)
	}
	if !kimiDirSyncDurable {
		return nil, nil, fmt.Errorf("%w: no durable directory flush on %s", ErrKimiStoreUnsupported, runtime.GOOS)
	}
	wait := s.lockWait
	if wait <= 0 {
		wait = kimiLockWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	mu := kimiIdentityLock(credPath)
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("kimi credentials: lock %s busy over %s", lockPath, wait)
	case <-mu:
	}
	releaseMu := true
	defer func() {
		if releaseMu {
			mu <- struct{}{}
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("kimi credentials: lock %s busy over %s", lockPath, wait)
	default:
	}
	root, err := openKimiRoot(s.dir, true)
	if err != nil {
		return nil, nil, err
	}
	keepRoot := false
	defer func() {
		if !keepRoot {
			_ = root.Close()
		}
	}()
	scoped := *s
	scoped.root = root
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		select {
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			return nil, nil, fmt.Errorf("kimi credentials: lock %s busy over %s", lockPath, wait)
		default:
		}
		name := filepath.Base(lockPath)
		if info, e := root.Lstat(name); e == nil && kimiCheckPrivateFile(info) != nil {
			return nil, nil, errors.New("kimi credentials: unsafe lock file")
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, nil, e
		}
		f, e := root.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return nil, nil, e
		}
		info, e := f.Stat()
		if e == nil {
			e = kimiCheckPrivateFile(info)
		}
		if e != nil {
			_ = f.Close()
			return nil, nil, e
		}
		acquired, err := kimiTryFlock(f)
		if !acquired || err != nil {
			_ = f.Close()
		}
		if err != nil {
			return nil, nil, err
		}
		if acquired {
			if err := ctx.Err(); err != nil {
				kimiUnlock(f)
				return nil, nil, err
			}
			select {
			case <-timer.C:
				kimiUnlock(f)
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
				return nil, nil, fmt.Errorf("kimi credentials: lock %s busy over %s", lockPath, wait)
			default:
			}
			releaseMu = false
			keepRoot = true
			return &scoped, func() {
				kimiUnlock(f)
				_ = root.Close()
				mu <- struct{}{}
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			return nil, nil, fmt.Errorf("kimi credentials: lock %s busy over %s", lockPath, wait)
		case <-time.After(kimiLockRetry):
		}
	}
}

// kimiIdentityLocks serializes one identity inside this process so the file
// lock below it is only ever contested between processes.
var (
	kimiIdentityLocksMu sync.Mutex
	kimiIdentityLocks   = map[string]chan struct{}{}
)

func kimiIdentityLock(key string) chan struct{} {
	kimiIdentityLocksMu.Lock()
	defer kimiIdentityLocksMu.Unlock()
	mu := kimiIdentityLocks[key]
	if mu == nil {
		mu = make(chan struct{}, 1)
		mu <- struct{}{}
		kimiIdentityLocks[key] = mu
	}
	return mu
}

// readPrivate refuses links and special files within the verified private directory.
func (s *KimiCredentialStore) readPrivate(path string) ([]byte, error) {
	name := filepath.Base(path)
	info, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := kimiCheckPrivateFile(info); err != nil {
		return nil, fmt.Errorf("kimi credentials: unsafe file %s: %w", name, err)
	}
	return s.root.ReadFile(name)
}

func (s *KimiCredentialStore) removePrivate(path string) error {
	name := filepath.Base(path)
	info, err := s.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := kimiCheckPrivateFile(info); err != nil {
		return fmt.Errorf("kimi credentials: unsafe file %s: %w", name, err)
	}
	return s.root.Remove(name)
}
