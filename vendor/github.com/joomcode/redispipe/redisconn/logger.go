package redisconn

import "log"

// Logger is a type for custom event and stat reporter.
type Logger interface {
	// Report will be called when some events happens during connection's lifetime.
	// Default implementation just prints this information using standard log package.
	Report(conn *Connection, event LogEvent)
	// ReqStat is called after request receives it's answer with request/result information
	// and time spend to fulfill request.
	// Default implementation is no-op.
	ReqStat(conn *Connection, req Request, res interface{}, nanos int64)
}

// LogEvent is a sum-type for events to be logged.
type LogEvent interface {
	logEvent() // tagging method
}

// LogConnecting is an event logged when Connection starts dialing to redis.
type LogConnecting struct{}

// LogConnected is logged when Connection established connection to redis.
type LogConnected struct {
	LocalAddr  string // - local ip:port
	RemoteAddr string // - remote ip:port
}

// LogConnectFailed is logged when connection establishing were unsuccessful.
type LogConnectFailed struct {
	Error error // - failure reason
}

// LogDisconnected is logged when connection were broken.
type LogDisconnected struct {
	Error      error  // - disconnection reason
	LocalAddr  string // - local ip:port
	RemoteAddr string // - remote ip:port
}

// LogContextClosed is logged when Connection's context were closed, or Connection.Close() called.
// Ie when connection is explicitly closed by user.
type LogContextClosed struct {
	Error error // - ctx.Err()
}

func (LogConnecting) logEvent()    {}
func (LogConnected) logEvent()     {}
func (LogConnectFailed) logEvent() {}
func (LogDisconnected) logEvent()  {}
func (LogContextClosed) logEvent() {}

func (conn *Connection) report(event LogEvent) {
	conn.opts.Logger.Report(conn, event)
}

// DefaultLogger is default implementation of Logger
type DefaultLogger struct{}

// Report implements Logger.Report
func (d DefaultLogger) Report(conn *Connection, event LogEvent) {
	switch ev := event.(type) {
	case LogConnecting:
		log.Printf("redis: connecting to %s", conn.Addr())
	case LogConnected:
		log.Printf("redis: connected to %s (localAddr: %s, remAddr: %s)",
			conn.Addr(), ev.LocalAddr, ev.RemoteAddr)
	case LogConnectFailed:
		log.Printf("redis: connection to %s failed: %s", conn.Addr(), ev.Error.Error())
	case LogDisconnected:
		log.Printf("redis: connection to %s broken (localAddr: %s, remAddr: %s): %s", conn.Addr(),
			ev.LocalAddr, ev.RemoteAddr, ev.Error.Error())
	case LogContextClosed:
		log.Printf("redis: connect to %s explicitly closed: %s", conn.Addr(), ev.Error.Error())
	default:
		log.Printf("redis: unexpected event: %#v", event)
	}
}

// ReqStat implements Logger.ReqStat
func (d DefaultLogger) ReqStat(conn *Connection, req Request, res interface{}, nanos int64) {
	// noop
}

// NoopLogger is noop implementation of Logger
// Useful in tests
type NoopLogger struct{}

// Report implements Logger.Report
func (d NoopLogger) Report(*Connection, LogEvent) {}

// ReqStat implements Logger.ReqStat
func (d NoopLogger) ReqStat(conn *Connection, req Request, res interface{}, nanos int64) {}
