package fasthttp

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var errNoCertOrKeyProvided = errors.New("Cert or key has not provided")

var (
	// ErrAlreadyServing is returned when calling Serve on a Server
	// that is already serving connections.
	ErrAlreadyServing = errors.New("Server is already serving connections")
)

// ServeConn serves HTTP requests from the given connection
// using the given handler.
//
// ServeConn returns nil if all requests from the c are successfully served.
// It returns non-nil error otherwise.
//
// Connection c must immediately propagate all the data passed to Write()
// to the client. Otherwise requests' processing may hang.
//
// ServeConn closes c before returning.
func ServeConn(c net.Conn, handler RequestHandler) error {
	v := serverPool.Get()
	if v == nil {
		v = &Server{}
	}
	s := v.(*Server)
	s.Handler = handler
	err := s.ServeConn(c)
	s.Handler = nil
	serverPool.Put(v)
	return err
}

var serverPool sync.Pool

// Serve serves incoming connections from the given listener
// using the given handler.
//
// Serve blocks until the given listener returns permanent error.
func Serve(ln net.Listener, handler RequestHandler) error {
	s := &Server{
		Handler: handler,
	}
	return s.Serve(ln)
}

// ServeTLS serves HTTPS requests from the given net.Listener
// using the given handler.
//
// certFile and keyFile are paths to TLS certificate and key files.
func ServeTLS(ln net.Listener, certFile, keyFile string, handler RequestHandler) error {
	s := &Server{
		Handler: handler,
	}
	return s.ServeTLS(ln, certFile, keyFile)
}

// ServeTLSEmbed serves HTTPS requests from the given net.Listener
// using the given handler.
//
// certData and keyData must contain valid TLS certificate and key data.
func ServeTLSEmbed(ln net.Listener, certData, keyData []byte, handler RequestHandler) error {
	s := &Server{
		Handler: handler,
	}
	return s.ServeTLSEmbed(ln, certData, keyData)
}

// ListenAndServe serves HTTP requests from the given TCP addr
// using the given handler.
func ListenAndServe(addr string, handler RequestHandler) error {
	s := &Server{
		Handler: handler,
	}
	return s.ListenAndServe(addr)
}

// ListenAndServeUNIX serves HTTP requests from the given UNIX addr
// using the given handler.
//
// The function deletes existing file at addr before starting serving.
//
// The server sets the given file mode for the UNIX addr.
func ListenAndServeUNIX(addr string, mode os.FileMode, handler RequestHandler) error {
	s := &Server{
		Handler: handler,
	}
	return s.ListenAndServeUNIX(addr, mode)
}

// ListenAndServeTLS serves HTTPS requests from the given TCP addr
// using the given handler.
//
// certFile and keyFile are paths to TLS certificate and key files.
func ListenAndServeTLS(addr, certFile, keyFile string, handler RequestHandler) error {
	s := &Server{
		Handler: handler,
	}
	return s.ListenAndServeTLS(addr, certFile, keyFile)
}

// ListenAndServeTLSEmbed serves HTTPS requests from the given TCP addr
// using the given handler.
//
// certData and keyData must contain valid TLS certificate and key data.
func ListenAndServeTLSEmbed(addr string, certData, keyData []byte, handler RequestHandler) error {
	s := &Server{
		Handler: handler,
	}
	return s.ListenAndServeTLSEmbed(addr, certData, keyData)
}

// RequestHandler must process incoming requests.
//
// RequestHandler must call ctx.TimeoutError() before returning
// if it keeps references to ctx and/or its' members after the return.
// Consider wrapping RequestHandler into TimeoutHandler if response time
// must be limited.
type RequestHandler func(ctx *RequestCtx)

// ServeHandler must process tls.Config.NextProto negotiated requests.
type ServeHandler func(c net.Conn) error

// Server implements HTTP server.
//
// Default Server settings should satisfy the majority of Server users.
// Adjust Server settings only if you really understand the consequences.
//
// It is forbidden copying Server instances. Create new Server instances
// instead.
//
// It is safe to call Server methods from concurrently running goroutines.
type Server struct {
	noCopy noCopy

	// Handler for processing incoming requests.
	//
	// Take into account that no `panic` recovery is done by `fasthttp` (thus any `panic` will take down the entire server).
	// Instead the user should use `recover` to handle these situations.
	Handler RequestHandler

	// ErrorHandler for returning a response in case of an error while receiving or parsing the request.
	//
	// The following is a non-exhaustive list of errors that can be expected as argument:
	//   * io.EOF
	//   * io.ErrUnexpectedEOF
	//   * ErrGetOnly
	//   * ErrSmallBuffer
	//   * ErrBodyTooLarge
	//   * ErrBrokenChunks
	ErrorHandler func(ctx *RequestCtx, err error)

	// Server name for sending in response headers.
	//
	// Default server name is used if left blank.
	Name string

	// The maximum number of concurrent connections the server may serve.
	//
	// DefaultConcurrency is used if not set.
	Concurrency int

	// Whether to disable keep-alive connections.
	//
	// The server will close all the incoming connections after sending
	// the first response to client if this option is set to true.
	//
	// By default keep-alive connections are enabled.
	DisableKeepalive bool

	// Per-connection buffer size for requests' reading.
	// This also limits the maximum header size.
	//
	// Increase this buffer if your clients send multi-KB RequestURIs
	// and/or multi-KB headers (for example, BIG cookies).
	//
	// Default buffer size is used if not set.
	ReadBufferSize int

	// Per-connection buffer size for responses' writing.
	//
	// Default buffer size is used if not set.
	WriteBufferSize int

	// ReadTimeout is the amount of time allowed to read
	// the full request including body. The connection's read
	// deadline is reset when the connection opens, or for
	// keep-alive connections after the first byte has been read.
	//
	// By default request read timeout is unlimited.
	ReadTimeout time.Duration

	// WriteTimeout is the maximum duration before timing out
	// writes of the response. It is reset after the request handler
	// has returned.
	//
	// By default response write timeout is unlimited.
	WriteTimeout time.Duration

	// IdleTimeout is the maximum amount of time to wait for the
	// next request when keep-alive is enabled. If IdleTimeout
	// is zero, the value of ReadTimeout is used.
	IdleTimeout time.Duration

	// Maximum number of concurrent client connections allowed per IP.
	//
	// By default unlimited number of concurrent connections
	// may be established to the server from a single IP address.
	MaxConnsPerIP int

	// Maximum number of requests served per connection.
	//
	// The server closes connection after the last request.
	// 'Connection: close' header is added to the last response.
	//
	// By default unlimited number of requests may be served per connection.
	MaxRequestsPerConn int

	// MaxKeepaliveDuration is a no-op and only left here for backwards compatibility.
	// Deprecated: Use IdleTimeout instead.
	MaxKeepaliveDuration time.Duration

	// Whether to enable tcp keep-alive connections.
	//
	// Whether the operating system should send tcp keep-alive messages on the tcp connection.
	//
	// By default tcp keep-alive connections are disabled.
	TCPKeepalive bool

	// Period between tcp keep-alive messages.
	//
	// TCP keep-alive period is determined by operation system by default.
	TCPKeepalivePeriod time.Duration

	// Maximum request body size.
	//
	// The server rejects requests with bodies exceeding this limit.
	//
	// Request body size is limited by DefaultMaxRequestBodySize by default.
	MaxRequestBodySize int

	// Aggressively reduces memory usage at the cost of higher CPU usage
	// if set to true.
	//
	// Try enabling this option only if the server consumes too much memory
	// serving mostly idle keep-alive connections. This may reduce memory
	// usage by more than 50%.
	//
	// Aggressive memory usage reduction is disabled by default.
	ReduceMemoryUsage bool

	// Rejects all non-GET requests if set to true.
	//
	// This option is useful as anti-DoS protection for servers
	// accepting only GET requests. The request size is limited
	// by ReadBufferSize if GetOnly is set.
	//
	// Server accepts all the requests by default.
	GetOnly bool

	// Logs all errors, including the most frequent
	// 'connection reset by peer', 'broken pipe' and 'connection timeout'
	// errors. Such errors are common in production serving real-world
	// clients.
	//
	// By default the most frequent errors such as
	// 'connection reset by peer', 'broken pipe' and 'connection timeout'
	// are suppressed in order to limit output log traffic.
	LogAllErrors bool

	// Header names are passed as-is without normalization
	// if this option is set.
	//
	// Disabled header names' normalization may be useful only for proxying
	// incoming requests to other servers expecting case-sensitive
	// header names. See https://github.com/valyala/fasthttp/issues/57
	// for details.
	//
	// By default request and response header names are normalized, i.e.
	// The first letter and the first letters following dashes
	// are uppercased, while all the other letters are lowercased.
	// Examples:
	//
	//     * HOST -> Host
	//     * content-type -> Content-Type
	//     * cONTENT-lenGTH -> Content-Length
	DisableHeaderNamesNormalizing bool

	// SleepWhenConcurrencyLimitsExceeded is a duration to be slept of if
	// the concurrency limit in exceeded (default [when is 0]: don't sleep
	// and accept new connections immidiatelly).
	SleepWhenConcurrencyLimitsExceeded time.Duration

	// NoDefaultServerHeader, when set to true, causes the default Server header
	// to be excluded from the Response.
	//
	// The default Server header value is the value of the Name field or an
	// internal default value in its absence. With this option set to true,
	// the only time a Server header will be sent is if a non-zero length
	// value is explicitly provided during a request.
	NoDefaultServerHeader bool

	// NoDefaultContentType, when set to true, causes the default Content-Type
	// header to be excluded from the Response.
	//
	// The default Content-Type header value is the internal default value. When
	// set to true, the Content-Type will not be present.
	NoDefaultContentType bool

	// ConnState specifies an optional callback function that is
	// called when a client connection changes state. See the
	// ConnState type and associated constants for details.
	ConnState func(net.Conn, ConnState)

	// Logger, which is used by RequestCtx.Logger().
	//
	// By default standard logger from log package is used.
	Logger Logger

	// KeepHijackedConns is an opt-in disable of connection
	// close by fasthttp after connections' HijackHandler returns.
	// This allows to save goroutines, e.g. when fasthttp used to upgrade
	// http connections to WS and connection goes to another handler,
	// which will close it when needed.
	KeepHijackedConns bool

	tlsConfig  *tls.Config
	nextProtos map[string]ServeHandler

	concurrency      uint32
	concurrencyCh    chan struct{}
	perIPConnCounter perIPConnCounter
	serverName       atomic.Value

	ctxPool        sync.Pool
	readerPool     sync.Pool
	writerPool     sync.Pool
	hijackConnPool sync.Pool

	// We need to know our listener so we can close it in Shutdown().
	ln net.Listener

	mu   sync.Mutex
	open int32
	stop int32
	done chan struct{}
}

