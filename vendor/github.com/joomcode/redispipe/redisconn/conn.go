package redisconn

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joomcode/errorx"

	"github.com/joomcode/redispipe/redis"
)

const (
	// DoAsking is a flag for Connection.SendBatchFlag signalling to send ASKING request before transactions.
	DoAsking = 1
	// DoTransaction is a flag for Connection.SendBatchFlag signalling to wrap bunch of requests into MULTI/EXEC.
	DoTransaction = 2

	connDisconnected = 0
	connConnecting   = 1
	connConnected    = 2
	connClosed       = 3

	defaultIOTimeout  = 1 * time.Second
	defaultWritePause = 50 * time.Microsecond
)

// Opts - options for Connection
type Opts struct {
	// DB - database number
	DB int
	// Password for AUTH
	Password string
	// IOTimeout - timeout on read/write to socket.
	// If IOTimeout == 0, then it is set to 1 second
	// If IOTimeout < 0, then timeout is disabled
	IOTimeout time.Duration
	// DialTimeout is timeout for net.Dialer
	// If it is <= 0 or >= IOTimeout, then IOTimeout
	// If IOTimeout is disabled, then 5 seconds used (but without affect on ReconnectPause)
	DialTimeout time.Duration
	// ReconnectPause is a pause after failed connection attempt before next one.
	// If ReconnectPause < 0, then no reconnection will be performed.
	// If ReconnectPause == 0, then DialTimeout * 2 is used
	ReconnectPause time.Duration
	// TCPKeepAlive - KeepAlive parameter for net.Dialer
	// default is IOTimeout / 3
	TCPKeepAlive time.Duration
	// Handle is returned with Connection.Handle()
	Handle interface{}
	// WritePause - write loop pauses for this time to collect more requests.
	// Default is 50 microseconds. Recommended value is 150 microseconds.
	// Set < 0 to disable for single threaded use case.
	WritePause time.Duration
	// Logger
	Logger Logger
	// AsyncDial - do not establish connection immediately
	AsyncDial bool
	// ScriptMode - enables blocking commands and turns default WritePause to -1.
	// It will allow to use this connector in script like (ie single threaded) environment
	// where it is ok to use blocking commands and pipelining gives no gain.
	ScriptMode bool
}

// Connection is implementation of redis.Sender which represents single connection to single redis instance.
//
// Underlying net.Conn is re-established as necessary.
// Queries are not retried in case of connection errors.
// Connection is safe for multi-threaded usage, ie it doesn't need in synchronisation.
type Connection struct {
	ctx    context.Context
	cancel context.CancelFunc
	state  uint32

	addr  string
	c     net.Conn
	mutex sync.Mutex

	futures   []future
	futsignal chan struct{}
	futtimer  *time.Timer
	futmtx    sync.Mutex

	firstConn chan struct{}
	opts      Opts
}

type oneconn struct {
	c       net.Conn
	futures chan []future
	control chan struct{}
	err     error
	erronce sync.Once
	futpool chan []future
}

