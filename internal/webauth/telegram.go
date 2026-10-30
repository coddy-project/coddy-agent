package webauth

// Telegram Mini App sign-in: the launch data Telegram hands a Mini App is
// signed with the bot's token, so a server that knows the token can tell who
// opened it without a password (core.telegram.org/bots/webapps, "Validating
// data received via the Mini App").

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// TelegramLaunch is what a verified launch says.
type TelegramLaunch struct {
	UserID   int64
	Username string
	AuthDate time.Time
	// Hash is the signature the launch carried, the key for single use.
	Hash string
}

// Errors of VerifyTelegramInitData.
var (
	ErrTelegramSignature = errors.New("telegram launch data: bad signature")
	ErrTelegramExpired   = errors.New("telegram launch data: too old")
	ErrTelegramMalformed = errors.New("telegram launch data: malformed")
)

// telegramClockSkew is how far in the future a launch may claim to be.
const telegramClockSkew = 5 * time.Minute

// VerifyTelegramInitData checks the launch data of a Mini App against the
// bot's token: the data-check string is every field but hash, sorted by key,
// key=value joined by newlines; the secret key is HMAC-SHA256 of the token
// keyed by "WebAppData"; the hash is the hex HMAC-SHA256 of the data-check
// string under that key. A launch older than maxAge is refused.
func VerifyTelegramInitData(initData, botToken string, now time.Time, maxAge time.Duration) (TelegramLaunch, error) {
	var out TelegramLaunch
	if strings.TrimSpace(botToken) == "" {
		return out, ErrTelegramSignature
	}
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return out, ErrTelegramMalformed
	}
	got := vals.Get("hash")
	if got == "" || len(vals["hash"]) != 1 {
		return out, ErrTelegramMalformed
	}
	keys := make([]string, 0, len(vals))
	for k, v := range vals {
		if k == "hash" {
			continue
		}
		if len(v) != 1 {
			// A key given twice has no single value to sign.
			return out, ErrTelegramMalformed
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+vals.Get(k))
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	want := hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(lines, "\n"))))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(got))) {
		return out, ErrTelegramSignature
	}
	sec, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil || sec <= 0 {
		return out, ErrTelegramMalformed
	}
	out.AuthDate = time.Unix(sec, 0)
	if now.Sub(out.AuthDate) > maxAge || out.AuthDate.Sub(now) > telegramClockSkew {
		return out, ErrTelegramExpired
	}
	var user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal([]byte(vals.Get("user")), &user); err != nil || user.ID == 0 {
		return out, ErrTelegramMalformed
	}
	out.UserID = user.ID
	out.Username = user.Username
	out.Hash = strings.ToLower(got)
	return out, nil
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// ReplayGuard remembers launches already used, until they would have
// expired anyway, so one launch data signs a browser in once. With a file
// (SetFile) the record outlives the process: a restart within a launch's
// hour does not make an already used launch new again.
type ReplayGuard struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	path   string
	loaded bool
}

// SetFile names the file the guard keeps its record in. The file holds a
// digest of each key, never the key itself.
func (g *ReplayGuard) SetFile(path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.path = path
	g.loaded = false
}

// Use records key until until and reports whether it was new. An error
// means the durable record could not be read or written: the caller must
// refuse the launch, since it cannot prove it stays used.
func (g *ReplayGuard) Use(key string, until, now time.Time) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = make(map[string]time.Time)
	}
	if err := g.loadLocked(); err != nil {
		return false, err
	}
	for k, exp := range g.seen {
		if now.After(exp) {
			delete(g.seen, k)
		}
	}
	sum := sha256.Sum256([]byte(key))
	digest := hex.EncodeToString(sum[:])
	if _, ok := g.seen[digest]; ok {
		return false, nil
	}
	g.seen[digest] = until
	if err := g.saveLocked(); err != nil {
		if !errors.Is(err, platform.ErrWriteNotDurable) {
			delete(g.seen, digest)
		}
		return false, err
	}
	return true, nil
}

// loadLocked merges the file's record into memory once. A file that is not
// there yet is an empty record; one that cannot be read or parsed is an
// error, since treating it as empty would accept a launch already spent
// before a restart. A failure is not remembered, so the next call tries
// the read again.
func (g *ReplayGuard) loadLocked() error {
	if g.loaded || g.path == "" {
		return nil
	}
	data, err := os.ReadFile(g.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			g.loaded = true
			return nil
		}
		return fmt.Errorf("telegram: the record of used launches cannot be read: %w", err)
	}
	var stored map[string]time.Time
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("telegram: the record of used launches cannot be read: %w", err)
	}
	if stored == nil {
		return errors.New("telegram: the record of used launches must be an object")
	}
	for key, expiry := range stored {
		decoded, err := hex.DecodeString(key)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != key || expiry.IsZero() {
			return errors.New("telegram: invalid entry in the record of used launches")
		}
	}
	g.loaded = true
	for k, exp := range stored {
		if cur, ok := g.seen[k]; !ok || exp.After(cur) {
			g.seen[k] = exp
		}
	}
	return nil
}

// saveLocked writes the record next to where it will be read, readable by
// the owner only. A durability error may follow a committed replacement.
func (g *ReplayGuard) saveLocked() error {
	if g.path == "" {
		return nil
	}
	data, err := json.Marshal(g.seen)
	if err != nil {
		return err
	}
	report, err := platform.WritePrivateFileReplacing(g.path, data)
	if err != nil {
		return fmt.Errorf("telegram: the record of used launches was not saved: %w", err)
	}
	if report.DirSyncErr != nil {
		return fmt.Errorf("telegram: the record of used launches: %w: %v", platform.ErrWriteNotDurable, report.DirSyncErr)
	}
	return nil
}
