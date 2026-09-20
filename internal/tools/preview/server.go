package preview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
)

// loopbackNames are the names a browser on this machine reaches a loopback
// listener under.
var loopbackNames = []string{"localhost", "127.0.0.1", "::1"}

// server is one preview server: a listener on a free port over one directory.
// listen opens it, serve starts answering, and from then on it is controlled
// through the bgtask.Handle methods.
type server struct {
	root       *os.Root
	ln         net.Listener
	bindHost   string
	publicHost string
	// loopback is a property of the bound address, not of how the bind host was
	// spelled: "localhost.", "LOCALHOST" or any other name resolving to it
	// counts as loopback too, and only a loopback listener gets the Host
	// allowlist against DNS rebinding.
	loopback bool
	// registryKey is the liveServers key under which the tool registers this
	// server: task ids are per-session, so the session id is part of it.
	registryKey string

	srv      *http.Server
	done     chan struct{}
	serveErr error
	stopOnce sync.Once
}

// listen opens dir and takes a free port on host. Nothing is served yet: the
// caller learns the address first, so the task record can carry it from its
// first snapshot, and closes the server if the task is then refused.
func listen(dir, host, publicHost string) (*server, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	// Port 0 asks the system for a free one, which is the only race-free way
	// to find it: the port is ours from the moment we learn its number.
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("listen on %s: %w", host, err)
	}
	// Whether the Host allowlist applies is decided by the address the listener
	// actually got, so a bind hostname that resolves to loopback ("localhost.",
	// "LOCALHOST") keeps the DNS-rebinding protection the literal "localhost"
	// has.
	loopback := false
	if ta, ok := ln.Addr().(*net.TCPAddr); ok {
		loopback = ta.IP.IsLoopback()
	}
	return &server{
		root:       root,
		ln:         ln,
		bindHost:   host,
		publicHost: publicHost,
		loopback:   loopback,
		done:       make(chan struct{}),
	}, nil
}

// port is the port the system handed out.
func (s *server) port() int {
	if addr, ok := s.ln.Addr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
}

// url is the address to hand to the operator, ending in a slash.
func (s *server) url() string {
	host := s.publicHost
	if host == "" {
		host = s.bindHost
		// A wildcard bind is not an address anybody can open.
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			host = "localhost"
		}
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(s.port())), Path: "/"}
	return u.String()
}

// allowedHosts is the Host allowlist for a loopback bind, and nil (any host)
// for a bind the operator opened to the network on purpose.
func (s *server) allowedHosts() []string {
	if !s.loopback {
		return nil
	}
	hosts := append([]string(nil), loopbackNames...)
	hosts = append(hosts, s.bindHost)
	if s.publicHost != "" {
		hosts = append(hosts, s.publicHost)
	}
	return hosts
}

// serve starts answering requests on a goroutine, logging them to log.
func (s *server) serve(log io.Writer) {
	s.srv = httpx.NewServer("", newHandler(s.root, handlerOptions{
		AllowedHosts: s.allowedHosts(),
		Log:          log,
	}))
	go func() {
		err := s.srv.Serve(s.ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		s.serveErr = err
		_ = s.root.Close()
		close(s.done)
	}()
}

// close releases a server that never got to serve.
func (s *server) close() {
	_ = s.ln.Close()
	_ = s.root.Close()
}

// Wait blocks until the server is down. It implements bgtask.Handle.
func (s *server) Wait() (int, error) {
	defer func() {
		if s.registryKey != "" {
			liveServers.Delete(s.registryKey)
		}
	}()
	<-s.done
	if s.serveErr != nil {
		return 1, s.serveErr
	}
	return 0, nil
}

// Stop shuts the server down, giving open requests the grace to finish and
// cutting whatever is left after it: a browser's keep-alive connection must not
// hold the port.
func (s *server) Stop(grace time.Duration) error {
	s.stopOnce.Do(func() {
		if s.srv == nil {
			// The task was stopped before serve ran: close what listen opened,
			// and mark the server down so a Wait does not hang.
			s.close()
			close(s.done)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			_ = s.srv.Close()
		}
	})
	return nil
}

// PID is 0: the server is goroutines of this process, not a process of its own.
func (s *server) PID() int { return 0 }

// ProcessStartedAt is zero for the same reason.
func (s *server) ProcessStartedAt() time.Time { return time.Time{} }

// servesSameDir reports whether the directory the server opened is still the
// one at dir: a build that deletes and recreates it leaves this server
// answering from the deleted inode.
func (s *server) servesSameDir(dir string) bool {
	opened, err := s.root.Stat(".")
	if err != nil {
		return false
	}
	current, err := os.Stat(dir)
	if err != nil {
		return false
	}
	return os.SameFile(opened, current)
}