// Connect establishes new connection to redis server.
// Connect will be automatically closed if context will be cancelled or timeouted. But it could be closed explicitely
// as well.
func Connect(ctx context.Context, addr string, opts Opts) (conn *Connection, err error) {
	if ctx == nil {
		return nil, redis.ErrContextIsNil.New("context is not specified")
	}
	if addr == "" {
		return nil, redis.ErrNoAddressProvided.New("address is not specified")
	}
	conn = &Connection{
		addr: addr,
		opts: opts,
	}
	conn.ctx, conn.cancel = context.WithCancel(ctx)

	conn.futsignal = make(chan struct{}, 1)
	conn.futtimer = time.NewTimer(24 * time.Hour)
	conn.futtimer.Stop()

	if conn.opts.IOTimeout == 0 {
		conn.opts.IOTimeout = defaultIOTimeout
	} else if conn.opts.IOTimeout < 0 {
		conn.opts.IOTimeout = 0
	}

	if conn.opts.DialTimeout <= 0 || conn.opts.DialTimeout > conn.opts.IOTimeout {
		conn.opts.DialTimeout = conn.opts.IOTimeout
	}

	if conn.opts.ReconnectPause == 0 {
		conn.opts.ReconnectPause = conn.opts.DialTimeout * 2
	}

	if conn.opts.TCPKeepAlive == 0 {
		conn.opts.TCPKeepAlive = conn.opts.IOTimeout / 3
	}
	if conn.opts.TCPKeepAlive < 0 {
		conn.opts.TCPKeepAlive = 0
	}

	if conn.opts.WritePause == 0 {
		if conn.opts.ScriptMode {
			conn.opts.WritePause = -1
		} else {
			conn.opts.WritePause = defaultWritePause
		}
	}

	if conn.opts.Logger == nil {
		conn.opts.Logger = DefaultLogger{}
	}

	if !conn.opts.AsyncDial {
		if err = conn.createConnection(false, nil); err != nil {
			if opts.ReconnectPause < 0 {
				return nil, err
			}
			if cer, ok := err.(*errorx.Error); ok && cer.HasTrait(ErrTraitInitPermanent) {
				return nil, err
			}
		}
	}

	if conn.opts.AsyncDial || err != nil {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			conn.mutex.Lock()
			defer conn.mutex.Unlock()
			conn.createConnection(true, &wg)
		}()
		// in async mode we are still waiting for state to set to connConnecting
		// so that Send will put requests into queue
		if conn.opts.AsyncDial {
			wg.Wait()
		}
	}

	go conn.control()

	return conn, nil
}

// Ctx returns context of this connection
func (conn *Connection) Ctx() context.Context {
	return conn.ctx
}

// ConnectedNow answers if connection is certainly connected at the moment
func (conn *Connection) ConnectedNow() bool {
	return atomic.LoadUint32(&conn.state) == connConnected
}

// MayBeConnected answers if connection either connected or connecting at the moment.
// Ie it returns false if connection is disconnected at the moment, and reconnection is not started yet.
func (conn *Connection) MayBeConnected() bool {
	s := atomic.LoadUint32(&conn.state)
	return s == connConnected || s == connConnecting
}

// Close closes connection forever
func (conn *Connection) Close() {
	conn.cancel()
}

// RemoteAddr is address of Redis socket
// Attention: do not call this method from Logger.Report, because it could lead to deadlock!
func (conn *Connection) RemoteAddr() string {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	if conn.c == nil {
		return ""
	}
	return conn.c.RemoteAddr().String()
}

// LocalAddr is outgoing socket addr
// Attention: do not call this method from Logger.Report, because it could lead to deadlock!
func (conn *Connection) LocalAddr() string {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	if conn.c == nil {
		return ""
	}
	return conn.c.LocalAddr().String()
}

// Addr retuns configurred address
func (conn *Connection) Addr() string {
	return conn.addr
}

// Handle returns user specified handle from Opts
func (conn *Connection) Handle() interface{} {
	return conn.opts.Handle
}

// Ping sends ping request synchronously
func (conn *Connection) Ping() error {
	res := redis.Sync{conn}.Do("PING")
	if err := redis.AsError(res); err != nil {
		return err
	}
	if str, ok := res.(string); !ok || str != "PONG" {
		return conn.err(redis.ErrPing).WithProperty(redis.EKResponse, res)
	}
	return nil
}

// dumb redis.Future implementation
type dumbcb struct{}

func (d dumbcb) Cancelled() error            { return nil }
func (d dumbcb) Resolve(interface{}, uint64) {}

var dumb dumbcb

// Send implements redis.Sender.Send
// It sends request asynchronously. At some moment in a future it will call cb.Resolve(result, n)
// But if cb is cancelled, then cb.Resolve will be called immediately.
func (conn *Connection) Send(req Request, cb Future, n uint64) {
	conn.SendAsk(req, cb, n, false)
}

