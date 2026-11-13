// Package localipc owns the private, per-agent-home endpoint through which a
// desktop uses the runtime of an already running coddy serve. The listener is
// independent of the public HTTP configuration and is protected by both the
// operating system's local transport and a per-start credential.
package localipc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

const (
	// Header authenticates the desktop bridge to the local endpoint. It is
	// deliberately separate from Authorization, which still carries the
	// browser's ordinary Coddy credential when desktop --auth is requested.
	Header          = "X-Coddy-Local-IPC"
	helloPath       = "/coddy-local-ipc/v1/hello"
	protocolVersion = 1
)

var (
	// ErrUnavailable means no usable runtime endpoint is currently recorded.
	ErrUnavailable = errors.New("no Coddy runtime IPC endpoint is available")
	// ErrAlreadyOwned means another live process holds this home's runtime lock.
	ErrAlreadyOwned = errors.New("another Coddy process already owns this agent home")
	// ErrUnsafe means the endpoint record or local transport failed an access check.
	ErrUnsafe = errors.New("the Coddy runtime IPC endpoint is not safe to use")
)

type record struct {
	Version  int    `json:"version"`
	Network  string `json:"network"`
	Address  string `json:"address"`
	PID      int    `json:"pid"`
	Instance string `json:"instance"`
	Token    string `json:"token"`
}

func recordPath(home string) string { return filepath.Join(home, ".local-ipc.json") }
func lockPath(home string) string   { return filepath.Join(home, ".local-ipc.lock") }

// resolveHome gives every spelling of the same home one IPC identity. The
// service and the desktop can be launched through different symlinks or with
// different working directories, so a merely absolute path is not enough.
func resolveHome(home string, create bool) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("local IPC: an agent home is required")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("local IPC: resolve the agent home: %w", err)
	}
	if create {
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return "", fmt.Errorf("local IPC: create the agent home: %w", err)
		}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, os.ErrNotExist) && !create {
		return "", ErrUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("local IPC: resolve the agent home: %w", err)
	}
	return filepath.Clean(resolved), nil
}

// Owner is the process-exclusive local endpoint. Claim it before constructing
// a session manager; the lock, not an HTTP port, decides which process owns the
// runtime of a home.
type Owner struct {
	home     string
	record   record
	listener net.Listener
	lock     *os.File

	closeOnce sync.Once
	closeErr  error
}

// Claim takes the per-home runtime lock and creates its local listener. No
// application handler is served until Serve is called.
func Claim(home string) (*Owner, error) {
	abs, err := resolveHome(home, true)
	if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(lockPath(abs), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("local IPC: open the owner lock: %w", err)
	}
	owned, err := tryLock(lock)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("local IPC: lock the agent home: %w", err)
	}
	if !owned {
		_ = lock.Close()
		return nil, ErrAlreadyOwned
	}
	fail := func(err error) (*Owner, error) {
		unlock(lock)
		_ = lock.Close()
		return nil, err
	}
	listener, rec, err := listen(abs)
	if err != nil {
		return fail(fmt.Errorf("local IPC: listen: %w", err))
	}
	rec.Version = protocolVersion
	rec.PID = os.Getpid()
	rec.Instance = rand.Text()
	rec.Token = rand.Text()
	if err := writeRecord(abs, rec); err != nil {
		_ = listener.Close()
		return fail(fmt.Errorf("local IPC: record the endpoint: %w", err))
	}
	return &Owner{home: abs, record: rec, listener: listener, lock: lock}, nil
}

// Serve runs the application handler on the claimed endpoint until ctx ends.
// The IPC credential is consumed at this boundary and never reaches Coddy's
// public authentication or CORS layers.
func (o *Owner) Serve(ctx context.Context, next http.Handler) error {
	if o == nil || o.listener == nil || next == nil {
		return errors.New("local IPC: an owner listener and application handler are required")
	}
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == helloPath && r.Method == http.MethodGet {
			if !o.authorized(r) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="coddy-local-ipc"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version": protocolVersion, "pid": o.record.PID, "instance": o.record.Instance,
			})
			return
		}
		if !o.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Header.Del(Header)
		next.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: root, ReadHeaderTimeout: 10 * time.Second}
	errs := make(chan error, 1)
	go func() { errs <- srv.Serve(peerCheckedListener{Listener: o.listener}) }()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
		}
		return nil
	}
}

func (o *Owner) authorized(r *http.Request) bool {
	got := strings.TrimSpace(r.Header.Get(Header))
	return len(got) > 0 && subtle.ConstantTimeCompare([]byte(got), []byte(o.record.Token)) == 1
}