// TimeoutHandler creates RequestHandler, which returns StatusRequestTimeout
// error with the given msg to the client if h didn't return during
// the given duration.
//
// The returned handler may return StatusTooManyRequests error with the given
// msg to the client if there are more than Server.Concurrency concurrent
// handlers h are running at the moment.
func TimeoutHandler(h RequestHandler, timeout time.Duration, msg string) RequestHandler {
	return TimeoutWithCodeHandler(h,timeout,msg, StatusRequestTimeout)
}

// TimeoutWithCodeHandler creates RequestHandler, which returns an error with 
// the given msg and status code to the client  if h didn't return during
// the given duration.
//
// The returned handler may return StatusTooManyRequests error with the given
// msg to the client if there are more than Server.Concurrency concurrent
// handlers h are running at the moment.
func TimeoutWithCodeHandler(h RequestHandler, timeout time.Duration, msg string, statusCode int) RequestHandler {
	if timeout <= 0 {
		return h
	}

	return func(ctx *RequestCtx) {
		concurrencyCh := ctx.s.concurrencyCh
		select {
		case concurrencyCh <- struct{}{}:
		default:
			ctx.Error(msg, StatusTooManyRequests)
			return
		}

		ch := ctx.timeoutCh
		if ch == nil {
			ch = make(chan struct{}, 1)
			ctx.timeoutCh = ch
		}
		go func() {
			h(ctx)
			ch <- struct{}{}
			<-concurrencyCh
		}()
		ctx.timeoutTimer = initTimer(ctx.timeoutTimer, timeout)
		select {
		case <-ch:
		case <-ctx.timeoutTimer.C:
			ctx.TimeoutErrorWithCode(msg, statusCode)
		}
		stopTimer(ctx.timeoutTimer)
	}
}

// CompressHandler returns RequestHandler that transparently compresses
// response body generated by h if the request contains 'gzip' or 'deflate'
// 'Accept-Encoding' header.
func CompressHandler(h RequestHandler) RequestHandler {
	return CompressHandlerLevel(h, CompressDefaultCompression)
}

// CompressHandlerLevel returns RequestHandler that transparently compresses
// response body generated by h if the request contains 'gzip' or 'deflate'
// 'Accept-Encoding' header.
//
// Level is the desired compression level:
//
//     * CompressNoCompression
//     * CompressBestSpeed
//     * CompressBestCompression
//     * CompressDefaultCompression
//     * CompressHuffmanOnly
func CompressHandlerLevel(h RequestHandler, level int) RequestHandler {
	return func(ctx *RequestCtx) {
		h(ctx)
		if ctx.Request.Header.HasAcceptEncodingBytes(strGzip) {
			ctx.Response.gzipBody(level)
		} else if ctx.Request.Header.HasAcceptEncodingBytes(strDeflate) {
			ctx.Response.deflateBody(level)
		}
	}
}

// RequestCtx contains incoming request and manages outgoing response.
//
// It is forbidden copying RequestCtx instances.
//
// RequestHandler should avoid holding references to incoming RequestCtx and/or
// its' members after the return.
// If holding RequestCtx references after the return is unavoidable
// (for instance, ctx is passed to a separate goroutine and ctx lifetime cannot
// be controlled), then the RequestHandler MUST call ctx.TimeoutError()
// before return.
//
// It is unsafe modifying/reading RequestCtx instance from concurrently
// running goroutines. The only exception is TimeoutError*, which may be called
// while other goroutines accessing RequestCtx.
type RequestCtx struct {
	noCopy noCopy

	// Incoming request.
	//
	// Copying Request by value is forbidden. Use pointer to Request instead.
	Request Request

	// Outgoing response.
	//
	// Copying Response by value is forbidden. Use pointer to Response instead.
	Response Response

	userValues userData

	connID         uint64
	connRequestNum uint64
	connTime       time.Time

	time time.Time

	logger ctxLogger
	s      *Server
	c      net.Conn
	fbr    firstByteReader

	timeoutResponse *Response
	timeoutCh       chan struct{}
	timeoutTimer    *time.Timer

	hijackHandler HijackHandler
}

// HijackHandler must process the hijacked connection c.
//
// If KeepHijackedConns is disabled, which is by default,
// the connection c is automatically closed after returning from HijackHandler.
//
// The connection c must not be used after returning from the handler, if KeepHijackedConns is disabled.
//
// When KeepHijackedConns enabled, fasthttp will not Close() the connection,
// you must do it when you need it. You must not use c in any way after calling Close().
type HijackHandler func(c net.Conn)

// Hijack registers the given handler for connection hijacking.
//
// The handler is called after returning from RequestHandler
// and sending http response. The current connection is passed
// to the handler. The connection is automatically closed after
// returning from the handler.
//
// The server skips calling the handler in the following cases:
//
//     * 'Connection: close' header exists in either request or response.
//     * Unexpected error during response writing to the connection.
//
// The server stops processing requests from hijacked connections.
// Server limits such as Concurrency, ReadTimeout, WriteTimeout, etc.
// aren't applied to hijacked connections.
//
// The handler must not retain references to ctx members.
//
// Arbitrary 'Connection: Upgrade' protocols may be implemented
// with HijackHandler. For instance,
//
//     * WebSocket ( https://en.wikipedia.org/wiki/WebSocket )
//     * HTTP/2.0 ( https://en.wikipedia.org/wiki/HTTP/2 )
//
func (ctx *RequestCtx) Hijack(handler HijackHandler) {
	ctx.hijackHandler = handler
}

// Hijacked returns true after Hijack is called.
func (ctx *RequestCtx) Hijacked() bool {
	return ctx.hijackHandler != nil
}

// SetUserValue stores the given value (arbitrary object)
// under the given key in ctx.
//
// The value stored in ctx may be obtained by UserValue*.
//
// This functionality may be useful for passing arbitrary values between
// functions involved in request processing.
//
// All the values are removed from ctx after returning from the top
// RequestHandler. Additionally, Close method is called on each value
// implementing io.Closer before removing the value from ctx.
func (ctx *RequestCtx) SetUserValue(key string, value interface{}) {
	ctx.userValues.Set(key, value)
}

// SetUserValueBytes stores the given value (arbitrary object)
// under the given key in ctx.
//
// The value stored in ctx may be obtained by UserValue*.
//
// This functionality may be useful for passing arbitrary values between
// functions involved in request processing.
//
// All the values stored in ctx are deleted after returning from RequestHandler.
func (ctx *RequestCtx) SetUserValueBytes(key []byte, value interface{}) {
	ctx.userValues.SetBytes(key, value)
}

// UserValue returns the value stored via SetUserValue* under the given key.
func (ctx *RequestCtx) UserValue(key string) interface{} {
	return ctx.userValues.Get(key)
}

// UserValueBytes returns the value stored via SetUserValue*
// under the given key.
func (ctx *RequestCtx) UserValueBytes(key []byte) interface{} {
	return ctx.userValues.GetBytes(key)
}

// VisitUserValues calls visitor for each existing userValue.
//
// visitor must not retain references to key and value after returning.
// Make key and/or value copies if you need storing them after returning.
func (ctx *RequestCtx) VisitUserValues(visitor func([]byte, interface{})) {
	for i, n := 0, len(ctx.userValues); i < n; i++ {
		kv := &ctx.userValues[i]
		visitor(kv.key, kv.value)
	}
}

type connTLSer interface {
	Handshake() error
	ConnectionState() tls.ConnectionState
}

// IsTLS returns true if the underlying connection is tls.Conn.
//
// tls.Conn is an encrypted connection (aka SSL, HTTPS).
func (ctx *RequestCtx) IsTLS() bool {
	// cast to (connTLSer) instead of (*tls.Conn), since it catches
	// cases with overridden tls.Conn such as:
	//
	// type customConn struct {
	//     *tls.Conn
	//
	//     // other custom fields here
	// }
	_, ok := ctx.c.(connTLSer)
	return ok
}

