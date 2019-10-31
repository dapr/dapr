package gocql

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	randr    *rand.Rand
	mutRandr sync.Mutex
)

func init() {
	b := make([]byte, 4)
	if _, err := crand.Read(b); err != nil {
		panic(fmt.Sprintf("unable to seed random number generator: %v", err))
	}

	randr = rand.New(rand.NewSource(int64(readInt(b))))
}

// Ensure that the atomic variable is aligned to a 64bit boundary
// so that atomic operations can be applied on 32bit architectures.
type controlConn struct {
	started      int32
	reconnecting int32

	session *Session
	conn    atomic.Value

	retry RetryPolicy

	quit chan struct{}
}

func createControlConn(session *Session) *controlConn {
	control := &controlConn{
		session: session,
		quit:    make(chan struct{}),
		retry:   &SimpleRetryPolicy{NumRetries: 3},
	}

	control.conn.Store((*connHost)(nil))

	return control
}

func (c *controlConn) heartBeat() {
	if !atomic.CompareAndSwapInt32(&c.started, 0, 1) {
		return
	}

	sleepTime := 1 * time.Second
	timer := time.NewTimer(sleepTime)
	defer timer.Stop()

	for {
		timer.Reset(sleepTime)

		select {
		case <-c.quit:
			return
		case <-timer.C:
		}

		resp, err := c.writeFrame(&writeOptionsFrame{})
		if err != nil {
			goto reconn
		}

		switch resp.(type) {
		case *supportedFrame:
			// Everything ok
			sleepTime = 5 * time.Second
			continue
		case error:
			goto reconn
		default:
			panic(fmt.Sprintf("gocql: unknown frame in response to options: %T", resp))
		}

	reconn:
		// try to connect a bit faster
		sleepTime = 1 * time.Second
		c.reconnect(true)
		continue
	}
}

var hostLookupPreferV4 = os.Getenv("GOCQL_HOST_LOOKUP_PREFER_V4") == "true"

func hostInfo(addr string, defaultPort int) ([]*HostInfo, error) {
	var port int
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
		port = defaultPort
	} else {
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return nil, err
		}
	}

	var hosts []*HostInfo

	// Check if host is a literal IP address
	if ip := net.ParseIP(host); ip != nil {
		hosts = append(hosts, &HostInfo{hostname: host, connectAddress: ip, port: port})
		return hosts, nil
	}

	// Look up host in DNS
	ips, err := LookupIP(host)
	if err != nil {
		return nil, err
	} else if len(ips) == 0 {
		return nil, fmt.Errorf("No IP's returned from DNS lookup for %q", addr)
	}

	// Filter to v4 addresses if any present
	if hostLookupPreferV4 {
		var preferredIPs []net.IP
		for _, v := range ips {
			if v4 := v.To4(); v4 != nil {
				preferredIPs = append(preferredIPs, v4)
			}
		}
		if len(preferredIPs) != 0 {
			ips = preferredIPs
		}
	}

	for _, ip := range ips {
		hosts = append(hosts, &HostInfo{hostname: host, connectAddress: ip, port: port})
	}

	return hosts, nil
}