// SendAsk is a helper method for redis-cluster client implementation.
// If asking==true, it will send request with ASKING request sent before.
func (conn *Connection) SendAsk(req Request, cb Future, n uint64, asking bool) {
	if cb == nil {
		cb = &dumb
	}
	if err := conn.doSend(req, cb, n, asking); err != nil {
		cb.Resolve(err, n)
	}
}

func (conn *Connection) doSend(req Request, cb Future, n uint64, asking bool) *errorx.Error {
	if err := cb.Cancelled(); err != nil {
		return conn.err(redis.ErrRequestCancelled)
	}

	// Since we do not pack request here, we need to be sure it could be packed
	if err := redis.CheckRequest(req, conn.opts.ScriptMode); err != nil {
		return conn.addProps(err.(*errorx.Error))
	}

	conn.futmtx.Lock()
	defer conn.futmtx.Unlock()

	// we need to check conn.state first
	// since we do not lock connection itself, we need to use atomics.
	// Note: we do not check for connConnecting, ie we will try to send request after connection established.
	switch atomic.LoadUint32(&conn.state) {
	case connClosed:
		return conn.errWrap(redis.ErrContextClosed, conn.ctx.Err())
	case connDisconnected:
		return conn.err(ErrNotConnected)
	}
	futures := conn.futures
	if asking {
		// send ASKING request before actual
		futures = append(futures, future{&dumb, 0, 0, Request{"ASKING", nil}})
	}
	futures = append(futures, future{cb, n, nownano(), req})

	// should notify writer about this shard having queries.
	// Since we are under shard lock, it is safe to send notification before assigning futures.
	if len(conn.futures) == 0 {
		if conn.opts.WritePause > 0 {
			conn.futtimer.Reset(conn.opts.WritePause)
		} else {
			select {
			case conn.futsignal <- struct{}{}:
			default:
			}
		}
	}
	conn.futures = futures
	return nil
}

// SendMany implements redis.Sender.SendMany
// Sends several requests asynchronously. Fills with cb.Resolve(res, n), cb.Resolve(res, n+1), ... etc.
// Note: it could resolve requests in arbitrary order.
func (conn *Connection) SendMany(requests []Request, cb Future, start uint64) {
	// split requests by chunks of 16 to not block futures for a long time.
	// Also it could help a bit to save pipeline with writer loop.
	for i := 0; i < len(requests); i += 16 {
		j := i + 16
		if j > len(requests) {
			j = len(requests)
		}
		conn.SendBatch(requests[i:j], cb, start+uint64(i))
	}
}

// SendBatch sends several requests in preserved order.
// They will be serialized to network in the order passed.
func (conn *Connection) SendBatch(requests []Request, cb Future, start uint64) {
	conn.SendBatchFlags(requests, cb, start, 0)
}

// SendBatchFlags sends several requests in preserved order with addition ASKING, MULTI+EXEC commands.
// If flag&DoAsking != 0 , then "ASKING" command is prepended.
// If flag&DoTransaction != 0, then "MULTI" command is prepended, and "EXEC" command appended.
// Note: cb.Resolve will be also called with start+len(requests) index with result of EXEC command.
// It is mostly helper method for SendTransaction for single connect and cluster implementations.
//
// Note: since it is used for transaction, single wrong argument in single request
// will result in error for all commands in a batch.
func (conn *Connection) SendBatchFlags(requests []Request, cb Future, start uint64, flags int) {
	var err *errorx.Error
	var commonerr *errorx.Error
	errpos := -1
	// check arguments of all commands. If single request is malformed, then all requests will be aborted.
	for i, req := range requests {
		if rerr := redis.CheckRequest(req, conn.opts.ScriptMode); rerr != nil {
			err = conn.addProps(rerr.(*errorx.Error))
			commonerr = conn.errWrap(redis.ErrBatchFormat, err)
			errpos = i
			break
		}
	}
	if cb == nil {
		cb = &dumb
	}
	if commonerr == nil {
		commonerr = conn.doSendBatch(requests, cb, start, flags)
	}
	if commonerr != nil {
		commonerr = commonerr.WithProperty(redis.EKRequests, requests)
		for i := 0; i < len(requests); i++ {
			if i != errpos {
				cb.Resolve(commonerr.WithProperty(redis.EKRequest, requests[i]), start+uint64(i))
			} else {
				cb.Resolve(err.WithProperty(redis.EKRequests, requests), start+uint64(i))
			}
		}
		if flags&DoTransaction != 0 {
			// resolve EXEC request as well
			cb.Resolve(commonerr, start+uint64(len(requests)))
		}
	}
}

