package fasthttputil

import (
	"fmt"
	"net"
	"sync"
)

// InmemoryListener provides in-memory dialer<->net.Listener implementation.
//
// It may be used either for fast in-process client<->server communications
// without network stack overhead or for client<->server tests.
type InmemoryListener struct {
	lock   sync.Mutex
	closed bool
	conns  chan acceptConn
}

type acceptConn struct {
	conn     net.Conn
	accepted chan struct{}
}

// NewInmemoryListener returns new in-memory dialer<->net.Listener.
func NewInmemoryListener() *InmemoryListener {
	return &InmemoryListener{
		conns: make(chan acceptConn, 1024),
	}
}

// Accept implements net.Listener's Accept.
//
// It is safe calling Accept from concurrently running goroutines.
//
// Accept returns new connection per each Dial call.
func (ln *InmemoryListener) Accept() (net.Conn, error) {
	c, ok := <-ln.conns
	if !ok {
		return nil, fmt.Errorf("InmemoryListener is already closed: use of closed network connection")
	}
	close(c.accepted)
	return c.conn, nil
}

// Close implements net.Listener's Close.
func (ln *InmemoryListener) Close() error {
	var err error

	ln.lock.Lock()
	if !ln.closed {
		close(ln.conns)
		ln.closed = true
	} else {
		err = fmt.Errorf("InmemoryListener is already closed")
	}
	ln.lock.Unlock()
	return err
}

// Addr implements net.Listener's Addr.
func (ln *InmemoryListener) Addr() net.Addr {
	return &net.UnixAddr{
		Name: "InmemoryListener",
		Net:  "memory",
	}
}

// Dial creates new client<->server connection.
// Just like a real Dial it only returns once the server
// has accepted the connection.
//
// It is safe calling Dial from concurrently running goroutines.
func (ln *InmemoryListener) Dial() (net.Conn, error) {
	pc := NewPipeConns()
	cConn := pc.Conn1()
	sConn := pc.Conn2()
	ln.lock.Lock()
	accepted := make(chan struct{})
	if !ln.closed {
		ln.conns <- acceptConn{sConn, accepted}
		// Wait until the connection has been accepted.
		<-accepted
	} else {
		sConn.Close()
		cConn.Close()
		cConn = nil
	}
	ln.lock.Unlock()

	if cConn == nil {
		return nil, fmt.Errorf("InmemoryListener is already closed")
	}
	return cConn, nil
}