// TLSConnectionState returns TLS connection state.
//
// The function returns nil if the underlying connection isn't tls.Conn.
//
// The returned state may be used for verifying TLS version, client certificates,
// etc.
func (ctx *RequestCtx) TLSConnectionState() *tls.ConnectionState {
	tlsConn, ok := ctx.c.(connTLSer)
	if !ok {
		return nil
	}
	state := tlsConn.ConnectionState()
	return &state
}

// Conn returns a reference to the underlying net.Conn.
//
// WARNING: Only use this method if you know what you are doing!
//
// Reading from or writing to the returned connection will end badly!
func (ctx *RequestCtx) Conn() net.Conn {
	return ctx.c
}

type firstByteReader struct {
	c        net.Conn
	ch       byte
	byteRead bool
}

func (r *firstByteReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	nn := 0
	if !r.byteRead {
		b[0] = r.ch
		b = b[1:]
		r.byteRead = true
		nn = 1
	}
	n, err := r.c.Read(b)
	return n + nn, err
}

// Logger is used for logging formatted messages.
type Logger interface {
	// Printf must have the same semantics as log.Printf.
	Printf(format string, args ...interface{})
}

var ctxLoggerLock sync.Mutex

type ctxLogger struct {
	ctx    *RequestCtx
	logger Logger
}

func (cl *ctxLogger) Printf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	ctxLoggerLock.Lock()
	cl.logger.Printf("%.3f %s - %s", time.Since(cl.ctx.ConnTime()).Seconds(), cl.ctx.String(), msg)
	ctxLoggerLock.Unlock()
}

var zeroTCPAddr = &net.TCPAddr{
	IP: net.IPv4zero,
}

// String returns unique string representation of the ctx.
//
// The returned value may be useful for logging.
func (ctx *RequestCtx) String() string {
	return fmt.Sprintf("#%016X - %s<->%s - %s %s", ctx.ID(), ctx.LocalAddr(), ctx.RemoteAddr(), ctx.Request.Header.Method(), ctx.URI().FullURI())
}

// ID returns unique ID of the request.
func (ctx *RequestCtx) ID() uint64 {
	return (ctx.connID << 32) | ctx.connRequestNum
}

// ConnID returns unique connection ID.
//
// This ID may be used to match distinct requests to the same incoming
// connection.
func (ctx *RequestCtx) ConnID() uint64 {
	return ctx.connID
}

// Time returns RequestHandler call time.
func (ctx *RequestCtx) Time() time.Time {
	return ctx.time
}

// ConnTime returns the time the server started serving the connection
// the current request came from.
func (ctx *RequestCtx) ConnTime() time.Time {
	return ctx.connTime
}

// ConnRequestNum returns request sequence number
// for the current connection.
//
// Sequence starts with 1.
func (ctx *RequestCtx) ConnRequestNum() uint64 {
	return ctx.connRequestNum
}

// SetConnectionClose sets 'Connection: close' response header and closes
// connection after the RequestHandler returns.
func (ctx *RequestCtx) SetConnectionClose() {
	ctx.Response.SetConnectionClose()
}

// SetStatusCode sets response status code.
func (ctx *RequestCtx) SetStatusCode(statusCode int) {
	ctx.Response.SetStatusCode(statusCode)
}

// SetContentType sets response Content-Type.
func (ctx *RequestCtx) SetContentType(contentType string) {
	ctx.Response.Header.SetContentType(contentType)
}

// SetContentTypeBytes sets response Content-Type.
//
// It is safe modifying contentType buffer after function return.
func (ctx *RequestCtx) SetContentTypeBytes(contentType []byte) {
	ctx.Response.Header.SetContentTypeBytes(contentType)
}

// RequestURI returns RequestURI.
//
// This uri is valid until returning from RequestHandler.
func (ctx *RequestCtx) RequestURI() []byte {
	return ctx.Request.Header.RequestURI()
}

// URI returns requested uri.
//
// The uri is valid until returning from RequestHandler.
func (ctx *RequestCtx) URI() *URI {
	return ctx.Request.URI()
}

// Referer returns request referer.
//
// The referer is valid until returning from RequestHandler.
func (ctx *RequestCtx) Referer() []byte {
	return ctx.Request.Header.Referer()
}

// UserAgent returns User-Agent header value from the request.
func (ctx *RequestCtx) UserAgent() []byte {
	return ctx.Request.Header.UserAgent()
}

// Path returns requested path.
//
// The path is valid until returning from RequestHandler.
func (ctx *RequestCtx) Path() []byte {
	return ctx.URI().Path()
}

// Host returns requested host.
//
// The host is valid until returning from RequestHandler.
func (ctx *RequestCtx) Host() []byte {
	return ctx.URI().Host()
}

// QueryArgs returns query arguments from RequestURI.
//
// It doesn't return POST'ed arguments - use PostArgs() for this.
//
// Returned arguments are valid until returning from RequestHandler.
//
// See also PostArgs, FormValue and FormFile.
func (ctx *RequestCtx) QueryArgs() *Args {
	return ctx.URI().QueryArgs()
}

// PostArgs returns POST arguments.
//
// It doesn't return query arguments from RequestURI - use QueryArgs for this.
//
// Returned arguments are valid until returning from RequestHandler.
//
// See also QueryArgs, FormValue and FormFile.
func (ctx *RequestCtx) PostArgs() *Args {
	return ctx.Request.PostArgs()
}

// MultipartForm returns requests's multipart form.
//
// Returns ErrNoMultipartForm if request's content-type
// isn't 'multipart/form-data'.
//
// All uploaded temporary files are automatically deleted after
// returning from RequestHandler. Either move or copy uploaded files
// into new place if you want retaining them.
//
// Use SaveMultipartFile function for permanently saving uploaded file.
//
// The returned form is valid until returning from RequestHandler.
//
// See also FormFile and FormValue.
func (ctx *RequestCtx) MultipartForm() (*multipart.Form, error) {
	return ctx.Request.MultipartForm()
}

// FormFile returns uploaded file associated with the given multipart form key.
//
// The file is automatically deleted after returning from RequestHandler,
// so either move or copy uploaded file into new place if you want retaining it.
//
// Use SaveMultipartFile function for permanently saving uploaded file.
//
// The returned file header is valid until returning from RequestHandler.
func (ctx *RequestCtx) FormFile(key string) (*multipart.FileHeader, error) {
	mf, err := ctx.MultipartForm()
	if err != nil {
		return nil, err
	}
	if mf.File == nil {
		return nil, err
	}
	fhh := mf.File[key]
	if fhh == nil {
		return nil, ErrMissingFile
	}
	return fhh[0], nil
}

// ErrMissingFile may be returned from FormFile when the is no uploaded file
// associated with the given multipart form key.
var ErrMissingFile = errors.New("there is no uploaded file associated with the given key")

// SaveMultipartFile saves multipart file fh under the given filename path.
func SaveMultipartFile(fh *multipart.FileHeader, path string) error {
	f, err := fh.Open()
	if err != nil {
		return err
	}

	if ff, ok := f.(*os.File); ok {
		// Windows can't rename files that are opened.
		if err := f.Close(); err != nil {
			return err
		}

		// If renaming fails we try the normal copying method.
		// Renaming could fail if the files are on different devices.
		if os.Rename(ff.Name(), path) == nil {
			return nil
		}

		// Reopen f for the code below.
		f, err = fh.Open()
		if err != nil {
			return err
		}
	}

	defer f.Close()

	ff, err := os.Create(path)
	if err != nil {
		return err
	}
	defer ff.Close()
	_, err = copyZeroAlloc(ff, f)
	return err
}

// FormValue returns form value associated with the given key.
//
// The value is searched in the following places:
//
//   * Query string.
//   * POST or PUT body.
//
// There are more fine-grained methods for obtaining form values:
//
//   * QueryArgs for obtaining values from query string.
//   * PostArgs for obtaining values from POST or PUT body.
//   * MultipartForm for obtaining values from multipart form.
//   * FormFile for obtaining uploaded files.
//
// The returned value is valid until returning from RequestHandler.
func (ctx *RequestCtx) FormValue(key string) []byte {
	v := ctx.QueryArgs().Peek(key)
	if len(v) > 0 {
		return v
	}
	v = ctx.PostArgs().Peek(key)
	if len(v) > 0 {
		return v
	}
	mf, err := ctx.MultipartForm()
	if err == nil && mf.Value != nil {
		vv := mf.Value[key]
		if len(vv) > 0 {
			return []byte(vv[0])
		}
	}
	return nil
}

// IsGet returns true if request method is GET.
func (ctx *RequestCtx) IsGet() bool {
	return ctx.Request.Header.IsGet()
}

// IsPost returns true if request method is POST.
func (ctx *RequestCtx) IsPost() bool {
	return ctx.Request.Header.IsPost()
}

// IsPut returns true if request method is PUT.
func (ctx *RequestCtx) IsPut() bool {
	return ctx.Request.Header.IsPut()
}

// IsDelete returns true if request method is DELETE.
func (ctx *RequestCtx) IsDelete() bool {
	return ctx.Request.Header.IsDelete()
}

// IsConnect returns true if request method is CONNECT.
func (ctx *RequestCtx) IsConnect() bool {
	return ctx.Request.Header.IsConnect()
}

// IsOptions returns true if request method is OPTIONS.
func (ctx *RequestCtx) IsOptions() bool {
	return ctx.Request.Header.IsOptions()
}

// IsTrace returns true if request method is TRACE.
func (ctx *RequestCtx) IsTrace() bool {
	return ctx.Request.Header.IsTrace()
}