func (conn *Connection) doSendBatch(requests []Request, cb Future, start uint64, flags int) *errorx.Error {
	if err := cb.Cancelled(); err != nil {
		return conn.err(redis.ErrRequestCancelled)
	}

	if len(requests) == 0 {
		if flags&DoTransaction != 0 {
			cb.Resolve([]interface{}{}, start)
		}
		return nil
	}

	conn.futmtx.Lock()
	defer conn.futmtx.Unlock()

	// we need to check conn.state first
	// since we do not lock connection itself, we need to use atomics.
	// Note: we do not check for connConnecting, ie we will try to send request after connection established.
	switch atomic.LoadUint32(&conn.state) {
	case connClosed:
		return conn.errWrap(redis.ErrContextClosed, conn.ctx.Err())
	case connDisconnected:
		return conn.err(ErrNotConnected)
	}

	futures := conn.futures
	if flags&DoAsking != 0 {
		// send ASKING request before actual
		futures = append(futures, future{&dumb, 0, 0, Request{"ASKING", nil}})
	}
	if flags&DoTransaction != 0 {
		// send MULTI request for transaction start
		futures = append(futures, future{&dumb, 0, 0, Request{"MULTI", nil}})
	}

	now := nownano()

	for i, req := range requests {
		futures = append(futures, future{cb, start + uint64(i), now, req})
	}

	if flags&DoTransaction != 0 {
		// send EXEC request for transaction end
		futures = append(futures, future{cb, start + uint64(len(requests)), now, Request{"EXEC", nil}})
	}

	// should notify writer about this shard having queries
	// Since we are under shard lock, it is safe to send notification before assigning futures.
	if len(conn.futures) == 0 {
		if conn.opts.WritePause > 0 {
			conn.futtimer.Reset(conn.opts.WritePause)
		} else {
			select {
			case conn.futsignal <- struct{}{}:
			default:
			}
		}
	}
	conn.futures = futures
	return nil
}

// wrapped preserves Cancelled method of wrapped future, but redefines Resolve to react only on result of EXEC.
type transactionFuture struct {
	Future
	l   int
	off uint64
}

func (cw transactionFuture) Resolve(res interface{}, n uint64) {
	if n == uint64(cw.l) {
		cw.Future.Resolve(res, cw.off)
	}
}

// SendTransaction implements redis.Sender.SendTransaction
func (conn *Connection) SendTransaction(reqs []Request, cb Future, off uint64) {
	conn.SendBatchFlags(reqs, transactionFuture{cb, len(reqs), off}, 0, DoTransaction)
}

// String implements fmt.Stringer
func (conn *Connection) String() string {
	return fmt.Sprintf("*redisconn.Connection{addr: %s}", conn.addr)
}

/********** private api **************/

