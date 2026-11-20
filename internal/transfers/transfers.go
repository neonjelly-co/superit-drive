// Package transfers holds the two rules that keep the Drive inside its memory figure while files move through it
// (counted at 64 MiB; the kernel's network buffers are charged to the container too):
//
//   - every connection's network buffers are capped at SocketBuffer, on the browser side (Listen) and on the storage
//     side (Dial). Left alone, the kernel grows each one to several MiB while a transfer is in flight;
//   - at most Slots transfers are in flight at once (Gate), of which outsiders (visitors with a public link) hold at
//     most OutsiderSlots. Further ones wait their turn.
//
// A transfer is a request that moves a file's bytes: a download (of a file or a zip) or an upload (whole, or one part
// of a resumable upload).
package transfers

import (
	"context"
	"net"
	"time"
)

// SocketBuffer is the most each connection's send and receive buffers may hold, in bytes. (Linux doubles the value
// it is given, for its own bookkeeping.)
const SocketBuffer = 256 << 10

// Slots is how many transfers may be in flight at once.
const Slots = 32

// OutsiderSlots is how many of the Slots outsiders may hold at once; members can always use the others.
const OutsiderSlots = 8

// Capped sets conn's send and receive buffers to SocketBuffer, if it is a TCP connection.
func Capped(conn net.Conn) net.Conn {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(SocketBuffer)
		_ = tc.SetWriteBuffer(SocketBuffer)
	}
	return conn
}

// Listen listens on address, and caps the buffers of every connection it accepts.
func Listen(address string) (net.Listener, error) {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return cappedListener{ln}, nil
}

type cappedListener struct{ net.Listener }

func (l cappedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return Capped(conn), nil
}

// Dial is a DialContext for an http.Transport that caps the buffers of every connection it makes. TLS runs on top
// of the connection it returns.
func Dial(timeout time.Duration) func(ctx context.Context, network, address string) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := d.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return Capped(conn), nil
	}
}
