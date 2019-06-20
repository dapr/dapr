package fasthttp

import (
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Dial dials the given TCP addr using tcp4.
//
// This function has the following additional features comparing to net.Dial:
//
//   * It reduces load on DNS resolver by caching resolved TCP addressed
//     for DefaultDNSCacheDuration.
//   * It dials all the resolved TCP addresses in round-robin manner until
//     connection is established. This may be useful if certain addresses
//     are temporarily unreachable.
//   * It returns ErrDialTimeout if connection cannot be established during
//     DefaultDialTimeout seconds. Use DialTimeout for customizing dial timeout.
//
// This dialer is intended for custom code wrapping before passing
// to Client.Dial or HostClient.Dial.
//
// For instance, per-host counters and/or limits may be implemented
// by such wrappers.
//
// The addr passed to the function must contain port. Example addr values:
//
//     * foobar.baz:443
//     * foo.bar:80
//     * aaa.com:8080
func Dial(addr string) (net.Conn, error) {
	return getDialer(DefaultDialTimeout, false)(addr)
}

// DialTimeout dials the given TCP addr using tcp4 using the given timeout.
//
// This function has the following additional features comparing to net.Dial:
//
//   * It reduces load on DNS resolver by caching resolved TCP addressed
//     for DefaultDNSCacheDuration.
//   * It dials all the resolved TCP addresses in round-robin manner until
//     connection is established. This may be useful if certain addresses
//     are temporarily unreachable.
//
// This dialer is intended for custom code wrapping before passing
// to Client.Dial or HostClient.Dial.
//
// For instance, per-host counters and/or limits may be implemented
// by such wrappers.
//
// The addr passed to the function must contain port. Example addr values:
//
//     * foobar.baz:443
//     * foo.bar:80
//     * aaa.com:8080
func DialTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	return getDialer(timeout, false)(addr)
}

// DialDualStack dials the given TCP addr using both tcp4 and tcp6.
//
// This function has the following additional features comparing to net.Dial:
//
//   * It reduces load on DNS resolver by caching resolved TCP addressed
//     for DefaultDNSCacheDuration.
//   * It dials all the resolved TCP addresses in round-robin manner until
//     connection is established. This may be useful if certain addresses
//     are temporarily unreachable.
//   * It returns ErrDialTimeout if connection cannot be established during
//     DefaultDialTimeout seconds. Use DialDualStackTimeout for custom dial
//     timeout.
//
// This dialer is intended for custom code wrapping before passing
// to Client.Dial or HostClient.Dial.
//
// For instance, per-host counters and/or limits may be implemented
// by such wrappers.
//
// The addr passed to the function must contain port. Example addr values:
//
//     * foobar.baz:443
//     * foo.bar:80
//     * aaa.com:8080
func DialDualStack(addr string) (net.Conn, error) {
	return getDialer(DefaultDialTimeout, true)(addr)
}

// DialDualStackTimeout dials the given TCP addr using both tcp4 and tcp6
// using the given timeout.
//
// This function has the following additional features comparing to net.Dial:
//
//   * It reduces load on DNS resolver by caching resolved TCP addressed
//     for DefaultDNSCacheDuration.
//   * It dials all the resolved TCP addresses in round-robin manner until
//     connection is established. This may be useful if certain addresses
//     are temporarily unreachable.
//
// This dialer is intended for custom code wrapping before passing
// to Client.Dial or HostClient.Dial.
//
// For instance, per-host counters and/or limits may be implemented
// by such wrappers.
//
// The addr passed to the function must contain port. Example addr values:
//
//     * foobar.baz:443
//     * foo.bar:80
//     * aaa.com:8080
func DialDualStackTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	return getDialer(timeout, true)(addr)
}

func getDialer(timeout time.Duration, dualStack bool) DialFunc {
	if timeout <= 0 {
		timeout = DefaultDialTimeout
	}
	timeoutRounded := int(timeout.Seconds()*10 + 9)

	m := dialMap
	if dualStack {
		m = dialDualStackMap
	}

	dialMapLock.Lock()
	d := m[timeoutRounded]
	if d == nil {
		dialer := dialerStd
		if dualStack {
			dialer = dialerDualStack
		}
		d = dialer.NewDial(timeout)
		m[timeoutRounded] = d
	}
	dialMapLock.Unlock()
	return d
}

var (
	dialerStd       = &tcpDialer{}
	dialerDualStack = &tcpDialer{DualStack: true}

	dialMap          = make(map[int]DialFunc)
	dialDualStackMap = make(map[int]DialFunc)
	dialMapLock      sync.Mutex
)

type tcpDialer struct {
	DualStack bool

	tcpAddrsLock sync.Mutex
	tcpAddrsMap  map[string]*tcpAddrEntry

	concurrencyCh chan struct{}

	once sync.Once
}

const maxDialConcurrency = 1000

