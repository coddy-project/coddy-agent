package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const opencodeConsoleURL = "https://opencode.ai/console"

const openCodeClientID = "coddy"

// EnvOpenCodeConsoleURL redirects account requests for a local test stand.
// Configuration cannot redirect account or refresh tokens to another host.
const EnvOpenCodeConsoleURL = "CODDY_OPENCODE_CONSOLE_URL"

// opencodeRefreshLocks serializes, per credential file, a refresh with the
// writes and the removal of that file, so a refresh never overwrites a newer
// login or brings back one a logout removed. A file of its own per row means a
// refresh that waits on Console holds up only the requests of its own row.
var opencodeRefreshLocks sync.Map // credential path -> *sync.Mutex

func opencodeRefreshLock(path string) *sync.Mutex {
	mu, _ := opencodeRefreshLocks.LoadOrStore(path, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func openCodeConsoleURL() string {
	if value := strings.TrimSpace(os.Getenv(EnvOpenCodeConsoleURL)); value != "" {
		return strings.TrimRight(value, "/")
	}
	return opencodeConsoleURL
}

type openCodeToken struct {
	Access  string  `json:"access_token"`
	Refresh string  `json:"refresh_token"`
	Expires int64   `json:"expires_in"`
	OrgID   *string `json:"org_id"`
	Error   string  `json:"error"`
}

// OpenCodeDeviceLogin is an authorization attempt. The device code stays
// private; only the verification URL and user code are displayed.
type OpenCodeDeviceLogin struct {
	UserCode string
	URL      string
	code     string
	interval time.Duration
	expires  time.Time
	client   *http.Client
}

// StartOpenCodeDeviceLogin starts the same Console account flow as OpenCode's
// device method, using the provider row's transport for every account request.
func StartOpenCodeDeviceLogin(ctx context.Context, proxySetting string) (*OpenCodeDeviceLogin, error) {
	hc, err := HTTPClientForProviderProxy(proxySetting)
	if err != nil {
		return nil, err
	}
	client := hc
	var device struct {
		DeviceCode   string `json:"device_code"`
		UserCode     string `json:"user_code"`
		Verification string `json:"verification_uri_complete"`
		Expires      int64  `json:"expires_in"`
		Interval     int64  `json:"interval"`
	}
	if err := openCodeAccountRequest(ctx, client, "/auth/device/code", map[string]any{"client_id": openCodeClientID, "supports_org_scope": true}, "", &device); err != nil {
		return nil, err
	}
	if device.DeviceCode == "" || device.UserCode == "" || device.Expires <= 0 || device.Expires > 86400 || device.Interval < 0 || device.Interval > 3600 {
		return nil, errors.New("opencode: invalid device authorization response")
	}
	base, err := url.Parse(openCodeConsoleURL() + "/")
	if err != nil {
		return nil, errors.New("opencode: invalid Console URL")
	}
	// Stricter than the upstream client, which checks the scheme only: the
	// confirmation page must sit on the Console origin itself, so a stand-in
	// or a compromised account endpoint cannot send the operator to a
	// look-alike that harvests the code. If Console ever moves the page to
	// another host, this is the line to relax.
	verification, err := base.Parse(device.Verification)
	if err != nil || device.Verification == "" || (verification.Scheme != "https" && verification.Scheme != "http") || verification.User != nil || verification.Host != base.Host || verification.Scheme != base.Scheme {
		return nil, errors.New("opencode: invalid device verification URL")
	}
	return &OpenCodeDeviceLogin{UserCode: device.UserCode, URL: verification.String(), code: device.DeviceCode, interval: time.Duration(device.Interval) * time.Second, expires: time.Now().Add(time.Duration(device.Expires) * time.Second), client: client}, nil
}

// Wait completes authorization, discovers the account and selected organization,
// and atomically stores tokens. Ctrl-C or the server's expiry cancels the wait.
func (login *OpenCodeDeviceLogin) Wait(ctx context.Context, authPath string) error {
	return login.WaitWith(ctx, func(ctx context.Context, credential []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return SaveOpenCodeAuthFile(authPath, credential)
	})
}

// WaitWith hands the completed credential to its owner. HTTP sign-in uses this
// boundary to serialize storage with cancellation, replacement and logout.
func (login *OpenCodeDeviceLogin) WaitWith(ctx context.Context, persist func(context.Context, []byte) error) error {
	if login == nil || login.client == nil {
		return errors.New("opencode: missing device authorization attempt")
	}
	if persist == nil {
		return errors.New("opencode: missing credential storage callback")
	}
	ctx, cancel := context.WithDeadline(ctx, login.expires)
	defer cancel()
	interval := login.interval
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		var token openCodeToken
		err := openCodeAccountRequest(ctx, login.client, "/auth/device/token", map[string]any{"grant_type": "urn:ietf:params:oauth:grant-type:device_code", "device_code": login.code, "client_id": openCodeClientID}, "", &token)
		if err != nil {
			return err
		}
		switch token.Error {
		case "authorization_pending":
			if interval == 0 {
				interval = time.Second
			}
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "":
		default:
			return openCodeGrantError(token.Error)
		}
		credential := &opencodeAuthFile{ObtainedAt: time.Now().UTC().Format(time.RFC3339)}
		if err := applyOpenCodeToken(credential, token); err != nil {
			return err
		}
		if err := discoverOpenCodeAccount(ctx, login.client, credential); err != nil {
			return err
		}
		data, err := json.MarshalIndent(credential, "", "  ") //nolint:gosec // G117: the credential goes to persist, which its callers store with SaveOpenCodeAuthFile, private (0600)
		if err != nil {
			return err
		}
		return persist(ctx, data)
	}
}

// SaveOpenCodeAuthFile validates and atomically installs a completed login.
func SaveOpenCodeAuthFile(path string, data []byte) error {
	var credential opencodeAuthFile
	if err := json.Unmarshal(data, &credential); err != nil {
		return errors.New("opencode: invalid credential")
	}
	if credential.APIKey == "" && (credential.AccessToken == "" || credential.RefreshToken == "" || credential.ExpiresAt <= 0) {
		return errors.New("opencode: incomplete credential")
	}
	refresh := opencodeRefreshLock(path)
	refresh.Lock()
	defer refresh.Unlock()
	return saveOpenCodeCredential(path, &credential)
}

func discoverOpenCodeAccount(ctx context.Context, client *http.Client, credential *opencodeAuthFile) error {
	var user struct {
		Email string `json:"email"`
	}
	if err := openCodeAccountRequest(ctx, client, "/api/user", nil, credential.AccessToken, &user); err != nil {
		return err
	}
	var orgs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := openCodeAccountRequest(ctx, client, "/api/orgs", nil, credential.AccessToken, &orgs); err != nil {
		return err
	}
	sort.Slice(orgs, func(i, j int) bool {
		if orgs[i].Name == orgs[j].Name {
			return orgs[i].ID < orgs[j].ID
		}
		return orgs[i].Name < orgs[j].Name
	})
	credential.Email = user.Email
	for _, org := range orgs {
		if credential.OrgID == "" || credential.OrgID == org.ID {
			credential.OrgID, credential.OrgName = org.ID, org.Name
			return nil
		}
	}
	if credential.OrgID != "" {
		return errors.New("opencode: selected organization was not found; sign in again")
	}
	return nil
}

func saveOpenCodeCredential(path string, credential *opencodeAuthFile) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("opencode: credential path is empty")
	}
	data, err := json.MarshalIndent(credential, "", "  ") //nolint:gosec // G117: this writes the credential file itself, private (0600)
	if err != nil {
		return err
	}
	opencodeAuthMu.Lock()
	defer opencodeAuthMu.Unlock()
	return writePrivateFileAtomic(path, data)
}