// IsPatch returns true if request method is PATCH.
func (ctx *RequestCtx) IsPatch() bool {
	return ctx.Request.Header.IsPatch()
}

// Method return request method.
//
// Returned value is valid until returning from RequestHandler.
func (ctx *RequestCtx) Method() []byte {
	return ctx.Request.Header.Method()
}

// IsHead returns true if request method is HEAD.
func (ctx *RequestCtx) IsHead() bool {
	return ctx.Request.Header.IsHead()
}

// RemoteAddr returns client address for the given request.
//
// Always returns non-nil result.
func (ctx *RequestCtx) RemoteAddr() net.Addr {
	if ctx.c == nil {
		return zeroTCPAddr
	}
	addr := ctx.c.RemoteAddr()
	if addr == nil {
		return zeroTCPAddr
	}
	return addr
}

// LocalAddr returns server address for the given request.
//
// Always returns non-nil result.
func (ctx *RequestCtx) LocalAddr() net.Addr {
	if ctx.c == nil {
		return zeroTCPAddr
	}
	addr := ctx.c.LocalAddr()
	if addr == nil {
		return zeroTCPAddr
	}
	return addr
}

// RemoteIP returns the client ip the request came from.
//
// Always returns non-nil result.
func (ctx *RequestCtx) RemoteIP() net.IP {
	return addrToIP(ctx.RemoteAddr())
}

// LocalIP returns the server ip the request came to.
//
// Always returns non-nil result.
func (ctx *RequestCtx) LocalIP() net.IP {
	return addrToIP(ctx.LocalAddr())
}

func addrToIP(addr net.Addr) net.IP {
	x, ok := addr.(*net.TCPAddr)
	if !ok {
		return net.IPv4zero
	}
	return x.IP
}

// Error sets response status code to the given value and sets response body
// to the given message.
//
// Warning: this will reset the response headers and body already set!
func (ctx *RequestCtx) Error(msg string, statusCode int) {
	ctx.Response.Reset()
	ctx.SetStatusCode(statusCode)
	ctx.SetContentTypeBytes(defaultContentType)
	ctx.SetBodyString(msg)
}

// Success sets response Content-Type and body to the given values.
func (ctx *RequestCtx) Success(contentType string, body []byte) {
	ctx.SetContentType(contentType)
	ctx.SetBody(body)
}

// SuccessString sets response Content-Type and body to the given values.
func (ctx *RequestCtx) SuccessString(contentType, body string) {
	ctx.SetContentType(contentType)
	ctx.SetBodyString(body)
}

// Redirect sets 'Location: uri' response header and sets the given statusCode.
//
// statusCode must have one of the following values:
//
//    * StatusMovedPermanently (301)
//    * StatusFound (302)
//    * StatusSeeOther (303)
//    * StatusTemporaryRedirect (307)
//    * StatusPermanentRedirect (308)
//
// All other statusCode values are replaced by StatusFound (302).
//
// The redirect uri may be either absolute or relative to the current
// request uri. Fasthttp will always send an absolute uri back to the client.
// To send a relative uri you can use the following code:
//
//   strLocation = []byte("Location") // Put this with your top level var () declarations.
//   ctx.Response.Header.SetCanonical(strLocation, "/relative?uri")
//   ctx.Response.SetStatusCode(fasthttp.StatusMovedPermanently)
//
func (ctx *RequestCtx) Redirect(uri string, statusCode int) {
	u := AcquireURI()
	ctx.URI().CopyTo(u)
	u.Update(uri)
	ctx.redirect(u.FullURI(), statusCode)
	ReleaseURI(u)
}

// RedirectBytes sets 'Location: uri' response header and sets
// the given statusCode.
//
// statusCode must have one of the following values:
//
//    * StatusMovedPermanently (301)
//    * StatusFound (302)
//    * StatusSeeOther (303)
//    * StatusTemporaryRedirect (307)
//    * StatusPermanentRedirect (308)
//
// All other statusCode values are replaced by StatusFound (302).
//
// The redirect uri may be either absolute or relative to the current
// request uri. Fasthttp will always send an absolute uri back to the client.
// To send a relative uri you can use the following code:
//
//   strLocation = []byte("Location") // Put this with your top level var () declarations.
//   ctx.Response.Header.SetCanonical(strLocation, "/relative?uri")
//   ctx.Response.SetStatusCode(fasthttp.StatusMovedPermanently)
//
func (ctx *RequestCtx) RedirectBytes(uri []byte, statusCode int) {
	s := b2s(uri)
	ctx.Redirect(s, statusCode)
}

func (ctx *RequestCtx) redirect(uri []byte, statusCode int) {
	ctx.Response.Header.SetCanonical(strLocation, uri)
	statusCode = getRedirectStatusCode(statusCode)
	ctx.Response.SetStatusCode(statusCode)
}

func getRedirectStatusCode(statusCode int) int {
	if statusCode == StatusMovedPermanently || statusCode == StatusFound ||
		statusCode == StatusSeeOther || statusCode == StatusTemporaryRedirect ||
		statusCode == StatusPermanentRedirect {
		return statusCode
	}
	return StatusFound
}

// SetBody sets response body to the given value.
//
// It is safe re-using body argument after the function returns.
func (ctx *RequestCtx) SetBody(body []byte) {
	ctx.Response.SetBody(body)
}

// SetBodyString sets response body to the given value.
func (ctx *RequestCtx) SetBodyString(body string) {
	ctx.Response.SetBodyString(body)
}

// ResetBody resets response body contents.
func (ctx *RequestCtx) ResetBody() {
	ctx.Response.ResetBody()
}

// SendFile sends local file contents from the given path as response body.
//
// This is a shortcut to ServeFile(ctx, path).
//
// SendFile logs all the errors via ctx.Logger.
//
// See also ServeFile, FSHandler and FS.
func (ctx *RequestCtx) SendFile(path string) {
	ServeFile(ctx, path)
}

// SendFileBytes sends local file contents from the given path as response body.
//
// This is a shortcut to ServeFileBytes(ctx, path).
//
// SendFileBytes logs all the errors via ctx.Logger.
//
// See also ServeFileBytes, FSHandler and FS.
func (ctx *RequestCtx) SendFileBytes(path []byte) {
	ServeFileBytes(ctx, path)
}

// IfModifiedSince returns true if lastModified exceeds 'If-Modified-Since'
// value from the request header.
//
// The function returns true also 'If-Modified-Since' request header is missing.
func (ctx *RequestCtx) IfModifiedSince(lastModified time.Time) bool {
	ifModStr := ctx.Request.Header.peek(strIfModifiedSince)
	if len(ifModStr) == 0 {
		return true
	}
	ifMod, err := ParseHTTPDate(ifModStr)
	if err != nil {
		return true
	}
	lastModified = lastModified.Truncate(time.Second)
	return ifMod.Before(lastModified)
}

// NotModified resets response and sets '304 Not Modified' response status code.
func (ctx *RequestCtx) NotModified() {
	ctx.Response.Reset()
	ctx.SetStatusCode(StatusNotModified)
}

// NotFound resets response and sets '404 Not Found' response status code.
func (ctx *RequestCtx) NotFound() {
	ctx.Response.Reset()
	ctx.SetStatusCode(StatusNotFound)
	ctx.SetBodyString("404 Page not found")
}

// Write writes p into response body.
func (ctx *RequestCtx) Write(p []byte) (int, error) {
	ctx.Response.AppendBody(p)
	return len(p), nil
}

// WriteString appends s to response body.
func (ctx *RequestCtx) WriteString(s string) (int, error) {
	ctx.Response.AppendBodyString(s)
	return len(s), nil
}

// PostBody returns POST request body.
//
// The returned value is valid until RequestHandler return.
func (ctx *RequestCtx) PostBody() []byte {
	return ctx.Request.Body()
}

// SetBodyStream sets response body stream and, optionally body size.
//
// bodyStream.Close() is called after finishing reading all body data
// if it implements io.Closer.
//
// If bodySize is >= 0, then bodySize bytes must be provided by bodyStream
// before returning io.EOF.
//
// If bodySize < 0, then bodyStream is read until io.EOF.
//
// See also SetBodyStreamWriter.
func (ctx *RequestCtx) SetBodyStream(bodyStream io.Reader, bodySize int) {
	ctx.Response.SetBodyStream(bodyStream, bodySize)
}

// SetBodyStreamWriter registers the given stream writer for populating
// response body.
//
// Access to RequestCtx and/or its' members is forbidden from sw.
//
// This function may be used in the following cases:
//
//     * if response body is too big (more than 10MB).
//     * if response body is streamed from slow external sources.
//     * if response body must be streamed to the client in chunks.
//     (aka `http server push`).
func (ctx *RequestCtx) SetBodyStreamWriter(sw StreamWriter) {
	ctx.Response.SetBodyStreamWriter(sw)
}

// IsBodyStream returns true if response body is set via SetBodyStream*.
func (ctx *RequestCtx) IsBodyStream() bool {
	return ctx.Response.IsBodyStream()
}

// Logger returns logger, which may be used for logging arbitrary
// request-specific messages inside RequestHandler.
//
// Each message logged via returned logger contains request-specific information
// such as request id, request duration, local address, remote address,
// request method and request url.
//
// It is safe re-using returned logger for logging multiple messages
// for the current request.
//
// The returned logger is valid until returning from RequestHandler.
func (ctx *RequestCtx) Logger() Logger {
	if ctx.logger.ctx == nil {
		ctx.logger.ctx = ctx
	}
	if ctx.logger.logger == nil {
		ctx.logger.logger = ctx.s.logger()
	}
	return &ctx.logger
}