func (d *tcpDialer) NewDial(timeout time.Duration) DialFunc {
	d.once.Do(func() {
		d.concurrencyCh = make(chan struct{}, maxDialConcurrency)
		d.tcpAddrsMap = make(map[string]*tcpAddrEntry)
		go d.tcpAddrsClean()
	})

	return func(addr string) (net.Conn, error) {
		addrs, idx, err := d.getTCPAddrs(addr)
		if err != nil {
			return nil, err
		}
		network := "tcp4"
		if d.DualStack {
			network = "tcp"
		}

		var conn net.Conn
		n := uint32(len(addrs))
		deadline := time.Now().Add(timeout)
		for n > 0 {
			conn, err = tryDial(network, &addrs[idx%n], deadline, d.concurrencyCh)
			if err == nil {
				return conn, nil
			}
			if err == ErrDialTimeout {
				return nil, err
			}
			idx++
			n--
		}
		return nil, err
	}
}

func tryDial(network string, addr *net.TCPAddr, deadline time.Time, concurrencyCh chan struct{}) (net.Conn, error) {
	timeout := -time.Since(deadline)
	if timeout <= 0 {
		return nil, ErrDialTimeout
	}

	select {
	case concurrencyCh <- struct{}{}:
	default:
		tc := acquireTimer(timeout)
		isTimeout := false
		select {
		case concurrencyCh <- struct{}{}:
		case <-tc.C:
			isTimeout = true
		}
		releaseTimer(tc)
		if isTimeout {
			return nil, ErrDialTimeout
		}
	}

	timeout = -time.Since(deadline)
	if timeout <= 0 {
		<-concurrencyCh
		return nil, ErrDialTimeout
	}

	chv := dialResultChanPool.Get()
	if chv == nil {
		chv = make(chan dialResult, 1)
	}
	ch := chv.(chan dialResult)
	go func() {
		var dr dialResult
		dr.conn, dr.err = net.DialTCP(network, nil, addr)
		ch <- dr
		<-concurrencyCh
	}()

	var (
		conn net.Conn
		err  error
	)

	tc := acquireTimer(timeout)
	select {
	case dr := <-ch:
		conn = dr.conn
		err = dr.err
		dialResultChanPool.Put(ch)
	case <-tc.C:
		err = ErrDialTimeout
	}
	releaseTimer(tc)

	return conn, err
}

var dialResultChanPool sync.Pool

type dialResult struct {
	conn net.Conn
	err  error
}

// ErrDialTimeout is returned when TCP dialing is timed out.
var ErrDialTimeout = errors.New("dialing to the given TCP address timed out")

// DefaultDialTimeout is timeout used by Dial and DialDualStack
// for establishing TCP connections.
const DefaultDialTimeout = 3 * time.Second

type tcpAddrEntry struct {
	addrs    []net.TCPAddr
	addrsIdx uint32

	resolveTime time.Time
	pending     bool
}

// DefaultDNSCacheDuration is the duration for caching resolved TCP addresses
// by Dial* functions.
const DefaultDNSCacheDuration = time.Minute

func (d *tcpDialer) tcpAddrsClean() {
	expireDuration := 2 * DefaultDNSCacheDuration
	for {
		time.Sleep(time.Second)
		t := time.Now()

		d.tcpAddrsLock.Lock()
		for k, e := range d.tcpAddrsMap {
			if t.Sub(e.resolveTime) > expireDuration {
				delete(d.tcpAddrsMap, k)
			}
		}
		d.tcpAddrsLock.Unlock()
	}
}

func (d *tcpDialer) getTCPAddrs(addr string) ([]net.TCPAddr, uint32, error) {
	d.tcpAddrsLock.Lock()
	e := d.tcpAddrsMap[addr]
	if e != nil && !e.pending && time.Since(e.resolveTime) > DefaultDNSCacheDuration {
		e.pending = true
		e = nil
	}
	d.tcpAddrsLock.Unlock()

	if e == nil {
		addrs, err := resolveTCPAddrs(addr, d.DualStack)
		if err != nil {
			d.tcpAddrsLock.Lock()
			e = d.tcpAddrsMap[addr]
			if e != nil && e.pending {
				e.pending = false
			}
			d.tcpAddrsLock.Unlock()
			return nil, 0, err
		}

		e = &tcpAddrEntry{
			addrs:       addrs,
			resolveTime: time.Now(),
		}

		d.tcpAddrsLock.Lock()
		d.tcpAddrsMap[addr] = e
		d.tcpAddrsLock.Unlock()
	}

	idx := atomic.AddUint32(&e.addrsIdx, 1)
	return e.addrs, idx, nil
}

func resolveTCPAddrs(addr string, dualStack bool) ([]net.TCPAddr, error) {
	host, portS, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portS)
	if err != nil {
		return nil, err
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}

	n := len(ips)
	addrs := make([]net.TCPAddr, 0, n)
	for i := 0; i < n; i++ {
		ip := ips[i]
		if !dualStack && ip.To4() == nil {
			continue
		}
		addrs = append(addrs, net.TCPAddr{
			IP:   ip,
			Port: port,
		})
	}
	if len(addrs) == 0 {
		return nil, errNoDNSEntries
	}
	return addrs, nil
}

var errNoDNSEntries = errors.New("couldn't find DNS entries for the given domain. Try using DialDualStack")
