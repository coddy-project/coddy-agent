package netx

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
)

// bufferedConn yields bytes that were already read off a connection before
// anything further from the socket.
type bufferedConn struct {
	net.Conn
	reader io.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// SpliceBuffered returns a connection that replays what a buffered reader has
// already pulled off conn.
//
// Any time an HTTP message is read from a connection that is about to be
// reused for something else, the reader can and does pull in whatever came
// next. Those bytes belong to the next speaker, and dropping them leaves a
// connection that looks healthy and then fails on its first frame - which
// reads like a protocol bug and is really an accounting one.
func SpliceBuffered(conn net.Conn, buffered *bufio.Reader) net.Conn {
	if buffered == nil || buffered.Buffered() == 0 {
		return conn
	}
	return &bufferedConn{
		Conn:   conn,
		reader: io.MultiReader(io.LimitReader(buffered, int64(buffered.Buffered())), conn),
	}
}

// readCONNECTResponse reads the proxy's answer and hands back a connection that
// still carries anything the reader pulled in behind it.
//
// A proxy is free to start relaying the origin the instant it writes its 200,
// so those first bytes routinely arrive in the same read as the response.
func readCONNECTResponse(conn net.Conn, req *http.Request) (*http.Response, net.Conn, error) {
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, conn, fmt.Errorf("proxy CONNECT response: %w", err)
	}
	return res, SpliceBuffered(conn, br), nil
}

func basicAuth(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}
