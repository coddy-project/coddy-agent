// Package httpx holds the HTTP server settings every coddy listener should
// share. The standard http.ListenAndServe builds a server with no timeouts at
// all, which is survivable on loopback and reckless anywhere else: a handful of
// clients that open a connection and never finish a request header will pin
// goroutines forever.
package httpx

import (
	"net/http"
	"time"
)

// Defaults chosen so that a slow or hostile peer cannot hold resources
// indefinitely, while a legitimate long-lived response stays untouched.
const (
	// ReadHeaderTimeout bounds how long a client may take to send request
	// headers. It does not affect the body, so a large upload is unaffected.
	ReadHeaderTimeout = 20 * time.Second
	// IdleTimeout closes keep-alive connections that carry no request.
	IdleTimeout = 120 * time.Second
	// MaxHeaderBytes caps header size well above anything coddy sends.
	MaxHeaderBytes = 1 << 20
)

// NewServer returns a server with those bounds applied to h.
//
// Deliberately absent: ReadTimeout and WriteTimeout. Both are whole-request
// deadlines, and coddy's most important responses are streams that legitimately
// stay open for as long as a model keeps talking or an operator keeps thinking
// about a permission prompt. Setting either would cut those off mid-flight,
// which is exactly the failure the header and idle bounds are meant to avoid
// introducing elsewhere.
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: ReadHeaderTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
	}
}
