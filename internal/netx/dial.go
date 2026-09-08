// Package netx builds outbound connections that have to cross somebody else's
// network: through a proxy, under TLS, or both. Coddy already had this logic,
// but only behind the gateway build tags, where nothing else could reach it.
package netx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// Options describe one outbound leg.
type Options struct {
	// Proxy routes the connection through http, https, socks5 or socks5h.
	// Empty falls back to the standard environment variables.
	Proxy string
	// CAFile verifies a peer whose certificate is signed privately.
	CAFile string
	// InsecureSkipVerify accepts any certificate. Never a default, and every
	// caller that sets it is expected to say so out loud in its log.
	InsecureSkipVerify bool
	// DialTimeout bounds establishing the connection, not using it.
	DialTimeout time.Duration
}

func (o Options) dialTimeout() time.Duration {
	if o.DialTimeout <= 0 {
		return 15 * time.Second
	}
	return o.DialTimeout
}

// TLSConfig builds the client configuration for reaching serverName.
func (o Options) TLSConfig(serverName string) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         serverName,
		InsecureSkipVerify: o.InsecureSkipVerify, //nolint:gosec // opt-in, and the caller logs it
	}
	if path := strings.TrimSpace(o.CAFile); path != "" {
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_file %q contains no certificate", path)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// DialFunc returns a dialer that reaches an address through the configured
// proxy, or directly when none is set.
func (o Options) DialFunc() (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	raw := strings.TrimSpace(o.Proxy)
	base := &net.Dialer{Timeout: o.dialTimeout(), KeepAlive: 30 * time.Second}
	if raw == "" {
		return base.DialContext, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url %q: %w", raw, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		d, derr := proxy.FromURL(u, base)
		if derr != nil {
			return nil, fmt.Errorf("socks5 proxy: %w", derr)
		}
		if ctxDialer, ok := d.(proxy.ContextDialer); ok {
			return ctxDialer.DialContext, nil
		}
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.Dial(network, addr)
		}, nil
	case "http", "https":
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialViaCONNECT(ctx, base, u, network, addr, o)
		}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (want http, https, socks5 or socks5h)", u.Scheme)
	}
}

// HTTPClient builds a client for ordinary requests over this leg.
func (o Options) HTTPClient() (*http.Client, error) {
	tr, err := o.Transport()
	if err != nil {
		return nil, err
	}
	// No client Timeout: a swarm request can legitimately stream for as long as
	// a turn lasts, and a whole-request deadline would cut it.
	return &http.Client{Transport: tr}, nil
}

// Transport builds the round tripper for this leg.
func (o Options) Transport() (*http.Transport, error) {
	tlsCfg, err := o.TLSConfig("")
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   o.dialTimeout(),
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
		ForceAttemptHTTP2:     true,
	}
	if strings.TrimSpace(o.Proxy) != "" {
		dial, derr := o.DialFunc()
		if derr != nil {
			return nil, derr
		}
		tr.DialContext = dial
	} else {
		tr.Proxy = http.ProxyFromEnvironment
		tr.DialContext = (&net.Dialer{Timeout: o.dialTimeout(), KeepAlive: 30 * time.Second}).DialContext
	}
	return tr, nil
}

// dialViaCONNECT opens a tunnel through an HTTP proxy. This is the path a
// dial-out node takes when the only way out of its network is a proxy, and it
// has to yield a raw stream rather than an http.Client, because what follows on
// that stream is not an ordinary request.
func dialViaCONNECT(ctx context.Context, base *net.Dialer, proxyURL *url.URL, network, addr string, o Options) (net.Conn, error) {
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		if strings.EqualFold(proxyURL.Scheme, "https") {
			proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "443")
		} else {
			proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "80")
		}
	}
	conn, err := base.DialContext(ctx, network, proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", proxyAddr, err)
	}
	if strings.EqualFold(proxyURL.Scheme, "https") {
		tlsCfg, terr := o.TLSConfig(proxyURL.Hostname())
		if terr != nil {
			_ = conn.Close()
			return nil, terr
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if herr := tlsConn.HandshakeContext(ctx); herr != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("proxy tls handshake: %w", herr)
		}
		conn = tlsConn
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		if pw, ok := proxyURL.User.Password(); ok {
			req.Header.Set("Proxy-Authorization", basicAuth(proxyURL.User.Username(), pw))
		}
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT: %w", err)
	}
	res, spliced, err := readCONNECTResponse(conn, req)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT to %s: %s", addr, res.Status)
	}
	return spliced, nil
}
