package localipc

import "net"

// peerCheckedListener rejects a connection before net/http can read a request
// from it, so an unauthorized local peer receives no application response.
type peerCheckedListener struct{ net.Listener }

func (l peerCheckedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := sameUserPeer(conn); err != nil {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}
