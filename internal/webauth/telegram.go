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
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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
// expired anyway, so one launch data signs a browser in once.
type ReplayGuard struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// Use records key until until and reports whether it was new.
func (g *ReplayGuard) Use(key string, until, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = make(map[string]time.Time)
	}
	for k, exp := range g.seen {
		if now.After(exp) {
			delete(g.seen, k)
		}
	}
	if _, ok := g.seen[key]; ok {
		return false
	}
	g.seen[key] = until
	return true
}
