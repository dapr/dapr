package redisconn

import (
	"io"
	"net"
	"time"
)

// deadlineIO is a wrapper that sets read deadline before each Read.
type deadlineIO struct {
	to time.Duration
	c  net.Conn
}

func newDeadlineIO(c net.Conn, to time.Duration) io.ReadWriter {
	if to > 0 {
		return &deadlineIO{c: c, to: to}
	}
	return c
}

// Write implements io.Writer.
// It doesn't set write deadline.
func (d *deadlineIO) Write(b []byte) (int, error) {
	return d.c.Write(b)
}

// Read implements io.Reader
// It sets read deadline before each call to Read.
func (d *deadlineIO) Read(b []byte) (int, error) {
	d.c.SetReadDeadline(time.Now().Add(d.to))
	return d.c.Read(b)
}
