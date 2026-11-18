package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// kimiRefreshIntent is the intent written under the identity lock
// before a refresh request is sent. It records the credential generation the
// refresh started from and the generation it intends to commit, so a later
// process can tell a completed rotation (the credential's generation matches
// Target) from an abandoned one (anything else, which fails closed). The
// refresh token itself is only fingerprinted.
type kimiRefreshIntent struct {
	Region        string `json:"region"`
	Generation    uint64 `json:"generation"`
	Target        uint64 `json:"target_generation"`
	RefreshSHA256 string `json:"refresh_sha256,omitempty"`
	StartedAt     int64  `json:"started_at"`
}

const (
	// kimiMinRefreshThresholdSeconds is the floor of the refresh window; a
	// token closer to expiry than it refreshes eagerly (upstream's
	// max(300s, expires_in/2)).
	kimiMinRefreshThresholdSeconds = int64(300)
	// kimiUnauthorizedRecoveryWait is the short pause before re-reading the
	// store after a 401, giving a peer rotation a moment to become visible.
	kimiUnauthorizedRecoveryWait = 100 * time.Millisecond
)

var errKimiRefreshRateLimited = errors.New("kimi credentials: refresh rate limited")

// kimiNeedsRefresh mirrors the upstream rule: a forced call always
// refreshes; otherwise a token refreshes once its remaining lifetime falls
// below max(300s, expires_in/2). A token without an expiry never refreshes
// on its own.
func kimiNeedsRefresh(cred KimiCredential, force bool, now int64) bool {
	if force {
		return true
	}
	if cred.ExpiresAt == 0 {
		return false
	}
	threshold := cred.ExpiresIn / 2
	if threshold < kimiMinRefreshThresholdSeconds {
		threshold = kimiMinRefreshThresholdSeconds
	}
	return cred.ExpiresAt-now < threshold
}

func kimiFingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:8])
}

// kimiRefresherConfig carries the injectable clock and sleeper; production
// uses wall time, tests substitute their own.
type kimiRefresherConfig struct {
	now   func() int64
	sleep func(time.Duration)
}

// KimiRefresherOption tunes a refresher for tests.
type KimiRefresherOption func(*kimiRefresherConfig)

// WithKimiClock substitutes the wall clock (unix seconds).
func WithKimiClock(now func() int64) KimiRefresherOption {
	return func(c *kimiRefresherConfig) {
		if now != nil {
			c.now = now
		}
	}
}

// WithKimiSleep substitutes the retry/recovery sleeper.
func WithKimiSleep(sleep func(time.Duration)) KimiRefresherOption {
	return func(c *kimiRefresherConfig) {
		if sleep != nil {
			c.sleep = sleep
		}
	}
}

// KimiCredentialRefresher returns a valid access token for one provider +
// region identity, refreshing it lazily when it nears expiry. Refresh is
// serialized three ways: an in-process mutex, an inter-process file lock on
// the identity, and a re-read inside the lock, so however many callers and
// processes ask at once, at most one rotation goes out.
type KimiCredentialRefresher struct {
	store     *KimiCredentialStore
	region    string
	oauthHost string
	client    *http.Client
	now       func() int64
	sleep     func(time.Duration)

	// hookAfterLoad is a test seam invoked between the pre-lock read and
	// the lock acquisition, to place a racing writer deterministically.
	hookAfterLoad func()
}

