package llm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

const kimiClientID = "17e5f671-d194-4dfb-9706-5516cb48c098"

// kimiOAuthEndpoint maps the configured region to its OAuth endpoint. The
// region vocabulary is the one config validates: "china", "global" and the
// empty string, which means global.
func kimiOAuthEndpoint(region string) (string, error) {
	switch region {
	case "", "global":
		return "https://auth.kimi.ai", nil
	case "china":
		return "https://auth.kimi.com", nil
	default:
		return "", errors.New("kimi: invalid region")
	}
}

// kimiSiteHost returns the verification site for a configured region, or an
// empty string when the region is unknown.
func kimiSiteHost(region string) string {
	switch region {
	case "", "global":
		return "www.kimi.ai"
	case "china":
		return "www.kimi.com"
	default:
		return ""
	}
}

// ValidateKimiVerificationURL rejects destinations not supported by evidence.
func ValidateKimiVerificationURL(region, raw string) error {
	host := kimiSiteHost(region)
	if host == "" {
		return errors.New("kimi: untrusted verification URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != host || u.User != nil || u.Fragment != "" || u.Path != "/code/authorize_device" || u.RawPath != "" || u.ForceQuery {
		return errors.New("kimi: untrusted verification URL")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) > 1 || (len(q) == 1 && (len(q["user_code"]) != 1 || q.Get("user_code") == "")) {
		return errors.New("kimi: invalid verification URL query")
	}
	return nil
}

type KimiToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	ExpiresIn    int64  `json:"expires_in"`
	DeviceID     string `json:"device_id,omitempty"`
}

type KimiDeviceLogin struct {
	UserCode string
	URL      string
	code     string
	endpoint string
	client   *http.Client
	deviceID string
	interval time.Duration
	expires  time.Time
}

// StartKimiDeviceLogin starts a device flow over the configured provider proxy.
// The region is the one config validates: "china", "global" or "" for global.
func StartKimiDeviceLogin(ctx context.Context, region, proxy string) (*KimiDeviceLogin, error) {
	endpoint, err := kimiOAuthEndpoint(region)
	if err != nil {
		return nil, err
	}
	client, err := HTTPClientForProviderProxy(proxy)
	if err != nil {
		return nil, err
	}
	return startKimiDeviceLogin(ctx, region, client, endpoint)
}

func startKimiDeviceLogin(ctx context.Context, region string, client *http.Client, endpoint string) (*KimiDeviceLogin, error) {
	deviceID, err := kimiDeviceID()
	if err != nil {
		return nil, errors.New("kimi: cannot generate device identifier")
	}
	var response struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URL        string `json:"verification_uri_complete"`
		Expires    int64  `json:"expires_in"`
		Interval   *int64 `json:"interval"`
	}
	status, err := kimiPostForDevice(ctx, client, endpoint+"/api/oauth/device_authorization", url.Values{"client_id": {kimiClientID}}, &response, deviceID)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || response.DeviceCode == "" || response.UserCode == "" || response.Expires <= 0 || response.Expires > 86400 {
		return nil, errors.New("kimi: invalid device authorization response")
	}
	if err := ValidateKimiVerificationURL(region, response.URL); err != nil {
		return nil, err
	}
	interval := int64(5)
	if response.Interval != nil {
		interval = *response.Interval
	}
	if interval < 0 || interval > 3600 {
		return nil, errors.New("kimi: invalid polling interval")
	}
	if interval == 0 {
		interval = 1
	}
	return &KimiDeviceLogin{UserCode: response.UserCode, URL: response.URL, code: response.DeviceCode, endpoint: endpoint, client: client, deviceID: deviceID, interval: time.Duration(interval) * time.Second, expires: time.Now().Add(time.Duration(response.Expires) * time.Second)}, nil
}

// Wait polls until authorization succeeds, expires, is denied or is cancelled.
func (login *KimiDeviceLogin) Wait(ctx context.Context) (KimiToken, error) {
	if login == nil || login.client == nil {
		return KimiToken{}, errors.New("kimi: missing device authorization")
	}
	ctx, cancel := context.WithDeadline(ctx, login.expires)
	defer cancel()
	deviceID := login.deviceID
	if deviceID == "" {
		var err error
		deviceID, err = kimiDeviceID()
		if err != nil {
			return KimiToken{}, errors.New("kimi: cannot generate device identifier")
		}
	}
	interval := login.interval
	for {
		// The first poll goes out immediately, as upstream oauth-manager does;
		// the interval only separates subsequent polls.
		var response struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			Expires int64  `json:"expires_in"`
			Error   string `json:"error"`
		}
		status, err := kimiPostForDevice(ctx, login.client, login.endpoint+"/api/oauth/token", url.Values{"client_id": {kimiClientID}, "device_code": {login.code}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, &response, deviceID)
		if err != nil {
			return KimiToken{}, err
		}
		if status == http.StatusOK && response.Access != "" && response.Refresh != "" && response.Expires > 0 && response.Expires <= 31536000 {
			return KimiToken{AccessToken: response.Access, RefreshToken: response.Refresh, ExpiresAt: time.Now().Add(time.Duration(response.Expires) * time.Second).Unix(), ExpiresIn: response.Expires, DeviceID: deviceID}, nil
		}
		switch response.Error {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		default:
			return KimiToken{}, errors.New("kimi: device authorization failed or expired")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return KimiToken{}, ctx.Err()
		case <-timer.C:
		}
	}
}

var kimiDeviceIdentity struct {
	sync.Once
	id  string
	err error
}

// kimiDeviceID is a random process-scoped identifier, not a host fingerprint.
func kimiDeviceID() (string, error) {
	kimiDeviceIdentity.Do(func() {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			kimiDeviceIdentity.err = err
			return
		}
		b[6] = b[6]&0x0f | 0x40
		b[8] = b[8]&0x3f | 0x80
		kimiDeviceIdentity.id = fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	})
	return kimiDeviceIdentity.id, kimiDeviceIdentity.err
}

var kimiDeviceIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func kimiPostForDevice(ctx context.Context, client *http.Client, endpoint string, form url.Values, out any, deviceID string) (int, error) {
	if client == nil {
		return 0, errors.New("kimi: missing provider HTTP client")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, errors.New("kimi: invalid OAuth endpoint")
	}
	if deviceID == "" {
		deviceID, err = kimiDeviceID()
		if err != nil {
			return 0, errors.New("kimi: cannot generate device identifier")
		}
	}
	if !kimiDeviceIDPattern.MatchString(deviceID) {
		return 0, errors.New("kimi: invalid device identifier")
	}
	req.Header.Set("User-Agent", "coddy/"+version.Get())
	req.Header.Set("X-Msh-Platform", "kimi_code_cli")
	req.Header.Set("X-Msh-Version", version.Get())
	req.Header.Set("X-Msh-Device-Id", deviceID)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	guarded := *client
	guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := guarded.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, errors.New("kimi: OAuth request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return 0, errors.New("kimi: invalid OAuth response")
	}
	return resp.StatusCode, nil
}