// Close stops the listener and releases the home. It does not stop the
// application runtime; callers defer it beside their ordinary subsystem
// shutdown. A record written by a replacement is never removed.
func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		if o.listener != nil {
			_ = o.listener.Close()
		}
		if err := removeEndpoint(o.record); err != nil {
			o.closeErr = err
		}
		if rec, err := readRecord(o.home); err == nil && rec.Instance == o.record.Instance {
			if err := os.Remove(recordPath(o.home)); err != nil && !errors.Is(err, os.ErrNotExist) {
				o.closeErr = err
			}
		}
		if o.lock != nil {
			unlock(o.lock)
			if err := o.lock.Close(); o.closeErr == nil {
				o.closeErr = err
			}
		}
	})
	return o.closeErr
}

// Client dials the current owner of one home. Each request revalidates the
// private record, so a service restart replaces the instance credential rather
// than leaving the desktop pinned to a dead socket.
type Client struct{ home string }

// Connect verifies the record, transport peer and authenticated hello before
// returning a client. No public Coddy credential is involved in this probe.
func Connect(ctx context.Context, home string) (*Client, error) {
	abs, err := resolveHome(home, false)
	if err != nil {
		return nil, err
	}
	rec, err := readRecord(abs)
	if err != nil {
		return nil, err
	}
	client := &Client{home: abs}
	// A claimant writes its record before Runtime.Init has produced a handler.
	// A short bounded wait covers that start window without making an absent
	// service cost the desktop anything.
	deadline := time.Now().Add(3 * time.Second)
	for {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		req, reqErr := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://local"+helloPath, nil)
		if reqErr != nil {
			cancel()
			return nil, reqErr
		}
		res, doErr := client.httpClient(rec).Do(req)
		if doErr == nil {
			body, readErr := io.ReadAll(io.LimitReader(res.Body, 4096))
			_ = res.Body.Close()
			cancel()
			if readErr != nil {
				return nil, fmt.Errorf("local IPC: read the hello: %w", readErr)
			}
			if res.StatusCode == http.StatusOK {
				var hello struct {
					Version  int    `json:"version"`
					PID      int    `json:"pid"`
					Instance string `json:"instance"`
				}
				if err := json.Unmarshal(body, &hello); err == nil && hello.Version == protocolVersion &&
					hello.PID == rec.PID && hello.Instance == rec.Instance {
					return client, nil
				}
			}
			doErr = fmt.Errorf("hello answered %s", res.Status)
		} else {
			cancel()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, doErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// HTTPClient returns a streaming-capable client with no environment proxy.
// Request URLs use the placeholder host "local"; their paths and queries are
// forwarded unchanged.
func (c *Client) HTTPClient() *http.Client {
	return &http.Client{Transport: &clientTransport{home: c.home}}
}

func (c *Client) httpClient(rec record) *http.Client {
	return &http.Client{Transport: &clientTransport{home: c.home, pinned: &rec}}
}

type clientTransport struct {
	home   string
	pinned *record
}

func (t *clientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := t.pinned
	if rec == nil {
		current, err := readRecord(t.home)
		if err != nil {
			return nil, err
		}
		rec = &current
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, *rec)
		},
	}
	defer transport.CloseIdleConnections()
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = "local"
	// Preserve the browser-facing host after the desktop bridge has checked it.
	// The upstream uses it to validate same-origin writes and login cookies.
	clone.Host = req.Host
	clone.Header = req.Header.Clone()
	clone.Header.Set(Header, rec.Token)
	return transport.RoundTrip(clone)
}

func readRecord(home string) (record, error) {
	var rec record
	resolved, err := resolveHome(home, false)
	if err != nil {
		return rec, err
	}
	home = resolved
	path := recordPath(home)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return rec, ErrUnavailable
	}
	if err != nil {
		return rec, fmt.Errorf("local IPC: inspect the endpoint record: %w", err)
	}
	if err := privateRecordInfo(info); err != nil {
		return rec, fmt.Errorf("%w: %v", ErrUnsafe, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return rec, fmt.Errorf("local IPC: read the endpoint record: %w", err)
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return rec, fmt.Errorf("local IPC: decode the endpoint record: %w", err)
	}
	expectedNetwork, expectedAddress, err := endpointAddress(home)
	if err != nil {
		return rec, err
	}
	if rec.Version != protocolVersion || rec.Network != expectedNetwork || rec.Address != expectedAddress ||
		rec.PID <= 0 || len(rec.Instance) < 16 || len(rec.Token) < 16 {
		return rec, fmt.Errorf("%w: the endpoint record is invalid or incompatible", ErrUnsafe)
	}
	if !platform.ProcessAlive(rec.PID, time.Time{}) {
		return rec, ErrUnavailable
	}
	return rec, nil
}

func writeRecord(home string, rec record) error {
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = platform.WritePrivateFileReplacing(recordPath(home), append(body, '\n'))
	return err
}
