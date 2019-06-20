package fasthttputil

import (
	"fmt"
	"net"
	"sync"
)

// InmemoryListener provides in-memory dialer<->net.Listener implementation.
//
// It may be used either for fast in-process client<->server communcations
// without network stack overhead or for client<->server tests.
type InmemoryListener struct {
	lock   sync.Mutex
	closed bool
	conns  chan net.Conn
}

// NewInmemoryListener returns new in-memory dialer<->net.Listener.
func NewInmemoryListener() *InmemoryListener {
	return &InmemoryListener{
		conns: make(chan net.Conn, 1024),
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
	return c, nil
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

// Dial creates new client<->server connection, enqueues server side
// of the connection to Accept and returns client side of the connection.
//
// It is safe calling Dial from concurrently running goroutines.
func (ln *InmemoryListener) Dial() (net.Conn, error) {
	pc := NewPipeConns()
	cConn := pc.Conn1()
	sConn := pc.Conn2()
	ln.lock.Lock()
	if !ln.closed {
		ln.conns <- sConn
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