// NewKimiCredentialRefresher builds a refresher for the identity (store dir,
// region) whose refresh requests go to oauthHost. proxyOrClient is the
// shared boundary: a providers[].proxy string is resolved through
// HTTPClientForProviderProxy, an explicit *http.Client is the test seam, and
// nil is an error, never a silent default.
func NewKimiCredentialRefresher(store *KimiCredentialStore, region, oauthHost string, proxyOrClient any, opts ...KimiRefresherOption) (*KimiCredentialRefresher, error) {
	if store == nil {
		return nil, errors.New("kimi credentials: nil store")
	}
	canonical, err := KimiCanonicalRegion(region)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(strings.TrimSpace(oauthHost))
	if err != nil || u.User != nil || u.Fragment != "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("kimi credentials: invalid OAuth host %q", oauthHost)
	}
	if _, isTestClient := proxyOrClient.(*http.Client); !isTestClient {
		allowed, err := kimiOAuthEndpoint(canonical)
		if err != nil || strings.TrimRight(oauthHost, "/") != allowed {
			return nil, errors.New("kimi credentials: OAuth host does not match region")
		}
	}
	client, err := providerHTTPClientArg(proxyOrClient)
	if err != nil {
		return nil, err
	}
	cfg := &kimiRefresherConfig{
		now:   func() int64 { return time.Now().Unix() },
		sleep: func(d time.Duration) { time.Sleep(d) },
	}
	for _, o := range opts {
		if o != nil {
			o(cfg)
		}
	}
	return &KimiCredentialRefresher{
		store:     store,
		region:    canonical,
		oauthHost: strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"),
		client:    client,
		now:       cfg.now,
		sleep:     cfg.sleep,
	}, nil
}

// NewKimiCredentialRefresherForRegion is the production constructor: the
// OAuth host is derived from the region alone, so no caller can aim the
// refresh elsewhere.
func NewKimiCredentialRefresherForRegion(store *KimiCredentialStore, region string, proxyOrClient any, opts ...KimiRefresherOption) (*KimiCredentialRefresher, error) {
	host, err := kimiOAuthEndpoint(region)
	if err != nil {
		return nil, err
	}
	return NewKimiCredentialRefresher(store, region, host, proxyOrClient, opts...)
}

// AccessToken returns a valid access token, refreshing once the stored one
// is inside the refresh window.
func (r *KimiCredentialRefresher) AccessToken(ctx context.Context) (string, error) {
	return r.accessToken(ctx, false)
}

// ForceAccessToken refreshes even outside the refresh window, unless the
// stored credential moved to a newer generation while the lock was being
// acquired (a peer rotation or a fresh login), in which case that credential
// is returned.
func (r *KimiCredentialRefresher) ForceAccessToken(ctx context.Context) (string, error) {
	return r.accessToken(ctx, true)
}

func (r *KimiCredentialRefresher) accessToken(ctx context.Context, force bool) (string, error) {
	// The fast path reads without the intent check: a live refresh by a
	// peer (or a queued sibling goroutine) legitimately has one
	// outstanding, and only the post-lock re-read can tell it from a dead
	// one. A leftover intent from a crash still surfaces in lockedRefresh.
	cred, st, err := r.store.loadState(r.region, false)
	if err != nil {
		return "", err
	}
	switch st {
	case KimiCredentialMissing:
		return "", ErrKimiCredentialMissing
	case KimiCredentialRevoked:
		return "", ErrKimiCredentialUnauthorized
	case KimiCredentialUncertain:
		return "", ErrKimiCredentialUncertain
	}
	if !kimiNeedsRefresh(cred, force, r.now()) {
		if r.hookAfterLoad != nil {
			r.hookAfterLoad()
		}
		current, state, err := r.store.Load(r.region)
		if err != nil {
			return "", err
		}
		if state == KimiCredentialValid && !kimiNeedsRefresh(current, false, r.now()) {
			return current.AccessToken, nil
		}
	}
	if r.hookAfterLoad != nil {
		r.hookAfterLoad()
	}
	return r.lockedRefresh(ctx, cred, force)
}

