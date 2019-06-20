package fasthttp

import (
	"fmt"
	"net"
	"sync"
)

type perIPConnCounter struct {
	pool sync.Pool
	lock sync.Mutex
	m    map[uint32]int
}

func (cc *perIPConnCounter) Register(ip uint32) int {
	cc.lock.Lock()
	if cc.m == nil {
		cc.m = make(map[uint32]int)
	}
	n := cc.m[ip] + 1
	cc.m[ip] = n
	cc.lock.Unlock()
	return n
}

func (cc *perIPConnCounter) Unregister(ip uint32) {
	cc.lock.Lock()
	if cc.m == nil {
		cc.lock.Unlock()
		panic("BUG: perIPConnCounter.Register() wasn't called")
	}
	n := cc.m[ip] - 1
	if n < 0 {
		cc.lock.Unlock()
		panic(fmt.Sprintf("BUG: negative per-ip counter=%d for ip=%d", n, ip))
	}
	cc.m[ip] = n
	cc.lock.Unlock()
}

type perIPConn struct {
	net.Conn

	ip               uint32
	perIPConnCounter *perIPConnCounter
}

func acquirePerIPConn(conn net.Conn, ip uint32, counter *perIPConnCounter) *perIPConn {
	v := counter.pool.Get()
	if v == nil {
		v = &perIPConn{
			perIPConnCounter: counter,
		}
	}
	c := v.(*perIPConn)
	c.Conn = conn
	c.ip = ip
	return c
}

func releasePerIPConn(c *perIPConn) {
	c.Conn = nil
	c.perIPConnCounter.pool.Put(c)
}

func (c *perIPConn) Close() error {
	err := c.Conn.Close()
	c.perIPConnCounter.Unregister(c.ip)
	releasePerIPConn(c)
	return err
}

func getUint32IP(c net.Conn) uint32 {
	return ip2uint32(getConnIP4(c))
}

func getConnIP4(c net.Conn) net.IP {
	addr := c.RemoteAddr()
	ipAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return net.IPv4zero
	}
	return ipAddr.IP.To4()
}

func ip2uint32(ip net.IP) uint32 {
	if len(ip) != 4 {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint322ip(ip uint32) net.IP {
	b := make([]byte, 4)
	b[0] = byte(ip >> 24)
	b[1] = byte(ip >> 16)
	b[2] = byte(ip >> 8)
	b[3] = byte(ip)
	return b
}