// TimeoutError sets response status code to StatusRequestTimeout and sets
// body to the given msg.
//
// All response modifications after TimeoutError call are ignored.
//
// TimeoutError MUST be called before returning from RequestHandler if there are
// references to ctx and/or its members in other goroutines remain.
//
// Usage of this function is discouraged. Prefer eliminating ctx references
// from pending goroutines instead of using this function.
func (ctx *RequestCtx) TimeoutError(msg string) {
	ctx.TimeoutErrorWithCode(msg, StatusRequestTimeout)
}

// TimeoutErrorWithCode sets response body to msg and response status
// code to statusCode.
//
// All response modifications after TimeoutErrorWithCode call are ignored.
//
// TimeoutErrorWithCode MUST be called before returning from RequestHandler
// if there are references to ctx and/or its members in other goroutines remain.
//
// Usage of this function is discouraged. Prefer eliminating ctx references
// from pending goroutines instead of using this function.
func (ctx *RequestCtx) TimeoutErrorWithCode(msg string, statusCode int) {
	var resp Response
	resp.SetStatusCode(statusCode)
	resp.SetBodyString(msg)
	ctx.TimeoutErrorWithResponse(&resp)
}

// TimeoutErrorWithResponse marks the ctx as timed out and sends the given
// response to the client.
//
// All ctx modifications after TimeoutErrorWithResponse call are ignored.
//
// TimeoutErrorWithResponse MUST be called before returning from RequestHandler
// if there are references to ctx and/or its members in other goroutines remain.
//
// Usage of this function is discouraged. Prefer eliminating ctx references
// from pending goroutines instead of using this function.
func (ctx *RequestCtx) TimeoutErrorWithResponse(resp *Response) {
	respCopy := &Response{}
	resp.CopyTo(respCopy)
	ctx.timeoutResponse = respCopy
}

// NextProto adds nph to be processed when key is negotiated when TLS
// connection is established.
//
// This function can only be called before the server is started.
func (s *Server) NextProto(key string, nph ServeHandler) {
	if s.nextProtos == nil {
		s.nextProtos = make(map[string]ServeHandler)
	}
	s.configTLS()
	s.tlsConfig.NextProtos = append(s.tlsConfig.NextProtos, key)
	s.nextProtos[key] = nph
}

func (s *Server) getNextProto(c net.Conn) (proto string, err error) {
	if tlsConn, ok := c.(connTLSer); ok {
		err = tlsConn.Handshake()
		if err == nil {
			proto = tlsConn.ConnectionState().NegotiatedProtocol
		}
	}
	return
}

// tcpKeepAliveListener sets TCP keep-alive timeouts on accepted
// connections. It's used by ListenAndServe, ListenAndServeTLS and
// ListenAndServeTLSEmbed so dead TCP connections (e.g. closing laptop mid-download)
// eventually go away.
type tcpKeepaliveListener struct {
	*net.TCPListener
	keepalivePeriod time.Duration
}

func (ln tcpKeepaliveListener) Accept() (net.Conn, error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return nil, err
	}
	tc.SetKeepAlive(true)
	if ln.keepalivePeriod > 0 {
		tc.SetKeepAlivePeriod(ln.keepalivePeriod)
	}
	return tc, nil
}

// ListenAndServe serves HTTP requests from the given TCP4 addr.
//
// Pass custom listener to Serve if you need listening on non-TCP4 media
// such as IPv6.
//
// Accepted connections are configured to enable TCP keep-alives.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return err
	}
	if s.TCPKeepalive {
		if tcpln, ok := ln.(*net.TCPListener); ok {
			return s.Serve(tcpKeepaliveListener{
				TCPListener:     tcpln,
				keepalivePeriod: s.TCPKeepalivePeriod,
			})
		}
	}
	return s.Serve(ln)
}

// ListenAndServeUNIX serves HTTP requests from the given UNIX addr.
//
// The function deletes existing file at addr before starting serving.
//
// The server sets the given file mode for the UNIX addr.
func (s *Server) ListenAndServeUNIX(addr string, mode os.FileMode) error {
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unexpected error when trying to remove unix socket file %q: %s", addr, err)
	}
	ln, err := net.Listen("unix", addr)
	if err != nil {
		return err
	}
	if err = os.Chmod(addr, mode); err != nil {
		return fmt.Errorf("cannot chmod %#o for %q: %s", mode, addr, err)
	}
	return s.Serve(ln)
}

// ListenAndServeTLS serves HTTPS requests from the given TCP4 addr.
//
// certFile and keyFile are paths to TLS certificate and key files.
//
// Pass custom listener to Serve if you need listening on non-TCP4 media
// such as IPv6.
//
// If the certFile or keyFile has not been provided to the server structure,
// the function will use the previously added TLS configuration.
//
// Accepted connections are configured to enable TCP keep-alives.
func (s *Server) ListenAndServeTLS(addr, certFile, keyFile string) error {
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return err
	}
	if s.TCPKeepalive {
		if tcpln, ok := ln.(*net.TCPListener); ok {
			return s.ServeTLS(tcpKeepaliveListener{
				TCPListener:     tcpln,
				keepalivePeriod: s.TCPKeepalivePeriod,
			}, certFile, keyFile)
		}
	}
	return s.ServeTLS(ln, certFile, keyFile)
}

// ListenAndServeTLSEmbed serves HTTPS requests from the given TCP4 addr.
//
// certData and keyData must contain valid TLS certificate and key data.
//
// Pass custom listener to Serve if you need listening on arbitrary media
// such as IPv6.
//
// If the certFile or keyFile has not been provided the server structure,
// the function will use previously added TLS configuration.
//
// Accepted connections are configured to enable TCP keep-alives.
func (s *Server) ListenAndServeTLSEmbed(addr string, certData, keyData []byte) error {
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return err
	}
	if s.TCPKeepalive {
		if tcpln, ok := ln.(*net.TCPListener); ok {
			return s.ServeTLSEmbed(tcpKeepaliveListener{
				TCPListener:     tcpln,
				keepalivePeriod: s.TCPKeepalivePeriod,
			}, certData, keyData)
		}
	}
	return s.ServeTLSEmbed(ln, certData, keyData)
}

// ServeTLS serves HTTPS requests from the given listener.
//
// certFile and keyFile are paths to TLS certificate and key files.
//
// If the certFile or keyFile has not been provided the server structure,
// the function will use previously added TLS configuration.
func (s *Server) ServeTLS(ln net.Listener, certFile, keyFile string) error {
	err := s.AppendCert(certFile, keyFile)
	if err != nil && err != errNoCertOrKeyProvided {
		return err
	}
	if s.tlsConfig == nil {
		return errNoCertOrKeyProvided
	}
	s.tlsConfig.BuildNameToCertificate()

	return s.Serve(
		tls.NewListener(ln, s.tlsConfig),
	)
}

// ServeTLSEmbed serves HTTPS requests from the given listener.
//
// certData and keyData must contain valid TLS certificate and key data.
//
// If the certFile or keyFile has not been provided the server structure,
// the function will use previously added TLS configuration.
func (s *Server) ServeTLSEmbed(ln net.Listener, certData, keyData []byte) error {
	err := s.AppendCertEmbed(certData, keyData)
	if err != nil && err != errNoCertOrKeyProvided {
		return err
	}
	if s.tlsConfig == nil {
		return errNoCertOrKeyProvided
	}
	s.tlsConfig.BuildNameToCertificate()

	return s.Serve(
		tls.NewListener(ln, s.tlsConfig),
	)
}

// AppendCert appends certificate and keyfile to TLS Configuration.
//
// This function allows programmer to handle multiple domains
// in one server structure. See examples/multidomain
func (s *Server) AppendCert(certFile, keyFile string) error {
	if len(certFile) == 0 && len(keyFile) == 0 {
		return errNoCertOrKeyProvided
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("cannot load TLS key pair from certFile=%q and keyFile=%q: %s", certFile, keyFile, err)
	}

	s.configTLS()

	s.tlsConfig.Certificates = append(s.tlsConfig.Certificates, cert)
	return nil
}

// AppendCertEmbed does the same as AppendCert but using in-memory data.
func (s *Server) AppendCertEmbed(certData, keyData []byte) error {
	if len(certData) == 0 && len(keyData) == 0 {
		return errNoCertOrKeyProvided
	}

	cert, err := tls.X509KeyPair(certData, keyData)
	if err != nil {
		return fmt.Errorf("cannot load TLS key pair from the provided certData(%d) and keyData(%d): %s",
			len(certData), len(keyData), err)
	}

	s.configTLS()

	s.tlsConfig.Certificates = append(s.tlsConfig.Certificates, cert)
	return nil
}

func (s *Server) configTLS() {
	if s.tlsConfig == nil {
		s.tlsConfig = &tls.Config{
			PreferServerCipherSuites: true,
		}
	}
}

// DefaultConcurrency is the maximum number of concurrent connections
// the Server may serve by default (i.e. if Server.Concurrency isn't set).
const DefaultConcurrency = 256 * 1024

