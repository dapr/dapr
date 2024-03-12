/*
Copyright 2022 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package manager

import (
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	kclock "k8s.io/utils/clock"

	"github.com/dapr/kit/ptr"
)

// Real-time clock (wrapper around time.Time) to allow mocking
var clock kclock.Clock = &kclock.RealClock{}

// Maximum number of concurrent streams in a single gRPC connection
// This is the default value used by gRPC servers and clients
const grpcMaxConcurrentStreams = 100

// ConnectionPool holds a pool of connections to the same address.
type ConnectionPool struct {
	// Max connection idle time
	maxConnIdle time.Duration

	// Minimum number of active connections to keep
	minActiveConns int

	connections []*connectionPoolConnection
	lock        sync.RWMutex
}

// NewConnectionPool creates a new ConnectionPool object.
func NewConnectionPool(maxConnIdle time.Duration, minActiveConns int) *ConnectionPool {
	return &ConnectionPool{
		maxConnIdle:    maxConnIdle,
		minActiveConns: minActiveConns,
	}
}

// Get takes a connection from the pool or, if no connection exists, creates a new one using createFn, then stores it and returns it.
func (p *ConnectionPool) Get(createFn func() (grpc.ClientConnInterface, error)) (conn grpc.ClientConnInterface, err error) {
	// Try getting an existing connection
	p.lock.RLock()
	conn = p.doShare()
	p.lock.RUnlock()
	if conn != nil {
		return conn, nil
	}

	// Couldn't find one, so acquire a write lock and create one
	p.lock.Lock()
	defer p.lock.Unlock()

	// Before we create a new one, make sure that no other goroutine has created one in the meanwhile
	conn = p.doShare()
	if conn != nil {
		return conn, nil
	}

	// Create a connection using createFn
	newConn, err := createFn()
	if err != nil {
		return nil, err
	}
	p.doRegister(newConn)

	// Share the newly-created connection
	// Invoke doShare here so the reference count is increased
	conn = p.doShare()
	return conn, nil
}

// Register a new connection.
func (p *ConnectionPool) Register(conn grpc.ClientConnInterface) {
	p.lock.Lock()
	p.doRegister(conn)
	p.lock.Unlock()
}

// doRegister perfoms the actual registration.
// Needs to be wrapped by a write lock.
func (p *ConnectionPool) doRegister(conn grpc.ClientConnInterface) {
	// Expand the slice if needed
	l := len(p.connections)
	if l > 0 {
		tmp := p.connections
		p.connections = make([]*connectionPoolConnection, l+1)
		copy(p.connections, tmp)
	} else {
		// Create a slice with length = 1 as most addresses will only have 1 active connection at a given time
		p.connections = make([]*connectionPoolConnection, 1)
	}
	store := &connectionPoolConnection{
		conn: conn,
	}
	store.MarkIdle()
	p.connections[l] = store
}

// Share takes a connection from the pool and increments its reference count.
// The result can be nil if no available connection can be found.
func (p *ConnectionPool) Share() grpc.ClientConnInterface {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.doShare()
}

// doShare performs the sharing of the connection from the pool, incrementing its reference count.
// This needs to be wrapped in a (read/write) lock.
func (p *ConnectionPool) doShare() grpc.ClientConnInterface {
	// If there's more than 1 connection, grab the first one whose reference count is less or equal than 100 (grpcMaxConcurrentStreams)
	for i := 0; i < len(p.connections); i++ {
		// Check if the connection is still valid first
		// First we check if the referenceCount is 0, and then we check if the connection has expired
		// This should be safe for concurrent use
		if atomic.LoadInt32(&p.connections[i].referenceCount) == 0 && p.connections[i].Expired(p.maxConnIdle) {
			continue
		}

		// Increment the reference counter to signal that we're using the connection
		count := atomic.AddInt32(&p.connections[i].referenceCount, 1)

		// If the reference count is less (or equal) than 100, we can use this connection
		if count <= grpcMaxConcurrentStreams {
			return p.connections[i].conn
		}

		atomic.AddInt32(&p.connections[i].referenceCount, -1)
	}

	// Could not find a connection with less than 100 active streams, so return nil
	return nil
}

// Release is called when the method has finished using the connection.
// This decrements the reference counter for the connection.
func (p *ConnectionPool) Release(conn grpc.ClientConnInterface) {
	// Read lock is enough here
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, el := range p.connections {
		if el.conn == conn {
			count := atomic.AddInt32(&el.referenceCount, -1)
			if count <= 0 {
				el.MarkIdle()
			}
			return
		}
	}
}

// Destroy a connection, forcibly removing it from the pool
func (p *ConnectionPool) Destroy(conn grpc.ClientConnInterface) {
	// Need a write lock as we modify the slice
	p.lock.Lock()
	defer p.lock.Unlock()

	n := 0
	for i := 0; i < len(p.connections); i++ {
		el := p.connections[i]
		if el.conn == conn {
			// Close and filter out the connection
			if closer, ok := el.conn.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
			continue
		}

		p.connections[n] = el
		n++
	}
	p.connections = p.connections[:n]
}

// DestroyAll closes all connections in the poll.
func (p *ConnectionPool) DestroyAll() {
	p.lock.Lock()
	defer p.lock.Unlock()

	for i := 0; i < len(p.connections); i++ {
		if closer, ok := p.connections[i].conn.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	p.connections = p.connections[:0]
}

// Purge connections that have been idle for longer than maxConnIdle.
// Note that this method should not be called by multiple goroutines at the same time.
func (p *ConnectionPool) Purge() {
	// Need a write lock as we modify the slice
	p.lock.Lock()
	defer p.lock.Unlock()

	n := 0
	for i := 0; i < len(p.connections); i++ {
		el := p.connections[i]

		// If the connection has no use and the last usage was more than maxConnIdle ago, then close it and filter it out
		// Ok to load the reference count non-atomically because we have a write lock
		// Also, don't remove connections until we reach minActiveConns
		if i >= p.minActiveConns && el.referenceCount <= 0 && el.Expired(p.maxConnIdle) {
			if closer, ok := el.conn.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
			continue
		}

		p.connections[n] = el
		n++
	}
	p.connections = p.connections[:n]
}

// connectionPoolConnection is used by connectionPool to store the connection.
type connectionPoolConnection struct {
	conn           grpc.ClientConnInterface
	referenceCount int32
	idleSince      atomic.Pointer[time.Time]
}

// Expired returns true if the connection has Expired and should not be used.
func (pic *connectionPoolConnection) Expired(maxConnIdle time.Duration) bool {
	idle := pic.idleSince.Load()
	return idle != nil && clock.Since(*idle) > maxConnIdle
}

// MarkIdle sets the current time as when the connection became idle.
func (pic *connectionPoolConnection) MarkIdle() {
	pic.idleSince.Store(ptr.Of(clock.Now()))
}

// RemoteConnectionPool is used to hold connections to remote addresses.
type RemoteConnectionPool struct {
	pool sync.Map
}

// NewRemoteConnectionPool creates a new RemoteConnectionPool object.
func NewRemoteConnectionPool() *RemoteConnectionPool {
	return &RemoteConnectionPool{
		pool: sync.Map{},
	}
}

// Get takes a connection from the pool or, if no connection exists, creates a new one using createFn, then stores it and returns it.
func (p *RemoteConnectionPool) Get(address string, createFn func() (grpc.ClientConnInterface, error)) (conn grpc.ClientConnInterface, err error) {
	item := p.loadOrStoreItem(address)
	return item.Get(createFn)
}

// Register a new connection.
func (p *RemoteConnectionPool) Register(address string, conn grpc.ClientConnInterface) {
	item := p.loadOrStoreItem(address)
	item.Register(conn)
}

// Share takes a connection from the pool and increments its reference count.
// The result can be nil if no available connection can be found.
func (p *RemoteConnectionPool) Share(address string) grpc.ClientConnInterface {
	item, ok := p.pool.Load(address)
	if !ok {
		return nil
	}
	return item.(*ConnectionPool).Share()
}

// Release is called when the method has finished using the connection.
// This decrements the reference counter for the connection.
func (p *RemoteConnectionPool) Release(address string, conn grpc.ClientConnInterface) {
	item, ok := p.pool.Load(address)
	if !ok {
		return
	}
	item.(*ConnectionPool).Release(conn)
}

// Destroy a connection, forcibly removing ti from the pool
func (p *RemoteConnectionPool) Destroy(address string, conn grpc.ClientConnInterface) {
	item, ok := p.pool.Load(address)
	if !ok {
		return
	}
	item.(*ConnectionPool).Destroy(conn)
}

// Purge connections that have been idle for longer than maxConnIdle.
// Note that this method should not be called by multiple goroutines at the same time.
func (p *RemoteConnectionPool) Purge() {
	p.pool.Range(func(_ any, item any) bool {
		item.(*ConnectionPool).Purge()
		return true
	})
}

func (p *RemoteConnectionPool) loadOrStoreItem(address string) *ConnectionPool {
	item, ok := p.pool.Load(address)
	if !ok {
		// Use LoadOrStore here in case another goroutine is in the exact same spot
		item, _ = p.pool.LoadOrStore(address, NewConnectionPool(maxConnIdle, 0))
	}
	return item.(*ConnectionPool)
}
