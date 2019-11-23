// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package topology

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/snappy"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.mongodb.org/mongo-driver/x/mongo/driver/address"
	"go.mongodb.org/mongo-driver/x/mongo/driver/description"
	"go.mongodb.org/mongo-driver/x/mongo/driver/wiremessage"
)

var globalConnectionID uint64 = 1

func nextConnectionID() uint64 { return atomic.AddUint64(&globalConnectionID, 1) }

type connection struct {
	id               string
	nc               net.Conn // When nil, the connection is closed.
	addr             address.Address
	idleTimeout      time.Duration
	idleDeadline     atomic.Value // Stores a time.Time
	lifetimeDeadline time.Time
	readTimeout      time.Duration
	writeTimeout     time.Duration
	desc             description.Server
	compressor       wiremessage.CompressorID
	zliblevel        int
	connected        int32 // must be accessed using the sync/atomic package
	connectDone      chan struct{}
	connectErr       error
	config           *connectionConfig

	// pool related fields
	pool       *pool
	poolID     uint64
	generation uint64
}

// newConnection handles the creation of a connection. It does not connect the connection.
func newConnection(ctx context.Context, addr address.Address, opts ...ConnectionOption) (*connection, error) {
	cfg, err := newConnectionConfig(opts...)
	if err != nil {
		return nil, err
	}

	var lifetimeDeadline time.Time
	if cfg.lifeTimeout > 0 {
		lifetimeDeadline = time.Now().Add(cfg.lifeTimeout)
	}

	id := fmt.Sprintf("%s[-%d]", addr, nextConnectionID())

	c := &connection{
		id:               id,
		addr:             addr,
		idleTimeout:      cfg.idleTimeout,
		lifetimeDeadline: lifetimeDeadline,
		readTimeout:      cfg.readTimeout,
		writeTimeout:     cfg.writeTimeout,
		connectDone:      make(chan struct{}),
		config:           cfg,
	}
	atomic.StoreInt32(&c.connected, initialized)

	return c, nil
}

// connect handles the I/O for a connection. It will dial, configure TLS, and perform
// initialization handshakes.
func (c *connection) connect(ctx context.Context) {

	if !atomic.CompareAndSwapInt32(&c.connected, initialized, connected) {
		return
	}
	defer close(c.connectDone)

	var err error
	c.nc, err = c.config.dialer.DialContext(ctx, c.addr.Network(), c.addr.String())
	if err != nil {
		atomic.StoreInt32(&c.connected, disconnected)
		c.connectErr = ConnectionError{Wrapped: err, init: true}
		return
	}

	if c.config.tlsConfig != nil {
		tlsConfig := c.config.tlsConfig.Clone()
		c.nc, err = configureTLS(ctx, c.nc, c.addr, tlsConfig)
		if err != nil {
			atomic.StoreInt32(&c.connected, disconnected)
			c.connectErr = ConnectionError{Wrapped: err, init: true}
			return
		}
	}

	c.bumpIdleDeadline()

	// running isMaster and authentication is handled by a handshaker on the configuration instance.
	handshaker := c.config.handshaker
	if handshaker == nil {
		return
	}

	handshakeConn := initConnection{c}
	c.desc, err = handshaker.GetDescription(ctx, c.addr, handshakeConn)
	if err == nil {
		err = handshaker.FinishHandshake(ctx, handshakeConn)
	}
	if err != nil {
		if c.nc != nil {
			_ = c.nc.Close()
		}
		atomic.StoreInt32(&c.connected, disconnected)
		c.connectErr = ConnectionError{Wrapped: err, init: true}
		return
	}

	if c.config.descCallback != nil {
		c.config.descCallback(c.desc)
	}
	if len(c.desc.Compression) > 0 {
	clientMethodLoop:
		for _, method := range c.config.compressors {
			for _, serverMethod := range c.desc.Compression {
				if method != serverMethod {
					continue
				}

				switch strings.ToLower(method) {
				case "snappy":
					c.compressor = wiremessage.CompressorSnappy
				case "zlib":
					c.compressor = wiremessage.CompressorZLib
					c.zliblevel = wiremessage.DefaultZlibLevel
					if c.config.zlibLevel != nil {
						c.zliblevel = *c.config.zlibLevel
					}
				}
				break clientMethodLoop
			}
		}
	}
}

func (c *connection) wait() error {
	if c.connectDone != nil {
		<-c.connectDone
	}
	return c.connectErr
}

func (c *connection) writeWireMessage(ctx context.Context, wm []byte) error {
	var err error
	if atomic.LoadInt32(&c.connected) != connected {
		return ConnectionError{ConnectionID: c.id, message: "connection is closed"}
	}
	select {
	case <-ctx.Done():
		return ConnectionError{ConnectionID: c.id, Wrapped: ctx.Err(), message: "failed to write"}
	default:
	}

	var deadline time.Time
	if c.writeTimeout != 0 {
		deadline = time.Now().Add(c.writeTimeout)
	}

	if dl, ok := ctx.Deadline(); ok && (deadline.IsZero() || dl.Before(deadline)) {
		deadline = dl
	}

	if err := c.nc.SetWriteDeadline(deadline); err != nil {
		return ConnectionError{ConnectionID: c.id, Wrapped: err, message: "failed to set write deadline"}
	}

	_, err = c.nc.Write(wm)
	if err != nil {
		c.close()
		return ConnectionError{ConnectionID: c.id, Wrapped: err, message: "unable to write wire message to network"}
	}

	c.bumpIdleDeadline()
	return nil
}