// setup connection to redis
func (conn *Connection) dial() error {
	var connection net.Conn
	var err error

	// detect network and actual address
	network := "tcp"
	address := conn.addr
	timeout := conn.opts.DialTimeout
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	if address[0] == '.' || address[0] == '/' {
		network = "unix"
	} else if address[0:7] == "unix://" {
		network = "unix"
		address = address[7:]
	} else if address[0:6] == "tcp://" {
		network = "tcp"
		address = address[6:]
	}

	// dial to redis
	dialer := net.Dialer{
		Timeout:       timeout,
		DualStack:     true,
		FallbackDelay: timeout / 2,
		KeepAlive:     conn.opts.TCPKeepAlive,
	}
	connection, err = dialer.DialContext(conn.ctx, network, address)
	if err != nil {
		return conn.errWrap(ErrDial, err)
	}

	dc := newDeadlineIO(connection, conn.opts.IOTimeout)
	r := bufio.NewReaderSize(dc, 128*1024)

	// Password request
	var req []byte
	if conn.opts.Password != "" {
		req, _ = redis.AppendRequest(req, redis.Req("AUTH", conn.opts.Password))
	}
	const pingReq = "*1\r\n$4\r\nPING\r\n"
	// Ping request
	req = append(req, pingReq...)
	// Select request
	if conn.opts.DB != 0 {
		req, _ = redis.AppendRequest(req, redis.Req("SELECT", conn.opts.DB))
	}
	// Force timeout
	if conn.opts.IOTimeout > 0 {
		connection.SetWriteDeadline(time.Now().Add(conn.opts.IOTimeout))
	}
	if _, err = dc.Write(req); err != nil {
		connection.Close()
		return conn.errWrap(ErrConnSetup, err)
	}
	// Disarm timeout
	connection.SetWriteDeadline(time.Time{})

	var res interface{}
	// Password response
	if conn.opts.Password != "" {
		res = redis.ReadResponse(r)
		if err := redis.AsErrorx(res); err != nil {
			connection.Close()
			if !err.IsOfType(redis.ErrIO) {
				return conn.errWrap(ErrAuth, err)
			}
			return conn.errWrap(ErrConnSetup, err)
		}
	}
	// PING Response
	res = redis.ReadResponse(r)
	if err := redis.AsErrorx(res); err != nil {
		connection.Close()
		if !err.IsOfType(redis.ErrIO) {
			return conn.errWrap(ErrInit, err)
		}
		return conn.errWrap(ErrConnSetup, err)
	}
	if str, ok := res.(string); !ok || str != "PONG" {
		connection.Close()
		return conn.addProps(ErrInit.New("ping response mismatch")).
			WithProperty(redis.EKResponse, res)
	}
	// SELECT DB Response
	if conn.opts.DB != 0 {
		res = redis.ReadResponse(r)
		if err := redis.AsErrorx(res); err != nil {
			connection.Close()
			if !err.IsOfType(redis.ErrIO) {
				return conn.errWrap(ErrInit, err)
			}
			return conn.errWrap(ErrConnSetup, err)
		}
		if str, ok := res.(string); !ok || str != "OK" {
			connection.Close()
			return ErrInit.New("SELECT db response mismatch").
				WithProperty(EKDb, conn.opts.DB).
				WithProperty(redis.EKResponse, res)
		}
	}

	conn.c = connection

	one := &oneconn{
		c: connection,
		// We intentionally limit futures channel capacity:
		// this way we will force to write some first request eagerly to network,
		// and pause until first response returns.
		// During this time, many new request will be buffered, and then we will
		// be switching to steady state pipelining: new requests will be written
		// with the same speed responses will arrive.
		futures: make(chan []future, 64),
		control: make(chan struct{}),
		futpool: make(chan []future, 128),
	}

	go conn.writer(one)
	go conn.reader(r, one)

	return nil
}