// Serve serves incoming connections from the given listener.
//
// Serve blocks until the given listener returns permanent error.
func (s *Server) Serve(ln net.Listener) error {
	var lastOverflowErrorTime time.Time
	var lastPerIPErrorTime time.Time
	var c net.Conn
	var err error

	s.mu.Lock()
	{
		if s.ln != nil {
			s.mu.Unlock()
			return ErrAlreadyServing
		}

		s.ln = ln
		s.done = make(chan struct{})
	}
	s.mu.Unlock()

	maxWorkersCount := s.getConcurrency()
	s.concurrencyCh = make(chan struct{}, maxWorkersCount)
	wp := &workerPool{
		WorkerFunc:      s.serveConn,
		MaxWorkersCount: maxWorkersCount,
		LogAllErrors:    s.LogAllErrors,
		Logger:          s.logger(),
		connState:       s.setState,
	}
	wp.Start()

	// Count our waiting to accept a connection as an open connection.
	// This way we can't get into any weird state where just after accepting
	// a connection Shutdown is called which reads open as 0 because it isn't
	// incremented yet.
	atomic.AddInt32(&s.open, 1)
	defer atomic.AddInt32(&s.open, -1)

	for {
		if c, err = acceptConn(s, ln, &lastPerIPErrorTime); err != nil {
			wp.Stop()
			if err == io.EOF {
				return nil
			}
			return err
		}
		s.setState(c, StateNew)
		atomic.AddInt32(&s.open, 1)
		if !wp.Serve(c) {
			atomic.AddInt32(&s.open, -1)
			s.writeFastError(c, StatusServiceUnavailable,
				"The connection cannot be served because Server.Concurrency limit exceeded")
			c.Close()
			s.setState(c, StateClosed)
			if time.Since(lastOverflowErrorTime) > time.Minute {
				s.logger().Printf("The incoming connection cannot be served, because %d concurrent connections are served. "+
					"Try increasing Server.Concurrency", maxWorkersCount)
				lastOverflowErrorTime = time.Now()
			}

			// The current server reached concurrency limit,
			// so give other concurrently running servers a chance
			// accepting incoming connections on the same address.
			//
			// There is a hope other servers didn't reach their
			// concurrency limits yet :)
			//
			// See also: https://github.com/valyala/fasthttp/pull/485#discussion_r239994990
			if s.SleepWhenConcurrencyLimitsExceeded > 0 {
				time.Sleep(s.SleepWhenConcurrencyLimitsExceeded)
			}
		}
		c = nil
	}
}

// Shutdown gracefully shuts down the server without interrupting any active connections.
// Shutdown works by first closing all open listeners and then waiting indefinitely for all connections to return to idle and then shut down.
//
// When Shutdown is called, Serve, ListenAndServe, and ListenAndServeTLS immediately return nil.
// Make sure the program doesn't exit and waits instead for Shutdown to return.
//
// Shutdown does not close keepalive connections so its recommended to set ReadTimeout to something else than 0.
func (s *Server) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	atomic.StoreInt32(&s.stop, 1)
	defer atomic.StoreInt32(&s.stop, 0)

	if s.ln == nil {
		return nil
	}

	if err := s.ln.Close(); err != nil {
		return err
	}

	if s.done != nil {
		close(s.done)
	}

	// Closing the listener will make Serve() call Stop on the worker pool.
	// Setting .stop to 1 will make serveConn() break out of its loop.
	// Now we just have to wait until all workers are done.
	for {
		if open := atomic.LoadInt32(&s.open); open == 0 {
			break
		}
		// This is not an optimal solution but using a sync.WaitGroup
		// here causes data races as it's hard to prevent Add() to be called
		// while Wait() is waiting.
		time.Sleep(time.Millisecond * 100)
	}

	s.ln = nil
	return nil
}

func acceptConn(s *Server, ln net.Listener, lastPerIPErrorTime *time.Time) (net.Conn, error) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if c != nil {
				panic("BUG: net.Listener returned non-nil conn and non-nil error")
			}
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
				s.logger().Printf("Temporary error when accepting new connections: %s", netErr)
				time.Sleep(time.Second)
				continue
			}
			if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
				s.logger().Printf("Permanent error when accepting new connections: %s", err)
				return nil, err
			}
			return nil, io.EOF
		}
		if c == nil {
			panic("BUG: net.Listener returned (nil, nil)")
		}
		if s.MaxConnsPerIP > 0 {
			pic := wrapPerIPConn(s, c)
			if pic == nil {
				if time.Since(*lastPerIPErrorTime) > time.Minute {
					s.logger().Printf("The number of connections from %s exceeds MaxConnsPerIP=%d",
						getConnIP4(c), s.MaxConnsPerIP)
					*lastPerIPErrorTime = time.Now()
				}
				continue
			}
			c = pic
		}
		return c, nil
	}
}

func wrapPerIPConn(s *Server, c net.Conn) net.Conn {
	ip := getUint32IP(c)
	if ip == 0 {
		return c
	}
	n := s.perIPConnCounter.Register(ip)
	if n > s.MaxConnsPerIP {
		s.perIPConnCounter.Unregister(ip)
		s.writeFastError(c, StatusTooManyRequests, "The number of connections from your ip exceeds MaxConnsPerIP")
		c.Close()
		return nil
	}
	return acquirePerIPConn(c, ip, &s.perIPConnCounter)
}

var defaultLogger = Logger(log.New(os.Stderr, "", log.LstdFlags))

func (s *Server) logger() Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return defaultLogger
}

var (
	// ErrPerIPConnLimit may be returned from ServeConn if the number of connections
	// per ip exceeds Server.MaxConnsPerIP.
	ErrPerIPConnLimit = errors.New("too many connections per ip")

	// ErrConcurrencyLimit may be returned from ServeConn if the number
	// of concurrently served connections exceeds Server.Concurrency.
	ErrConcurrencyLimit = errors.New("cannot serve the connection because Server.Concurrency concurrent connections are served")
)

// ServeConn serves HTTP requests from the given connection.
//
// ServeConn returns nil if all requests from the c are successfully served.
// It returns non-nil error otherwise.
//
// Connection c must immediately propagate all the data passed to Write()
// to the client. Otherwise requests' processing may hang.
//
// ServeConn closes c before returning.
func (s *Server) ServeConn(c net.Conn) error {
	if s.MaxConnsPerIP > 0 {
		pic := wrapPerIPConn(s, c)
		if pic == nil {
			return ErrPerIPConnLimit
		}
		c = pic
	}

	n := atomic.AddUint32(&s.concurrency, 1)
	if n > uint32(s.getConcurrency()) {
		atomic.AddUint32(&s.concurrency, ^uint32(0))
		s.writeFastError(c, StatusServiceUnavailable, "The connection cannot be served because Server.Concurrency limit exceeded")
		c.Close()
		return ErrConcurrencyLimit
	}

	atomic.AddInt32(&s.open, 1)

	err := s.serveConn(c)

	atomic.AddUint32(&s.concurrency, ^uint32(0))

	if err != errHijacked {
		err1 := c.Close()
		s.setState(c, StateClosed)
		if err == nil {
			err = err1
		}
	} else {
		err = nil
		s.setState(c, StateHijacked)
	}
	return err
}

var errHijacked = errors.New("connection has been hijacked")

// GetCurrentConcurrency returns a number of currently served
// connections.
//
// This function is intended be used by monitoring systems
func (s *Server) GetCurrentConcurrency() uint32 {
	return atomic.LoadUint32(&s.concurrency)
}

// GetOpenConnectionsCount returns a number of opened connections.
//
// This function is intended be used by monitoring systems
func (s *Server) GetOpenConnectionsCount() int32 {
	return atomic.LoadInt32(&s.open) - 1
}

func (s *Server) getConcurrency() int {
	n := s.Concurrency
	if n <= 0 {
		n = DefaultConcurrency
	}
	return n
}

var globalConnID uint64

func nextConnID() uint64 {
	return atomic.AddUint64(&globalConnID, 1)
}

// DefaultMaxRequestBodySize is the maximum request body size the server
// reads by default.
//
// See Server.MaxRequestBodySize for details.
const DefaultMaxRequestBodySize = 4 * 1024 * 1024

func (s *Server) idleTimeout() time.Duration {
	if s.IdleTimeout != 0 {
		return s.IdleTimeout
	}
	return s.ReadTimeout
}

