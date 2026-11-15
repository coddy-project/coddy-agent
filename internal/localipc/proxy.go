package localipc

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// ProxyHandler forwards every API route, including SSE and response bodies
// streamed in both directions, to the verified local runtime. A negative flush
// interval asks ReverseProxy to flush each write instead of holding SSE frames.
func (c *Client) ProxyHandler() http.Handler {
	target := &url.URL{Scheme: "http", Host: "local"}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = c.HTTPClient().Transport
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "the Coddy service is unavailable: "+err.Error(), http.StatusBadGateway)
	}
	return proxy
}