func (conn *Connection) createConnection(reconnect bool, wg *sync.WaitGroup) error {
	var err error
	for conn.c == nil && atomic.LoadUint32(&conn.state) == connDisconnected {
		conn.report(LogConnecting{})
		now := time.Now()
		// start accepting requests
		atomic.StoreUint32(&conn.state, connConnecting)
		if wg != nil {
			wg.Done()
			wg = nil
		}
		err = conn.dial()
		if err == nil {
			atomic.StoreUint32(&conn.state, connConnected)
			conn.report(LogConnected{
				LocalAddr:  conn.c.LocalAddr().String(),
				RemoteAddr: conn.c.RemoteAddr().String(),
			})
			return nil
		}

		conn.report(LogConnectFailed{Error: err})
		// stop accepting request
		atomic.StoreUint32(&conn.state, connDisconnected)
		// revoke accumulated requests
		conn.futmtx.Lock()
		conn.dropFutures(err)
		conn.futmtx.Unlock()

		// If you doesn't use reconnection, quit
		if !reconnect {
			return err
		}
		conn.mutex.Unlock()
		// do not spend CPU on useless attempts
		time.Sleep(now.Add(conn.opts.ReconnectPause).Sub(time.Now()))
		conn.mutex.Lock()
	}
	if wg != nil {
		wg.Done()
	}
	if atomic.LoadUint32(&conn.state) == connClosed {
		err = conn.ctx.Err()
	}
	return err
}

// dropFutures revokes all accumulated requests
// Should be called with all shards locked.
func (conn *Connection) dropFutures(err error) {
	// first, empty futsignal queue.
	conn.futtimer.Stop()
	select {
	case <-conn.futsignal:
	default:
	}
	select {
	case <-conn.futtimer.C:
	default:
	}
	// then Resolve all future with error
	for _, fut := range conn.futures {
		conn.resolve(fut, err)
	}
	conn.futures = nil
}

func (conn *Connection) closeConnection(neterr *errorx.Error, forever bool) {
	if forever {
		atomic.StoreUint32(&conn.state, connClosed)
		conn.report(LogContextClosed{Error: neterr.Cause()})
	} else {
		atomic.StoreUint32(&conn.state, connDisconnected)
		conn.report(LogDisconnected{
			Error:      neterr,
			LocalAddr:  conn.c.LocalAddr().String(),
			RemoteAddr: conn.c.RemoteAddr().String(),
		})
	}

	if conn.c != nil {
		conn.c.Close()
		conn.c = nil
	}

	conn.futmtx.Lock()
	defer conn.futmtx.Unlock()
	if forever {
		conn.futtimer.Stop()
		// have to close futsignal under futmtx locked
		close(conn.futsignal)
	}

	conn.dropFutures(neterr)
}

func (conn *Connection) control() {
	timeout := conn.opts.IOTimeout / 3
	if timeout <= 0 {
		timeout = time.Second
	}
	t := time.NewTicker(timeout)
	defer t.Stop()
	for {
		select {
		case <-conn.ctx.Done():
			conn.mutex.Lock()
			defer conn.mutex.Unlock()
			closeErr := conn.errWrap(redis.ErrContextClosed, conn.ctx.Err())
			conn.closeConnection(closeErr, true)
			return
		case <-t.C:
		}
		// send PING at least 3 times per IO timeout, therefore read deadline will not be exceeded
		if err := conn.Ping(); err != nil {
			// I really don't know what to do here :-(
		}
	}
}

// setErr is called by either read or write loop in case of error
func (one *oneconn) setErr(neterr error, conn *Connection) {
	// lets sure error is set only once
	one.erronce.Do(func() {
		// notify writer to stop writting
		close(one.control)
		rerr, ok := neterr.(*errorx.Error)
		if !ok {
			rerr = conn.errWrap(redis.ErrIO, neterr)
		}
		rerr = conn.addProps(rerr)
		one.err = rerr
		// and try to reconnect asynchronously
		go conn.reconnect(rerr, one.c)
	})
}

func (conn *Connection) reconnect(neterr *errorx.Error, c net.Conn) {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	if atomic.LoadUint32(&conn.state) == connClosed {
		return
	}
	if conn.opts.ReconnectPause < 0 {
		conn.Close()
		return
	}
	if conn.c == c {
		conn.closeConnection(neterr, false)
		conn.createConnection(true, nil)
	}
}