func applyOpenCodeToken(credential *opencodeAuthFile, token openCodeToken) error {
	if token.Access == "" || token.Refresh == "" || token.Expires <= 0 || token.Expires > 31536000 {
		return errors.New("opencode: incomplete account token response")
	}
	credential.AccessToken, credential.RefreshToken = token.Access, token.Refresh
	credential.ExpiresAt = time.Now().Add(time.Duration(token.Expires) * time.Second).Unix()
	if token.OrgID != nil {
		if credential.OrgID != *token.OrgID {
			// The token carries the new organization's id, never its name;
			// the account discovery fills the name. Storing the id here would
			// show up as the organization's display name.
			credential.OrgName = ""
		}
		credential.OrgID = *token.OrgID
	}
	return nil
}

func openCodeCredential(ctx context.Context, explicit, path string, client *http.Client) (key, orgID string, err error) {
	if explicit != "" {
		return explicit, "", nil
	}
	if client == nil {
		return "", "", errors.New("opencode: missing account HTTP client")
	}
	refresh := opencodeRefreshLock(path)
	refresh.Lock()
	defer refresh.Unlock()
	credential, err := loadOpenCodeAuth(path)
	if err != nil || credential == nil {
		return "", "", err
	}
	if credential.RefreshToken == "" {
		if credential.AccessToken != "" {
			return "", "", errors.New("opencode: missing refresh token; run providers login again")
		}
		return credential.APIKey, "", nil
	}
	if !openCodeAccessUsable(credential) {
		var token openCodeToken
		if err := openCodeAccountRequest(ctx, client, "/auth/device/token", map[string]any{"grant_type": "refresh_token", "refresh_token": credential.RefreshToken, "client_id": openCodeClientID}, "", &token); err != nil {
			return "", "", err
		}
		if token.Error != "" {
			// Another Coddy process on the same home (a `coddy serve` next to
			// a console) may have rotated the refresh token while this request
			// was in flight. The mutex above is per process; the pair the other
			// one stored is on disk, and it is as good as a refresh of our own.
			if current, err := loadOpenCodeAuth(path); err == nil && current != nil &&
				current.RefreshToken != credential.RefreshToken && openCodeAccessUsable(current) {
				return current.AccessToken, current.OrgID, nil
			}
			return "", "", openCodeGrantError(token.Error)
		}
		if err := applyOpenCodeToken(credential, token); err != nil {
			return "", "", err
		}
		if credential.OrgName == "" && credential.OrgID != "" {
			// A refresh that moved the organization lost its name. Refill it
			// from the account, best effort: a display name never fails the
			// inference request that asked for the credential.
			_ = discoverOpenCodeAccount(ctx, client, credential)
		}
		if err := saveOpenCodeCredential(path, credential); err != nil {
			return "", "", err
		}
	}
	return credential.AccessToken, credential.OrgID, nil
}