// readWireMessage reads a wiremessage from the connection. The dst parameter will be overwritten.
func (c *connection) readWireMessage(ctx context.Context, dst []byte) ([]byte, error) {
	if atomic.LoadInt32(&c.connected) != connected {
		return dst, ConnectionError{ConnectionID: c.id, message: "connection is closed"}
	}

	select {
	case <-ctx.Done():
		// We closeConnection the connection because we don't know if there is an unread message on the wire.
		c.close()
		return nil, ConnectionError{ConnectionID: c.id, Wrapped: ctx.Err(), message: "failed to read"}
	default:
	}

	var deadline time.Time
	if c.readTimeout != 0 {
		deadline = time.Now().Add(c.readTimeout)
	}

	if dl, ok := ctx.Deadline(); ok && (deadline.IsZero() || dl.Before(deadline)) {
		deadline = dl
	}

	if err := c.nc.SetReadDeadline(deadline); err != nil {
		return nil, ConnectionError{ConnectionID: c.id, Wrapped: err, message: "failed to set read deadline"}
	}

	// We use an array here because it only costs 4 bytes on the stack and means we'll only need to
	// reslice dst once instead of twice.
	var sizeBuf [4]byte

	// We do a ReadFull into an array here instead of doing an opportunistic ReadAtLeast into dst
	// because there might be more than one wire message waiting to be read, for example when
	// reading messages from an exhaust cursor.
	_, err := io.ReadFull(c.nc, sizeBuf[:])
	if err != nil {
		// We closeConnection the connection because we don't know if there are other bytes left to read.
		c.close()
		return nil, ConnectionError{ConnectionID: c.id, Wrapped: err, message: "unable to decode message length"}
	}

	// read the length as an int32
	size := (int32(sizeBuf[0])) | (int32(sizeBuf[1]) << 8) | (int32(sizeBuf[2]) << 16) | (int32(sizeBuf[3]) << 24)

	if int(size) > cap(dst) {
		// Since we can't grow this slice without allocating, just allocate an entirely new slice.
		dst = make([]byte, 0, size)
	}
	// We need to ensure we don't accidentally read into a subsequent wire message, so we set the
	// size to read exactly this wire message.
	dst = dst[:size]
	copy(dst, sizeBuf[:])

	_, err = io.ReadFull(c.nc, dst[4:])
	if err != nil {
		// We closeConnection the connection because we don't know if there are other bytes left to read.
		c.close()
		return nil, ConnectionError{ConnectionID: c.id, Wrapped: err, message: "unable to read full message"}
	}

	c.bumpIdleDeadline()
	return dst, nil
}

func (c *connection) close() error {
	if atomic.LoadInt32(&c.connected) != connected {
		return nil
	}
	if c.pool == nil {
		var err error

		if c.nc != nil {
			err = c.nc.Close()
		}
		atomic.StoreInt32(&c.connected, disconnected)
		return err
	}
	return c.pool.closeConnection(c)
}

func (c *connection) expired() bool {
	now := time.Now()
	idleDeadline, ok := c.idleDeadline.Load().(time.Time)
	if ok && now.After(idleDeadline) {
		return true
	}

	if !c.lifetimeDeadline.IsZero() && now.After(c.lifetimeDeadline) {
		return true
	}

	return atomic.LoadInt32(&c.connected) == disconnected
}

func (c *connection) bumpIdleDeadline() {
	if c.idleTimeout > 0 {
		c.idleDeadline.Store(time.Now().Add(c.idleTimeout))
	}
}

// initConnection is an adapter used during connection initialization. It has the minimum
// functionality necessary to implement the driver.Connection interface, which is required to pass a
// *connection to a Handshaker.
type initConnection struct{ *connection }

var _ driver.Connection = initConnection{}

func (c initConnection) Description() description.Server {
	if c.connection == nil {
		return description.Server{}
	}
	return c.connection.desc
}
func (c initConnection) Close() error             { return nil }
func (c initConnection) ID() string               { return c.id }
func (c initConnection) Address() address.Address { return c.addr }
func (c initConnection) LocalAddress() address.Address {
	if c.connection == nil || c.nc == nil {
		return address.Address("0.0.0.0")
	}
	return address.Address(c.nc.LocalAddr().String())
}
func (c initConnection) WriteWireMessage(ctx context.Context, wm []byte) error {
	return c.writeWireMessage(ctx, wm)
}
func (c initConnection) ReadWireMessage(ctx context.Context, dst []byte) ([]byte, error) {
	return c.readWireMessage(ctx, dst)
}

