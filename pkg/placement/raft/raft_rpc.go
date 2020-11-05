package raft

import (
	"crypto/tls"
	"net"
	"sync"
	"time"

	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

var (
	errNotAdvertisable = errors.New("local bind address is not advertisable")
	errNotTCP          = errors.New("local address is not a TCP address")
)

// RaftSecureLayer implements the raft.StreamLayer interface.
type RaftSecureLayer struct {
	advertise net.Addr
	listener  net.Listener
	certChain *dapr_credentials.CertChain

	// Tracks if we are closed
	closed    bool
	closeLock sync.Mutex
}

// NewRaftSecureLayer is used to initialize a new RaftLayer which can
// be used as a StreamLayer for Raft. If a tlsConfig is provided,
// then the connection will use TLS.
func NewRaftSecureLayer(bindAddr string, advertise net.Addr, certChain *dapr_credentials.CertChain) (*RaftSecureLayer, error) {
	config := &tls.Config{
		InsecureSkipVerify: true,
	}

	if certChain != nil {
		cert, err := tls.X509KeyPair(certChain.Cert, certChain.Key)
		if err != nil {
			return nil, err
		}
		config.Certificates = []tls.Certificate{cert}
	}

	list, err := tls.Listen("tcp", bindAddr, config)
	if err != nil {
		return nil, err
	}

	// Verify that we have a usable advertise address
	addr, ok := advertise.(*net.TCPAddr)
	if !ok {
		list.Close()
		return nil, errNotTCP
	}
	if addr.IP == nil || addr.IP.IsUnspecified() {
		list.Close()
		return nil, errNotAdvertisable
	}

	return &RaftSecureLayer{
		certChain: certChain,
		advertise: advertise,
		listener:  list,
	}, nil
}

// Accept is used to return connection.
func (l *RaftSecureLayer) Accept() (net.Conn, error) {
	return l.listener.Accept()
}

// Close is used to stop listening for Raft connections.
func (l *RaftSecureLayer) Close() error {
	l.closeLock.Lock()
	defer l.closeLock.Unlock()

	if !l.closed {
		l.closed = true
		l.listener.Close()
	}

	return nil
}

// Addr is used to return the address of the listener
func (l *RaftSecureLayer) Addr() net.Addr {
	return l.listener.Addr()
}

// Dial is used to create a new outgoing connection
func (l *RaftSecureLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	d := &tls.Dialer{
		NetDialer: &net.Dialer{
			Timeout: timeout,
		},
		Config: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	conn, err := d.Dial("tcp", string(address))
	if err != nil {
		return nil, err
	}

	return conn, err
}