func (s *Server) serveConn(c net.Conn) error {
	defer atomic.AddInt32(&s.open, -1)

	if proto, err := s.getNextProto(c); err != nil {
		return err
	} else {
		handler, ok := s.nextProtos[proto]
		if ok {
			return handler(c)
		}
	}

	var serverName []byte
	if !s.NoDefaultServerHeader {
		serverName = s.getServerName()
	}
	connRequestNum := uint64(0)
	connID := nextConnID()
	connTime := time.Now()
	maxRequestBodySize := s.MaxRequestBodySize
	if maxRequestBodySize <= 0 {
		maxRequestBodySize = DefaultMaxRequestBodySize
	}

	ctx := s.acquireCtx(c)
	ctx.connTime = connTime
	isTLS := ctx.IsTLS()
	var (
		br *bufio.Reader
		bw *bufio.Writer

		err             error
		timeoutResponse *Response
		hijackHandler   HijackHandler

		connectionClose bool
		isHTTP11        bool

		reqReset bool
	)
	for {
		connRequestNum++

		// If this is a keep-alive connection set the idle timeout.
		if connRequestNum > 1 {
			if d := s.idleTimeout(); d > 0 {
				if err := c.SetReadDeadline(time.Now().Add(d)); err != nil {
					panic(fmt.Sprintf("BUG: error in SetReadDeadline(%s): %s", d, err))
				}
			}
		}

		if !s.ReduceMemoryUsage || br != nil {
			if br == nil {
				br = acquireReader(ctx)
			}

			// If this is a keep-alive connection we want to try and read the first bytes
			// within the idle time.
			if connRequestNum > 1 {
				var b []byte
				b, err = br.Peek(4)
				if len(b) == 0 {
					// If reading from a keep-alive connection returns nothing it means
					// the connection was closed (either timeout or from the other side).
					err = errNothingRead
				}
			}
		} else {
			// If this is a keep-alive connection acquireByteReader will try to peek
			// a couple of bytes already so the idle timeout will already be used.
			br, err = acquireByteReader(&ctx)
		}

		ctx.Request.isTLS = isTLS
		ctx.Response.Header.noDefaultContentType = s.NoDefaultContentType

		if err == nil {
			if s.ReadTimeout > 0 {
				if err := c.SetReadDeadline(time.Now().Add(s.ReadTimeout)); err != nil {
					panic(fmt.Sprintf("BUG: error in SetReadDeadline(%s): %s", s.ReadTimeout, err))
				}
			}

			if s.DisableHeaderNamesNormalizing {
				ctx.Request.Header.DisableNormalizing()
				ctx.Response.Header.DisableNormalizing()
			}
			// reading Headers and Body
			err = ctx.Request.readLimitBody(br, maxRequestBodySize, s.GetOnly)
			if err == nil {
				// If we read any bytes off the wire, we're active.
				s.setState(c, StateActive)
			}
			if (s.ReduceMemoryUsage && br.Buffered() == 0) || err != nil {
				releaseReader(s, br)
				br = nil
			}
		}

		if err != nil {
			if err == io.EOF {
				err = nil
			} else if connRequestNum > 1 && err == errNothingRead {
				// This is not the first request and we haven't read a single byte
				// of a new request yet. This means it's just a keep-alive connection
				// closing down either because the remote closed it or because
				// or a read timeout on our side. Either way just close the connection
				// and don't return any error response.
				err = nil
			} else {
				bw = s.writeErrorResponse(bw, ctx, serverName, err)
			}
			break
		}

		// 'Expect: 100-continue' request handling.
		// See http://www.w3.org/Protocols/rfc2616/rfc2616-sec8.html for details.
		if !ctx.Request.Header.ignoreBody() && ctx.Request.MayContinue() {
			// Send 'HTTP/1.1 100 Continue' response.
			if bw == nil {
				bw = acquireWriter(ctx)
			}
			bw.Write(strResponseContinue)
			err = bw.Flush()
			if err != nil {
				break
			}
			if s.ReduceMemoryUsage {
				releaseWriter(s, bw)
				bw = nil
			}

			// Read request body.
			if br == nil {
				br = acquireReader(ctx)
			}
			err = ctx.Request.ContinueReadBody(br, maxRequestBodySize)
			if (s.ReduceMemoryUsage && br.Buffered() == 0) || err != nil {
				releaseReader(s, br)
				br = nil
			}
			if err != nil {
				bw = s.writeErrorResponse(bw, ctx, serverName, err)
				break
			}
		}

		connectionClose = s.DisableKeepalive || ctx.Request.Header.ConnectionClose()
		isHTTP11 = ctx.Request.Header.IsHTTP11()

		if serverName != nil {
			ctx.Response.Header.SetServerBytes(serverName)
		}
		ctx.connID = connID
		ctx.connRequestNum = connRequestNum
		ctx.time = time.Now()
		s.Handler(ctx)

		timeoutResponse = ctx.timeoutResponse
		if timeoutResponse != nil {
			ctx = s.acquireCtx(c)
			timeoutResponse.CopyTo(&ctx.Response)
			if br != nil {
				// Close connection, since br may be attached to the old ctx via ctx.fbr.
				ctx.SetConnectionClose()
			}
		}

		if !ctx.IsGet() && ctx.IsHead() {
			ctx.Response.SkipBody = true
		}
		reqReset = true
		ctx.Request.Reset()

		hijackHandler = ctx.hijackHandler
		ctx.hijackHandler = nil

		ctx.userValues.Reset()

		if s.MaxRequestsPerConn > 0 && connRequestNum >= uint64(s.MaxRequestsPerConn) {
			ctx.SetConnectionClose()
		}

		if s.WriteTimeout > 0 {
			if err := c.SetWriteDeadline(time.Now().Add(s.WriteTimeout)); err != nil {
				panic(fmt.Sprintf("BUG: error in SetWriteDeadline(%s): %s", s.WriteTimeout, err))
			}
		}

		connectionClose = connectionClose || ctx.Response.ConnectionClose()
		if connectionClose {
			ctx.Response.Header.SetCanonical(strConnection, strClose)
		} else if !isHTTP11 {
			// Set 'Connection: keep-alive' response header for non-HTTP/1.1 request.
			// There is no need in setting this header for http/1.1, since in http/1.1
			// connections are keep-alive by default.
			ctx.Response.Header.SetCanonical(strConnection, strKeepAlive)
		}

		if serverName != nil && len(ctx.Response.Header.Server()) == 0 {
			ctx.Response.Header.SetServerBytes(serverName)
		}

		if bw == nil {
			bw = acquireWriter(ctx)
		}
		if err = writeResponse(ctx, bw); err != nil {
			break
		}

		// Only flush the writer if we don't have another request in the pipeline.
		// This is a big of an ugly optimization for https://www.techempower.com/benchmarks/
		// This benchmark will send 16 pipelined requests. It is faster to pack as many responses
		// in a TCP packet and send it back at once than waiting for a flush every request.
		// In real world circumstances this behaviour could be argued as being wrong.
		if br == nil || br.Buffered() == 0 || connectionClose {
			err = bw.Flush()
			if err != nil {
				break
			}
		}
		if connectionClose {
			break
		}
		if s.ReduceMemoryUsage {
			releaseWriter(s, bw)
			bw = nil
		}

		if hijackHandler != nil {
			var hjr io.Reader = c
			if br != nil {
				hjr = br
				br = nil

				// br may point to ctx.fbr, so do not return ctx into pool below.
				ctx = nil
			}
			if bw != nil {
				err = bw.Flush()
				if err != nil {
					break
				}
				releaseWriter(s, bw)
				bw = nil
			}
			c.SetReadDeadline(zeroTime)
			c.SetWriteDeadline(zeroTime)
			go hijackConnHandler(hjr, c, s, hijackHandler)
			hijackHandler = nil
			err = errHijacked
			break
		}

		s.setState(c, StateIdle)

		if atomic.LoadInt32(&s.stop) == 1 {
			err = nil
			break
		}
	}

	if br != nil {
		releaseReader(s, br)
	}
	if bw != nil {
		releaseWriter(s, bw)
	}
	if ctx != nil {
		// in unexpected cases the for loop will break
		// before request reset call. in such cases, call it before
		// release to fix #548
		if !reqReset {
			ctx.Request.Reset()
		}
		s.releaseCtx(ctx)
	}
	return err
}

func (s *Server) setState(nc net.Conn, state ConnState) {
	if hook := s.ConnState; hook != nil {
		hook(nc, state)
	}
}

func hijackConnHandler(r io.Reader, c net.Conn, s *Server, h HijackHandler) {
	hjc := s.acquireHijackConn(r, c)
	h(hjc)

	if br, ok := r.(*bufio.Reader); ok {
		releaseReader(s, br)
	}
	if !s.KeepHijackedConns {
		c.Close()
		s.releaseHijackConn(hjc)
	}
}

func (s *Server) acquireHijackConn(r io.Reader, c net.Conn) *hijackConn {
	v := s.hijackConnPool.Get()
	if v == nil {
		hjc := &hijackConn{
			Conn: c,
			r:    r,
			s:    s,
		}
		return hjc
	}
	hjc := v.(*hijackConn)
	hjc.Conn = c
	hjc.r = r
	return hjc
}

func (s *Server) releaseHijackConn(hjc *hijackConn) {
	hjc.Conn = nil
	hjc.r = nil
	s.hijackConnPool.Put(hjc)
}

type hijackConn struct {
	net.Conn
	r io.Reader
	s *Server
}

func (c *hijackConn) UnsafeConn() net.Conn {
	return c.Conn
}