// writer is a core writer loop. It is part of oneconn pair.
// It doesn't write requests immediately to network, but throttles itself to accumulate more requests.
// It is root of good pipelined performance: trade latency for throughtput.
func (conn *Connection) writer(one *oneconn) {
	var packet []byte
	var futures []future
	var ok bool

	defer func() {
		// on method exit send last futures to read loop.
		// Read loop will revoke these requests with error.
		if len(futures) != 0 {
			one.futures <- futures
		}
		// And inform read loop that our reader-writer pair is dying.
		close(one.futures)
	}()

	round := 1023
	for {
		// wait for dirtyShard or close of our reader-writer pair.
		select {
		case _, ok = <-conn.futsignal:
			if !ok {
				// user closed connection
				return
			}
		case <-conn.futtimer.C:
		case <-one.control:
			// this reader-writer pair is obsolete
			return
		}

		conn.futmtx.Lock()
		// fetch requests from shard, and replace it with empty buffer with non-zero capacity
		futures, conn.futures = conn.futures, futures
		conn.futmtx.Unlock()

		if len(futures) == 0 {
			// There are multiple ways to come here, and most of them are through dropFutures.
			// Lets just ignore them.
			continue
		}

		// serialize requests
		for _, fut := range futures {
			var err error
			if packet, err = redis.AppendRequest(packet, fut.req); err != nil {
				// since we checked arguments in doSend and doSendBatch, error here is a signal of programmer error.
				// lets just panic and die.
				panic(err)
			}
		}

		if _, err := one.c.Write(packet); err != nil {
			one.setErr(err, conn)
			return
		}

		// every 1023 writes check our buffer.
		// If it is too large, then lets GC to free it.
		if round--; round == 0 {
			round = 1023
			if cap(packet) > 128*1024 {
				packet = nil
			}
		}
		// otherwise, reuse buffer
		packet = packet[:0]

		one.futures <- futures

		select {
		// reuse request buffer
		case futures = <-one.futpool:
		default:
			// or allocate new one
			futures = make([]future, 0, len(futures)*2)
		}
	}
}

func (conn *Connection) reader(r *bufio.Reader, one *oneconn) {
	var futures []future
	var i int
	var res interface{}
	var ok bool

	for {
		// try to read response from buffered socket.
		// Here is IOTimeout handled as well (through deadlineIO wrapper around socket).
		res = redis.ReadResponse(r)
		if rerr := redis.AsErrorx(res); rerr != nil {
			if !rerr.IsOfType(redis.ErrResult) {
				// it is not redis-sended error, then close connection
				// (most probably, it is already closed. But also it could be timeout).
				one.setErr(rerr, conn)
				break
			}
		}
		if i == len(futures) {
			// this batch of requests exhausted,
			// lets recycle it
			i = 0
			select {
			case one.futpool <- futures[:0]:
			default:
			}
			// and fetch next one.
			futures, ok = <-one.futures
			if !ok {
				break
			}
		}
		// fetch request corresponding to answer
		fut := futures[i]
		futures[i] = future{}
		i++
		// and resolve it
		if rerr := redis.AsErrorx(res); rerr != nil {
			res = conn.addProps(rerr).WithProperty(redis.EKRequest, fut.req)
		}
		conn.resolve(fut, res)
	}

	// oops, connection is broken.
	// Should resolve already fetched requests with error.
	for _, fut := range futures[i:] {
		conn.resolve(fut, one.err)
	}
	// And should resolve all remaining requests as well
	// (looping until writer closes channel).
	for futures := range one.futures {
		for _, fut := range futures {
			conn.resolve(fut, one.err)
		}
	}
}

// create error with connection as an attribute.
func (conn *Connection) err(kind *errorx.Type) *errorx.Error {
	return conn.addProps(kind.NewWithNoMessage())
}

func (conn *Connection) errWrap(kind *errorx.Type, cause error) *errorx.Error {
	return conn.addProps(kind.WrapWithNoMessage(cause))
}

func (conn *Connection) addProps(err *errorx.Error) *errorx.Error {
	err = withNewProperty(err, EKConnection, conn)
	err = withNewProperty(err, redis.EKAddress, conn.Addr())
	return err
}