// Connection implements the driver.Connection interface to allow reading and writing wire
// messages and the driver.Expirable interface to allow expiring.
type Connection struct {
	*connection
	s *Server

	mu sync.RWMutex
}

var _ driver.Connection = (*Connection)(nil)
var _ driver.Expirable = (*Connection)(nil)

// WriteWireMessage handles writing a wire message to the underlying connection.
func (c *Connection) WriteWireMessage(ctx context.Context, wm []byte) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.connection == nil {
		return ErrConnectionClosed
	}
	return c.writeWireMessage(ctx, wm)
}

// ReadWireMessage handles reading a wire message from the underlying connection. The dst parameter
// will be overwritten with the new wire message.
func (c *Connection) ReadWireMessage(ctx context.Context, dst []byte) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.connection == nil {
		return dst, ErrConnectionClosed
	}
	return c.readWireMessage(ctx, dst)
}

// CompressWireMessage handles compressing the provided wire message using the underlying
// connection's compressor. The dst parameter will be overwritten with the new wire message. If
// there is no compressor set on the underlying connection, then no compression will be performed.
func (c *Connection) CompressWireMessage(src, dst []byte) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.connection == nil {
		return dst, ErrConnectionClosed
	}
	if c.connection.compressor == wiremessage.CompressorNoOp {
		return append(dst, src...), nil
	}
	_, reqid, respto, origcode, rem, ok := wiremessage.ReadHeader(src)
	if !ok {
		return dst, errors.New("wiremessage is too short to compress, less than 16 bytes")
	}
	idx, dst := wiremessage.AppendHeaderStart(dst, reqid, respto, wiremessage.OpCompressed)
	dst = wiremessage.AppendCompressedOriginalOpCode(dst, origcode)
	dst = wiremessage.AppendCompressedUncompressedSize(dst, int32(len(rem)))
	dst = wiremessage.AppendCompressedCompressorID(dst, c.connection.compressor)
	switch c.connection.compressor {
	case wiremessage.CompressorSnappy:
		compressed := snappy.Encode(nil, rem)
		dst = wiremessage.AppendCompressedCompressedMessage(dst, compressed)
	case wiremessage.CompressorZLib:
		var b bytes.Buffer
		w, err := zlib.NewWriterLevel(&b, c.connection.zliblevel)
		if err != nil {
			return dst, err
		}
		_, err = w.Write(rem)
		if err != nil {
			return dst, err
		}
		err = w.Close()
		if err != nil {
			return dst, err
		}
		dst = wiremessage.AppendCompressedCompressedMessage(dst, b.Bytes())
	default:
		return dst, fmt.Errorf("unknown compressor ID %v", c.connection.compressor)
	}

	return bsoncore.UpdateLength(dst, idx, int32(len(dst[idx:]))), nil
}

// Description returns the server description of the server this connection is connected to.
func (c *Connection) Description() description.Server {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.connection == nil {
		return description.Server{}
	}
	return c.desc
}

// Close returns this connection to the connection pool. This method may not closeConnection the underlying
// socket.
func (c *Connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connection == nil {
		return nil
	}
	if c.s != nil {
		defer c.s.sem.Release(1)
	}
	err := c.pool.put(c.connection)
	c.connection = nil
	return err
}

// Expire closes this connection and will closeConnection the underlying socket.
func (c *Connection) Expire() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connection == nil {
		return nil
	}
	if c.s != nil {
		c.s.sem.Release(1)
	}
	err := c.close()
	c.connection = nil
	return err
}

// Alive returns if the connection is still alive.
func (c *Connection) Alive() bool {
	return c.connection != nil
}

// ID returns the ID of this connection.
func (c *Connection) ID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.connection == nil {
		return "<closed>"
	}
	return c.id
}

// Address returns the address of this connection.
func (c *Connection) Address() address.Address {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.connection == nil {
		return address.Address("0.0.0.0")
	}
	return c.addr
}

// LocalAddress returns the local address of the connection
func (c *Connection) LocalAddress() address.Address {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.connection == nil || c.nc == nil {
		return address.Address("0.0.0.0")
	}
	return address.Address(c.nc.LocalAddr().String())
}

var notMasterCodes = []int32{10107, 13435}
var recoveringCodes = []int32{11600, 11602, 13436, 189, 91}

func configureTLS(ctx context.Context, nc net.Conn, addr address.Address, config *tls.Config) (net.Conn, error) {
	if !config.InsecureSkipVerify {
		hostname := addr.String()
		colonPos := strings.LastIndex(hostname, ":")
		if colonPos == -1 {
			colonPos = len(hostname)
		}

		hostname = hostname[:colonPos]
		config.ServerName = hostname
	}

	client := tls.Client(nc, config)

	errChan := make(chan error, 1)
	go func() {
		errChan <- client.Handshake()
	}()

	select {
	case err := <-errChan:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, errors.New("server connection cancelled/timeout during TLS handshake")
	}
	return client, nil
}