func (c *hijackConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func (c *hijackConn) Close() error {
	if !c.s.KeepHijackedConns {
		// when we do not keep hijacked connections,
		// it is closed in hijackConnHandler.
		return nil
	}

	conn := c.Conn
	c.s.releaseHijackConn(c)
	return conn.Close()
}

// LastTimeoutErrorResponse returns the last timeout response set
// via TimeoutError* call.
//
// This function is intended for custom server implementations.
func (ctx *RequestCtx) LastTimeoutErrorResponse() *Response {
	return ctx.timeoutResponse
}

func writeResponse(ctx *RequestCtx, w *bufio.Writer) error {
	if ctx.timeoutResponse != nil {
		panic("BUG: cannot write timed out response")
	}
	err := ctx.Response.Write(w)
	ctx.Response.Reset()
	return err
}

const (
	defaultReadBufferSize  = 4096
	defaultWriteBufferSize = 4096
)

func acquireByteReader(ctxP **RequestCtx) (*bufio.Reader, error) {
	ctx := *ctxP
	s := ctx.s
	c := ctx.c
	s.releaseCtx(ctx)

	// Make GC happy, so it could garbage collect ctx
	// while we waiting for the next request.
	ctx = nil
	*ctxP = nil

	var b [1]byte
	n, err := c.Read(b[:])

	ctx = s.acquireCtx(c)
	*ctxP = ctx
	if err != nil {
		// Treat all errors as EOF on unsuccessful read
		// of the first request byte.
		return nil, io.EOF
	}
	if n != 1 {
		panic("BUG: Reader must return at least one byte")
	}

	ctx.fbr.c = c
	ctx.fbr.ch = b[0]
	ctx.fbr.byteRead = false
	r := acquireReader(ctx)
	r.Reset(&ctx.fbr)
	return r, nil
}

func acquireReader(ctx *RequestCtx) *bufio.Reader {
	v := ctx.s.readerPool.Get()
	if v == nil {
		n := ctx.s.ReadBufferSize
		if n <= 0 {
			n = defaultReadBufferSize
		}
		return bufio.NewReaderSize(ctx.c, n)
	}
	r := v.(*bufio.Reader)
	r.Reset(ctx.c)
	return r
}

func releaseReader(s *Server, r *bufio.Reader) {
	s.readerPool.Put(r)
}

func acquireWriter(ctx *RequestCtx) *bufio.Writer {
	v := ctx.s.writerPool.Get()
	if v == nil {
		n := ctx.s.WriteBufferSize
		if n <= 0 {
			n = defaultWriteBufferSize
		}
		return bufio.NewWriterSize(ctx.c, n)
	}
	w := v.(*bufio.Writer)
	w.Reset(ctx.c)
	return w
}

func releaseWriter(s *Server, w *bufio.Writer) {
	s.writerPool.Put(w)
}

func (s *Server) acquireCtx(c net.Conn) (ctx *RequestCtx) {
	v := s.ctxPool.Get()
	if v == nil {
		ctx = &RequestCtx{
			s: s,
		}
		keepBodyBuffer := !s.ReduceMemoryUsage
		ctx.Request.keepBodyBuffer = keepBodyBuffer
		ctx.Response.keepBodyBuffer = keepBodyBuffer
	} else {
		ctx = v.(*RequestCtx)
	}
	ctx.c = c
	return
}

// Init2 prepares ctx for passing to RequestHandler.
//
// conn is used only for determining local and remote addresses.
//
// This function is intended for custom Server implementations.
// See https://github.com/valyala/httpteleport for details.
func (ctx *RequestCtx) Init2(conn net.Conn, logger Logger, reduceMemoryUsage bool) {
	ctx.c = conn
	ctx.logger.logger = logger
	ctx.connID = nextConnID()
	ctx.s = fakeServer
	ctx.connRequestNum = 0
	ctx.connTime = time.Now()

	keepBodyBuffer := !reduceMemoryUsage
	ctx.Request.keepBodyBuffer = keepBodyBuffer
	ctx.Response.keepBodyBuffer = keepBodyBuffer
}

// Init prepares ctx for passing to RequestHandler.
//
// remoteAddr and logger are optional. They are used by RequestCtx.Logger().
//
// This function is intended for custom Server implementations.
func (ctx *RequestCtx) Init(req *Request, remoteAddr net.Addr, logger Logger) {
	if remoteAddr == nil {
		remoteAddr = zeroTCPAddr
	}
	c := &fakeAddrer{
		laddr: zeroTCPAddr,
		raddr: remoteAddr,
	}
	if logger == nil {
		logger = defaultLogger
	}
	ctx.Init2(c, logger, true)
	req.CopyTo(&ctx.Request)
}

// Deadline returns the time when work done on behalf of this context
// should be canceled. Deadline returns ok==false when no deadline is
// set. Successive calls to Deadline return the same results.
//
// This method always returns 0, false and is only present to make
// RequestCtx implement the context interface.
func (ctx *RequestCtx) Deadline() (deadline time.Time, ok bool) {
	return
}

// Done returns a channel that's closed when work done on behalf of this
// context should be canceled. Done may return nil if this context can
// never be canceled. Successive calls to Done return the same value.
func (ctx *RequestCtx) Done() <-chan struct{} {
	return ctx.s.done
}

// Err returns a non-nil error value after Done is closed,
// successive calls to Err return the same error.
// If Done is not yet closed, Err returns nil.
// If Done is closed, Err returns a non-nil error explaining why:
// Canceled if the context was canceled (via server Shutdown)
// or DeadlineExceeded if the context's deadline passed.
func (ctx *RequestCtx) Err() error {
	select {
	case <-ctx.s.done:
		return context.Canceled
	default:
		return nil
	}
}

// Value returns the value associated with this context for key, or nil
// if no value is associated with key. Successive calls to Value with
// the same key returns the same result.
//
// This method is present to make RequestCtx implement the context interface.
// This method is the same as calling ctx.UserValue(key)
func (ctx *RequestCtx) Value(key interface{}) interface{} {
	if keyString, ok := key.(string); ok {
		return ctx.UserValue(keyString)
	}
	return nil
}

var fakeServer = &Server{
	// Initialize concurrencyCh for TimeoutHandler
	concurrencyCh: make(chan struct{}, DefaultConcurrency),
}

type fakeAddrer struct {
	net.Conn
	laddr net.Addr
	raddr net.Addr
}

func (fa *fakeAddrer) RemoteAddr() net.Addr {
	return fa.raddr
}

func (fa *fakeAddrer) LocalAddr() net.Addr {
	return fa.laddr
}

func (fa *fakeAddrer) Read(p []byte) (int, error) {
	panic("BUG: unexpected Read call")
}

func (fa *fakeAddrer) Write(p []byte) (int, error) {
	panic("BUG: unexpected Write call")
}

func (fa *fakeAddrer) Close() error {
	panic("BUG: unexpected Close call")
}

func (s *Server) releaseCtx(ctx *RequestCtx) {
	if ctx.timeoutResponse != nil {
		panic("BUG: cannot release timed out RequestCtx")
	}
	ctx.c = nil
	ctx.fbr.c = nil
	s.ctxPool.Put(ctx)
}

func (s *Server) getServerName() []byte {
	v := s.serverName.Load()
	var serverName []byte
	if v == nil {
		serverName = []byte(s.Name)
		if len(serverName) == 0 {
			serverName = defaultServerName
		}
		s.serverName.Store(serverName)
	} else {
		serverName = v.([]byte)
	}
	return serverName
}

func (s *Server) writeFastError(w io.Writer, statusCode int, msg string) {
	w.Write(statusLine(statusCode))

	server := ""
	if !s.NoDefaultServerHeader {
		server = fmt.Sprintf("Server: %s\r\n", s.getServerName())
	}

	fmt.Fprintf(w, "Connection: close\r\n"+
		server+
		"Date: %s\r\n"+
		"Content-Type: text/plain\r\n"+
		"Content-Length: %d\r\n"+
		"\r\n"+
		"%s",
		serverDate.Load(), len(msg), msg)
}

func defaultErrorHandler(ctx *RequestCtx, err error) {
	if _, ok := err.(*ErrSmallBuffer); ok {
		ctx.Error("Too big request header", StatusRequestHeaderFieldsTooLarge)
	} else {
		ctx.Error("Error when parsing request", StatusBadRequest)
	}
}

func (s *Server) writeErrorResponse(bw *bufio.Writer, ctx *RequestCtx, serverName []byte, err error) *bufio.Writer {
	errorHandler := defaultErrorHandler
	if s.ErrorHandler != nil {
		errorHandler = s.ErrorHandler
	}

	errorHandler(ctx, err)

	if serverName != nil {
		ctx.Response.Header.SetServerBytes(serverName)
	}
	ctx.SetConnectionClose()
	if bw == nil {
		bw = acquireWriter(ctx)
	}
	writeResponse(ctx, bw)
	bw.Flush()
	return bw
}

// A ConnState represents the state of a client connection to a server.
// It's used by the optional Server.ConnState hook.
type ConnState int

const (
	// StateNew represents a new connection that is expected to
	// send a request immediately. Connections begin at this
	// state and then transition to either StateActive or
	// StateClosed.
	StateNew ConnState = iota

	// StateActive represents a connection that has read 1 or more
	// bytes of a request. The Server.ConnState hook for
	// StateActive fires before the request has entered a handler
	// and doesn't fire again until the request has been
	// handled. After the request is handled, the state
	// transitions to StateClosed, StateHijacked, or StateIdle.
	// For HTTP/2, StateActive fires on the transition from zero
	// to one active request, and only transitions away once all
	// active requests are complete. That means that ConnState
	// cannot be used to do per-request work; ConnState only notes
	// the overall state of the connection.
	StateActive

	// StateIdle represents a connection that has finished
	// handling a request and is in the keep-alive state, waiting
	// for a new request. Connections transition from StateIdle
	// to either StateActive or StateClosed.
	StateIdle

	// StateHijacked represents a hijacked connection.
	// This is a terminal state. It does not transition to StateClosed.
	StateHijacked

	// StateClosed represents a closed connection.
	// This is a terminal state. Hijacked connections do not
	// transition to StateClosed.
	StateClosed
)

var stateName = map[ConnState]string{
	StateNew:      "new",
	StateActive:   "active",
	StateIdle:     "idle",
	StateHijacked: "hijacked",
	StateClosed:   "closed",
}

func (c ConnState) String() string {
	return stateName[c]
}