func shuffleHosts(hosts []*HostInfo) []*HostInfo {
	shuffled := make([]*HostInfo, len(hosts))
	copy(shuffled, hosts)

	mutRandr.Lock()
	randr.Shuffle(len(hosts), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	mutRandr.Unlock()

	return shuffled
}

func (c *controlConn) shuffleDial(endpoints []*HostInfo) (*Conn, error) {
	// shuffle endpoints so not all drivers will connect to the same initial
	// node.
	shuffled := shuffleHosts(endpoints)

	cfg := *c.session.connCfg
	cfg.disableCoalesce = true

	var err error
	for _, host := range shuffled {
		var conn *Conn
		conn, err = c.session.dial(host, &cfg, c)
		if err == nil {
			return conn, nil
		}

		Logger.Printf("gocql: unable to dial control conn %v: %v\n", host.ConnectAddress(), err)
	}

	return nil, err
}

// this is going to be version dependant and a nightmare to maintain :(
var protocolSupportRe = regexp.MustCompile(`the lowest supported version is \d+ and the greatest is (\d+)$`)

func parseProtocolFromError(err error) int {
	// I really wish this had the actual info in the error frame...
	matches := protocolSupportRe.FindAllStringSubmatch(err.Error(), -1)
	if len(matches) != 1 || len(matches[0]) != 2 {
		if verr, ok := err.(*protocolError); ok {
			return int(verr.frame.Header().version.version())
		}
		return 0
	}

	max, err := strconv.Atoi(matches[0][1])
	if err != nil {
		return 0
	}

	return max
}

func (c *controlConn) discoverProtocol(hosts []*HostInfo) (int, error) {
	hosts = shuffleHosts(hosts)

	connCfg := *c.session.connCfg
	connCfg.ProtoVersion = 4 // TODO: define maxProtocol

	handler := connErrorHandlerFn(func(c *Conn, err error, closed bool) {
		// we should never get here, but if we do it means we connected to a
		// host successfully which means our attempted protocol version worked
		if !closed {
			c.Close()
		}
	})

	var err error
	for _, host := range hosts {
		var conn *Conn
		conn, err = c.session.dial(host, &connCfg, handler)
		if conn != nil {
			conn.Close()
		}

		if err == nil {
			return connCfg.ProtoVersion, nil
		}

		if proto := parseProtocolFromError(err); proto > 0 {
			return proto, nil
		}
	}

	return 0, err
}

func (c *controlConn) connect(hosts []*HostInfo) error {
	if len(hosts) == 0 {
		return errors.New("control: no endpoints specified")
	}

	conn, err := c.shuffleDial(hosts)
	if err != nil {
		return fmt.Errorf("control: unable to connect to initial hosts: %v", err)
	}

	if err := c.setupConn(conn); err != nil {
		conn.Close()
		return fmt.Errorf("control: unable to setup connection: %v", err)
	}

	// we could fetch the initial ring here and update initial host data. So that
	// when we return from here we have a ring topology ready to go.

	go c.heartBeat()

	return nil
}

type connHost struct {
	conn *Conn
	host *HostInfo
}

func (c *controlConn) setupConn(conn *Conn) error {
	if err := c.registerEvents(conn); err != nil {
		conn.Close()
		return err
	}

	// TODO(zariel): do we need to fetch host info everytime
	// the control conn connects? Surely we have it cached?
	host, err := conn.localHostInfo(context.TODO())
	if err != nil {
		return err
	}

	ch := &connHost{
		conn: conn,
		host: host,
	}

	c.conn.Store(ch)
	c.session.handleNodeUp(host.ConnectAddress(), host.Port(), false)

	return nil
}

func (c *controlConn) registerEvents(conn *Conn) error {
	var events []string

	if !c.session.cfg.Events.DisableTopologyEvents {
		events = append(events, "TOPOLOGY_CHANGE")
	}
	if !c.session.cfg.Events.DisableNodeStatusEvents {
		events = append(events, "STATUS_CHANGE")
	}
	if !c.session.cfg.Events.DisableSchemaEvents {
		events = append(events, "SCHEMA_CHANGE")
	}

	if len(events) == 0 {
		return nil
	}

	framer, err := conn.exec(context.Background(),
		&writeRegisterFrame{
			events: events,
		}, nil)
	if err != nil {
		return err
	}

	frame, err := framer.parseFrame()
	if err != nil {
		return err
	} else if _, ok := frame.(*readyFrame); !ok {
		return fmt.Errorf("unexpected frame in response to register: got %T: %v\n", frame, frame)
	}

	return nil
}

func (c *controlConn) reconnect(refreshring bool) {
	if !atomic.CompareAndSwapInt32(&c.reconnecting, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&c.reconnecting, 0)
	// TODO: simplify this function, use session.ring to get hosts instead of the
	// connection pool

	var host *HostInfo
	ch := c.getConn()
	if ch != nil {
		host = ch.host
		ch.conn.Close()
	}

	var newConn *Conn
	if host != nil {
		// try to connect to the old host
		conn, err := c.session.connect(host, c)
		if err != nil {
			// host is dead
			// TODO: this is replicated in a few places
			if c.session.cfg.ConvictionPolicy.AddFailure(err, host) {
				c.session.handleNodeDown(host.ConnectAddress(), host.Port())
			}
		} else {
			newConn = conn
		}
	}

	// TODO: should have our own round-robin for hosts so that we can try each
	// in succession and guarantee that we get a different host each time.
	if newConn == nil {
		host := c.session.ring.rrHost()
		if host == nil {
			c.connect(c.session.ring.endpoints)
			return
		}

		var err error
		newConn, err = c.session.connect(host, c)
		if err != nil {
			// TODO: add log handler for things like this
			return
		}
	}

	if err := c.setupConn(newConn); err != nil {
		newConn.Close()
		Logger.Printf("gocql: control unable to register events: %v\n", err)
		return
	}

	if refreshring {
		c.session.hostSource.refreshRing()
	}
}

func (c *controlConn) HandleError(conn *Conn, err error, closed bool) {
	if !closed {
		return
	}

	oldConn := c.getConn()

	// If connection has long gone, and not been attempted for awhile,
	// it's possible to have oldConn as nil here (#1297).
	if oldConn != nil && oldConn.conn != conn {
		return
	}

	c.reconnect(false)
}

func (c *controlConn) getConn() *connHost {
	return c.conn.Load().(*connHost)
}

func (c *controlConn) writeFrame(w frameWriter) (frame, error) {
	ch := c.getConn()
	if ch == nil {
		return nil, errNoControl
	}

	framer, err := ch.conn.exec(context.Background(), w, nil)
	if err != nil {
		return nil, err
	}

	return framer.parseFrame()
}

func (c *controlConn) withConnHost(fn func(*connHost) *Iter) *Iter {
	const maxConnectAttempts = 5
	connectAttempts := 0

	for i := 0; i < maxConnectAttempts; i++ {
		ch := c.getConn()
		if ch == nil {
			if connectAttempts > maxConnectAttempts {
				break
			}

			connectAttempts++

			c.reconnect(false)
			continue
		}

		return fn(ch)
	}

	return &Iter{err: errNoControl}
}

func (c *controlConn) withConn(fn func(*Conn) *Iter) *Iter {
	return c.withConnHost(func(ch *connHost) *Iter {
		return fn(ch.conn)
	})
}

// query will return nil if the connection is closed or nil
func (c *controlConn) query(statement string, values ...interface{}) (iter *Iter) {
	q := c.session.Query(statement, values...).Consistency(One).RoutingKey([]byte{}).Trace(nil)

	for {
		iter = c.withConn(func(conn *Conn) *Iter {
			return conn.executeQuery(context.TODO(), q)
		})

		if gocqlDebug && iter.err != nil {
			Logger.Printf("control: error executing %q: %v\n", statement, iter.err)
		}

		q.AddAttempts(1, c.getConn().host)
		if iter.err == nil || !c.retry.Attempt(q) {
			break
		}
	}

	return
}

func (c *controlConn) awaitSchemaAgreement() error {
	return c.withConn(func(conn *Conn) *Iter {
		return &Iter{err: conn.awaitSchemaAgreement(context.TODO())}
	}).err
}

func (c *controlConn) close() {
	if atomic.CompareAndSwapInt32(&c.started, 1, -1) {
		c.quit <- struct{}{}
	}

	ch := c.getConn()
	if ch != nil {
		ch.conn.Close()
	}
}

var errNoControl = errors.New("gocql: no control connection available")