// openCodeAccessUsable reports whether the stored access token outlives the
// next minute, so a request sent with it does not expire on the way.
func openCodeAccessUsable(credential *opencodeAuthFile) bool {
	return credential.AccessToken != "" && credential.ExpiresAt > time.Now().Add(60*time.Second).Unix()
}

func openCodeGrantError(code string) error {
	switch code {
	case "invalid_grant", "access_denied", "expired_token":
		return errors.New("opencode: authorization expired or was denied; run providers login again")
	default:
		return errors.New("opencode: account authorization failed")
	}
}

// Account errors deliberately omit upstream bodies: they may echo a token.
func openCodeAccountRequest(ctx context.Context, client *http.Client, path string, body map[string]any, bearer string, out any) error {
	if client == nil {
		return errors.New("opencode: missing account HTTP client")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader, method = bytes.NewReader(data), http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, openCodeConsoleURL()+path, reader)
	if err != nil {
		return errors.New("opencode: invalid account endpoint")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", opencodeUserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	guarded := *client
	guarded.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := guarded.Do(req)
	if err != nil {
		return fmt.Errorf("opencode account request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && (path != "/auth/device/token" || resp.StatusCode != http.StatusBadRequest) {
		// Typed, so a refresh that hit a passing 5xx or 429 under an inference
		// request is retried like that request's own; the body stays out.
		return &httpStatusError{label: "opencode account request", status: resp.StatusCode, header: resp.Header}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return errors.New("opencode: invalid account response")
	}
	return nil
}
