package netx

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
)

// readCONNECTResponse reads the proxy's answer.
//
// It reads through a bufio.Reader, which can pull more bytes off the socket
// than the response itself. Anything left buffered belongs to whatever speaks
// next on this stream, so a leftover is an error rather than something to
// quietly drop: the alternative is a connection that looks fine and then
// mysteriously fails to parse its first frame.
func readCONNECTResponse(conn net.Conn, req *http.Request) (*http.Response, error) {
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, fmt.Errorf("proxy CONNECT response: %w", err)
	}
	if br.Buffered() > 0 {
		return nil, fmt.Errorf("proxy sent %d bytes after the CONNECT response; this stream cannot be reused safely", br.Buffered())
	}
	return res, nil
}

func basicAuth(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}