// lockedRefresh runs the refresh critical section under the identity lock:
// re-read, intent, request, commit, intent cleanup.
func (r *KimiCredentialRefresher) lockedRefresh(ctx context.Context, preLock KimiCredential, force bool) (string, error) {
	credPath, lockPath, intentPath, err := r.store.paths(r.region)
	if err != nil {
		return "", err
	}
	// Windows retains the intent on observed failures but cannot guarantee it
	// survives a power loss. Other platforms require a durable directory flush.
	if !kimiDirSyncDurable && runtime.GOOS != "windows" {
		return "", fmt.Errorf("%w: no durable directory flush on %s", ErrKimiStoreUnsupported, runtime.GOOS)
	}
	scoped, release, err := r.store.acquireScopedLock(ctx, lockPath, credPath)
	if err != nil {
		return "", err
	}
	defer release()
	copy := *r
	copy.store = scoped
	r = &copy

	// Post-lock re-read: a peer may have rotated, logged out, or logged in
	// again while we waited.
	cred, st, err := r.store.Load(r.region)
	if err != nil {
		return "", err
	}
	switch st {
	case KimiCredentialMissing:
		// A logout won the race; a stale response must not resurrect it.
		return "", ErrKimiCredentialMissing
	case KimiCredentialRevoked:
		return "", ErrKimiCredentialUnauthorized
	case KimiCredentialUncertain:
		return "", ErrKimiCredentialUncertain
	}
	if !kimiNeedsRefresh(cred, force, r.now()) {
		return cred.AccessToken, nil
	}
	if force && (cred.Generation != preLock.Generation || cred.AccessToken != preLock.AccessToken || cred.RefreshToken != preLock.RefreshToken) {
		// A peer rotated while we waited; their credential wins.
		return cred.AccessToken, nil
	}
	if cred.RefreshToken == "" {
		return "", ErrKimiCredentialUnauthorized
	}
	deviceID := cred.DeviceID
	if deviceID == "" {
		deviceID, err = kimiDeviceID()
		if err != nil {
			return "", errors.New("kimi credentials: cannot generate device identifier")
		}
	}
	if !kimiDeviceIDPattern.MatchString(deviceID) {
		return "", errors.New("kimi credentials: invalid device identifier")
	}

	// Intent before the request: a process that dies between here
	// and the commit below leaves this marker, and the next load fails
	// closed instead of reusing a possibly rotated refresh token.
	intent := kimiRefreshIntent{
		Region:        r.region,
		Generation:    cred.Generation,
		Target:        cred.Generation + 1,
		RefreshSHA256: kimiFingerprint(cred.RefreshToken),
		StartedAt:     r.now(),
	}
	if err := r.store.writeIntent(r.region, intent); err != nil {
		return "", fmt.Errorf("kimi credentials: cannot write refresh intent: %w", err)
	}

	next, refreshErr := r.refreshRequest(ctx, cred.RefreshToken, deviceID)
	if refreshErr != nil {
		if errors.Is(refreshErr, ErrKimiCredentialUnauthorized) {
			return r.resolveUnauthorized(cred, intentPath)
		}
		if isDialFailure(refreshErr) || errors.Is(refreshErr, errKimiRefreshRateLimited) {
			if err := r.store.removeIntent(intentPath); err != nil {
				return "", fmt.Errorf("kimi credentials: cannot clear refresh intent: %w", err)
			}
			return "", refreshErr
		}
		// The server may have rotated even if the response was lost.
		return "", fmt.Errorf("%w: %w", ErrKimiCredentialUncertain, refreshErr)
	}

	next.Generation = intent.Target
	data, err := json.Marshal(next) //nolint:gosec // G117: this writes the credential file itself, private (0600)
	if err != nil {
		return "", err
	}
	if err := r.store.writeDurable(credPath, data); err != nil {
		// The server rotated the refresh token and the new credential
		// could not be persisted. Leave the intent in place: the
		// credential is now uncertain and must be logged in again, never
		// retried with the old token.
		return "", fmt.Errorf("%w: rotated credential could not be persisted", ErrKimiCredentialUncertain)
	}
	if err := r.store.removeIntent(intentPath); err != nil {
		// The new credential is committed and the stale intent is inert
		// (its target matches the committed generation), so report the
		// cleanup failure without misrepresenting the state.
		return "", fmt.Errorf("kimi credentials: rotated credential committed but intent cleanup failed: %w", err)
	}
	return next.AccessToken, nil
}

