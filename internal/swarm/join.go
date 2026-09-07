package swarm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// Heartbeat pacing. A node refreshes well inside its lease so one lost request
// is not enough to make it vanish from the relay.
const (
	heartbeatFraction = 3
	minHeartbeat      = 5 * time.Second
	maxBackoff        = 60 * time.Second
	initialBackoff    = time.Second
)

// ErrNameConflict is returned when the relay holds that name for somebody else.
var ErrNameConflict = errors.New("swarm: node name is held by another node")

// SecretStore remembers the lease secret a relay minted, so a restart re-claims
// the same name instead of waiting for the old lease to expire.
type SecretStore interface {
	Load(relayURL, name string) (string, bool)
	Save(relayURL, name, secret string) error
}

// JoinOptions describe one registration into one relay.
type JoinOptions struct {
	// RelayURL is the parent relay's origin.
	RelayURL string
	// Name is the name to claim. Empty falls back to the host name.
	Name string
	// Kind is agent or relay.
	Kind string
	// PairingToken authorises the registration.
	PairingToken string
	// AdvertiseURL is where the relay can reach this node. Empty selects the
	// tunnel transport, where the node dials out instead.
	AdvertiseURL string
	// NodeToken is this node's own bearer credential, handed to the relay so it
	// can authenticate when it proxies.
	NodeToken string
	// Version and Labels are cosmetic, for topology views.
	Version string
	Labels  map[string]string
	// InstanceUUID identifies this process. Generated when empty.
	InstanceUUID string
	// Dial carries proxy and TLS settings for reaching the relay.
	Dial netx.Options
	// Secrets persists the lease secret across restarts.
	Secrets SecretStore
	// Log receives diagnostics.
	Log *slog.Logger
}

// Client keeps one node registered in one relay.
type Client struct {
	opts JoinOptions
	hc   *http.Client
	log  *slog.Logger

	mu          sync.Mutex
	leaseSecret string
	ttl         time.Duration
	generation  uint64
	online      bool
}

// NewClient validates the options and returns a client that has not yet
// registered.
func NewClient(opts JoinOptions) (*Client, error) {
	opts.RelayURL = strings.TrimRight(strings.TrimSpace(opts.RelayURL), "/")
	if opts.RelayURL == "" {
		return nil, fmt.Errorf("swarm join: relay url is required")
	}
	if strings.TrimSpace(opts.Name) == "" {
		host, err := os.Hostname()
		if err != nil || strings.TrimSpace(host) == "" {
			return nil, fmt.Errorf("swarm join: name is required and the host name is unavailable")
		}
		opts.Name = sanitiseNodeName(host)
	}
	if err := ValidateNodeName(opts.Name); err != nil {
		return nil, fmt.Errorf("swarm join: %w", err)
	}
	if opts.Kind == "" {
		opts.Kind = KindAgent
	}
	if opts.InstanceUUID == "" {
		id, err := randomID()
		if err != nil {
			return nil, err
		}
		opts.InstanceUUID = id
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	hc, err := opts.Dial.HTTPClient()
	if err != nil {
		return nil, fmt.Errorf("swarm join %s: %w", opts.RelayURL, err)
	}
	if opts.Dial.InsecureSkipVerify {
		opts.Log.Warn("swarm join: certificate verification disabled", "relay", opts.RelayURL)
	}
	c := &Client{opts: opts, hc: hc, log: opts.Log}
	if opts.Secrets != nil {
		if secret, ok := opts.Secrets.Load(opts.RelayURL, opts.Name); ok {
			c.leaseSecret = secret
		}
	}
	return c, nil
}

// Name reports the node name this client claims.
func (c *Client) Name() string { return c.opts.Name }

// Transport reports which transport this registration asks for.
func (c *Client) Transport() string {
	if strings.TrimSpace(c.opts.AdvertiseURL) == "" {
		return TransportTunnel
	}
	return TransportDirect
}

// LeaseSecret returns the secret proving this node owns its name, if it has one.
func (c *Client) LeaseSecret() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.leaseSecret
}

// Online reports whether the last registration attempt succeeded.
func (c *Client) Online() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.online
}