// resolveUnauthorized decides what a 401/invalid_grant means. A peer that
// rotated while our request was in flight wins: its credential is adopted,
// not tombstoned. With no peer write, the rejection is recorded as
// a tombstone so every later process reads "sign in again". Peer detection
// reads the credential alone (checkIntent=false): a moved file is a peer
// write regardless of intent bookkeeping - including our own intent, which
// is present by construction.
func (r *KimiCredentialRefresher) resolveUnauthorized(current KimiCredential, intentPath string) (string, error) {
	r.sleep(kimiUnauthorizedRecoveryWait)
	recred, st, err := r.store.loadState(r.region, false)
	if err != nil {
		return "", fmt.Errorf("kimi credentials: cannot re-read after rejection: %w", err)
	}
	{
		switch st {
		case KimiCredentialValid:
			if recred.RefreshToken != current.RefreshToken {
				if err := r.store.removeIntent(intentPath); err != nil {
					return "", fmt.Errorf("kimi credentials: peer credential found but intent cleanup failed: %w", err)
				}
				return recred.AccessToken, nil
			}
		case KimiCredentialRevoked:
			// A peer already tombstoned; nothing left to write.
			return "", ErrKimiCredentialUnauthorized
		case KimiCredentialMissing:
			return "", ErrKimiCredentialMissing
		}
	}
	credPath, _, _, perr := r.store.paths(r.region)
	if perr != nil {
		return "", perr
	}
	tombstone := KimiCredential{
		Scope:      current.Scope,
		TokenType:  current.TokenType,
		Generation: current.Generation + 1,
	}
	data, err := json.Marshal(tombstone) //nolint:gosec // G117: this writes the credential file itself, private (0600)
	if err == nil {
		err = r.store.writeDurable(credPath, data)
	}
	if err != nil {
		// The tombstone could not be committed; the intent stays so the
		// credential fails closed rather than retrying a dead token.
		return "", fmt.Errorf("%w: rejected refresh could not be tombstoned", ErrKimiCredentialUncertain)
	}
	if err := r.store.removeIntent(intentPath); err != nil {
		return "", fmt.Errorf("kimi credentials: tombstone committed but intent cleanup failed: %w", err)
	}
	return "", ErrKimiCredentialUnauthorized
}

// refreshRequest sends a single token request. An ambiguous failure must not
// be retried because the server may already have rotated the refresh token.
func (r *KimiCredentialRefresher) refreshRequest(ctx context.Context, refreshToken, deviceID string) (KimiCredential, error) {
	endpoint := r.oauthHost + "/api/oauth/token"
	var out struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
		Scope        string  `json:"scope"`
		TokenType    string  `json:"token_type"`
		Error        string  `json:"error"`
	}
	var headers http.Header
	status, err := kimiPostForDeviceWithHeaders(ctx, r.client, endpoint, url.Values{
		"client_id":     {kimiClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}, &out, deviceID, &headers)
	if err != nil {
		return KimiCredential{}, err
	}
	if status == http.StatusOK {
		if out.AccessToken == "" || out.RefreshToken == "" || out.ExpiresIn <= 0 {
			return KimiCredential{}, errors.New("kimi credentials: malformed refresh response")
		}
		tokenType := out.TokenType
		if tokenType == "" {
			tokenType = "Bearer"
		}
		return KimiCredential{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresAt: r.now() + int64(out.ExpiresIn), ExpiresIn: int64(out.ExpiresIn), Scope: out.Scope, TokenType: tokenType, DeviceID: deviceID}, nil
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden || out.Error == "invalid_grant" {
		return KimiCredential{}, fmt.Errorf("%w (status %d)", ErrKimiCredentialUnauthorized, status)
	}
	if status == http.StatusTooManyRequests {
		return KimiCredential{}, fmt.Errorf("%w: %w", errKimiRefreshRateLimited, &httpStatusError{label: "kimi credentials: refresh", status: status, header: headers})
	}
	return KimiCredential{}, fmt.Errorf("kimi credentials: refresh failed (status %d)", status)
}