// Register performs one registration or heartbeat.
func (c *Client) Register(ctx context.Context) error {
	c.mu.Lock()
	secret := c.leaseSecret
	c.mu.Unlock()

	req := RegisterRequest{
		Name:         c.opts.Name,
		Kind:         c.opts.Kind,
		Transport:    c.Transport(),
		AdvertiseURL: strings.TrimSpace(c.opts.AdvertiseURL),
		InstanceUUID: c.opts.InstanceUUID,
		Token:        c.opts.NodeToken,
		LeaseSecret:  secret,
		Version:      c.opts.Version,
		Labels:       c.opts.Labels,
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("swarm join: %w", err)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opts.RelayURL+"/swarm/register", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.opts.PairingToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.opts.PairingToken)
	}
	res, err := c.hc.Do(httpReq)
	if err != nil {
		c.markOffline()
		return fmt.Errorf("swarm join %s: %w", c.opts.RelayURL, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusConflict:
		c.markOffline()
		// The relay holds this name for a lease we cannot prove we own. Dropping
		// the stored secret would not help: it is either stale or was never
		// ours, and retrying with it would keep failing the same way.
		return fmt.Errorf("%w: %s", ErrNameConflict, c.opts.Name)
	default:
		c.markOffline()
		return fmt.Errorf("swarm join %s: %s: %s", c.opts.RelayURL, res.Status, strings.TrimSpace(string(body)))
	}

	var out RegisterResponse
	if err := json.Unmarshal(body, &out); err != nil {
		c.markOffline()
		return fmt.Errorf("swarm join %s: decode response: %w", c.opts.RelayURL, err)
	}
	c.mu.Lock()
	fresh := c.leaseSecret != out.LeaseSecret
	c.leaseSecret = out.LeaseSecret
	c.ttl = time.Duration(out.TTLSeconds) * time.Second
	c.generation = out.Generation
	c.online = true
	c.mu.Unlock()
	if fresh && c.opts.Secrets != nil {
		if err := c.opts.Secrets.Save(c.opts.RelayURL, c.opts.Name, out.LeaseSecret); err != nil {
			c.log.Warn("swarm join: could not persist the lease secret", "relay", c.opts.RelayURL, "error", err)
		}
	}
	return nil
}

func (c *Client) markOffline() {
	c.mu.Lock()
	c.online = false
	c.mu.Unlock()
}

// heartbeatInterval refreshes at a third of the lease so a single lost request
// does not drop the node out of the relay.
func (c *Client) heartbeatInterval() time.Duration {
	c.mu.Lock()
	ttl := c.ttl
	c.mu.Unlock()
	if ttl <= 0 {
		ttl = 90 * time.Second
	}
	d := ttl / heartbeatFraction
	if d < minHeartbeat {
		d = minHeartbeat
	}
	return d
}

// Run keeps the registration alive until ctx is cancelled.
//
// Failures back off exponentially with jitter. The jitter is not decoration: a
// relay restart makes every node it served notice at the same moment, and
// without it they would all retry in lockstep and keep knocking the relay over
// as it comes up.
func (c *Client) Run(ctx context.Context) error {
	backoff := initialBackoff
	for {
		err := c.Register(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var wait time.Duration
		switch {
		case err == nil:
			backoff = initialBackoff
			wait = c.heartbeatInterval()
		case errors.Is(err, ErrNameConflict):
			// Retrying quickly cannot help; the other lease has to expire or an
			// operator has to intervene.
			c.log.Error("swarm join refused", "relay", c.opts.RelayURL, "node", c.opts.Name, "error", err)
			wait = maxBackoff
		default:
			c.log.Warn("swarm join failed", "relay", c.opts.RelayURL, "node", c.opts.Name, "error", err)
			wait = backoff
			backoff = nextBackoff(backoff)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jitter(wait)):
		}
	}
}

func nextBackoff(current time.Duration) time.Duration {
	next := time.Duration(math.Min(float64(current)*2, float64(maxBackoff)))
	if next < initialBackoff {
		return initialBackoff
	}
	return next
}

// jitter spreads a wait over [50%, 100%] of its nominal value.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return d
	}
	n := uint64(0)
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	half := d / 2
	//nolint:gosec // spreading a retry, not deriving a secret
	return half + time.Duration(n%uint64(half+1))
}

func randomID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("swarm: instance id entropy: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// sanitiseNodeName turns a host name into something usable as a path segment,
// since that is what a node name becomes on every hop.
func sanitiseNodeName(host string) string {
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == '.':
			b.WriteRune('-')
		}
		if b.Len() >= MaxNodeNameLen {
			break
		}
	}
	return b.String()
}
