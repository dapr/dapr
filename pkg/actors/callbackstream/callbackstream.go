/*
Copyright 2026 The Dapr Authors
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

// Package callbackstream coordinates the app-initiated actor callback
// stream. The Dapr gRPC API handler registers each incoming stream with the
// Manager; the actor transport sends callback requests through the Manager
// and awaits correlated responses.
//
// The design mirrors the pubsub SubscribeTopicEventsAlpha1 stream: the app
// is the gRPC client, disconnect fails in-flight work immediately, and
// multiple concurrent connections from the same app are permitted.
package callbackstream

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.callbackstream")

// ErrNoConnection is returned when the actor transport tries to send a
// callback but no app has opened the stream.
var ErrNoConnection = errors.New("actor host is not connected")

// ErrDisconnected is returned when an in-flight callback fails because the
// app closed the stream.
var ErrDisconnected = errors.New("actor host disconnected before responding")

// Manager holds the set of active actor callback streams. It is safe for
// concurrent use by the gRPC server (which registers streams) and the
// actor transport (which sends callbacks).
type Manager struct {
	mu              sync.RWMutex
	conns           []*Connection
	registrationCfg *config.ApplicationConfig

	connIDSeq atomic.Uint64
}

// NewManager returns a new Manager with no active connections.
func NewManager() *Manager {
	return &Manager{}
}

// Connection represents a single live actor host stream. It exposes a send
// channel for outbound requests and a pending map for correlating
// responses. The owning handler is responsible for draining Outbox and
// routing incoming responses back through Deliver.
type Connection struct {
	ID     uint64
	Config *config.ApplicationConfig

	// Outbox is drained by the gRPC handler's send loop. It is intentionally
	// never closed (concurrent senders would panic on send-on-closed).
	// Consumers should exit on conn.Done() instead of ranging on this chan.
	Outbox chan *runtimev1pb.SubscribeActorEventsResponseAlpha1

	pending sync.Map // string (request id) → chan pendingResult

	ctx    context.Context
	cancel context.CancelCauseFunc
	idSeq  atomic.Uint64
	once   sync.Once
}

// pendingResult carries the app's response back to the caller that sent the
// request. Only one of the two fields is populated.
type pendingResult struct {
	msg *runtimev1pb.SubscribeActorEventsRequestAlpha1
	err error
}

// Done returns a channel that is closed when the connection is terminated.
// Handlers that drain Outbox should select on this channel to exit, since
// Outbox itself is never closed (avoids a send-on-closed-channel panic when
// a concurrent Send caller is mid-flight).
func (c *Connection) Done() <-chan struct{} {
	return c.ctx.Done()
}

// Register installs a new connection. The caller is responsible for draining
// Outbox and calling Deliver for each inbound response. When the stream ends,
// the caller must invoke Close (or let the context cancel).
func (m *Manager) Register(ctx context.Context, cfg *config.ApplicationConfig) *Connection {
	cctx, cancel := context.WithCancelCause(ctx)
	conn := &Connection{
		ID:     m.connIDSeq.Add(1),
		Config: cfg,
		// Buffered to absorb short bursts; the handler's send loop drains it.
		Outbox: make(chan *runtimev1pb.SubscribeActorEventsResponseAlpha1, 128),
		ctx:    cctx,
		cancel: cancel,
	}

	m.mu.Lock()
	m.conns = append(m.conns, conn)
	m.registrationCfg = cfg
	m.mu.Unlock()

	log.Debugf("Registered actor callback stream (connID=%d, entities=%v)", conn.ID, cfg.Entities)
	return conn
}

// Close terminates a connection: cancels its context, fails every pending
// request, and removes it from the manager. Safe to call multiple times.
func (m *Manager) Close(conn *Connection, cause error) {
	conn.once.Do(func() {
		if cause == nil {
			cause = ErrDisconnected
		}
		conn.cancel(cause)

		// Fail every pending request.
		conn.pending.Range(func(k, v any) bool {
			ch, ok := v.(chan pendingResult)
			if ok {
				select {
				case ch <- pendingResult{err: cause}:
				default:
				}
			}
			conn.pending.Delete(k)
			return true
		})

		// Do NOT close conn.Outbox: concurrent Send callers may still be
		// executing `c.Outbox <- msg`, and a send on a closed channel
		// panics. Termination is signalled via conn.ctx — Send selects on
		// c.ctx.Done() and the API handler's drain loop selects on
		// conn.Done().

		m.mu.Lock()
		for i, c := range m.conns {
			if c == conn {
				m.conns = append(m.conns[:i], m.conns[i+1:]...)
				break
			}
		}
		if len(m.conns) == 0 {
			m.registrationCfg = nil
		}
		m.mu.Unlock()

		log.Debugf("Closed actor callback stream (connID=%d): %v", conn.ID, cause)
	})
}

// Deliver routes an app → daprd message to the pending caller that owns the
// correlated request id. Called by the gRPC handler's recv loop for every
// inbound message. The initial_request variant is consumed by the handler
// itself and never reaches Deliver.
func (c *Connection) Deliver(msg *runtimev1pb.SubscribeActorEventsRequestAlpha1) {
	id, ok := requestID(msg)
	if !ok {
		log.Warnf("Dropping actor callback response without correlation id: %T", msg.GetRequestType())
		return
	}
	v, loaded := c.pending.LoadAndDelete(id)
	if !loaded {
		log.Warnf("Dropping actor callback response with unknown id=%s", id)
		return
	}
	ch, _ := v.(chan pendingResult)
	select {
	case ch <- pendingResult{msg: msg}:
	default:
		// No receiver — caller gave up. Drop silently.
	}
}

// CurrentConfig returns the most recently registered ApplicationConfig,
// or nil if no stream has registered yet. It never blocks — a pure
// snapshot of state. Callers that need to react to stream arrival should
// drive the lifecycle imperatively from the API handler (see
// pkg/api/grpc/actorcallbacks.go), mirroring how
// pkg/api/grpc/subscribe.go reacts to SubscribeTopicEventsAlpha1.
func (m *Manager) CurrentConfig() *config.ApplicationConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registrationCfg
}

// HasConnection reports whether at least one app is currently streaming.
func (m *Manager) HasConnection() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.conns) > 0
}

// Send issues a callback request over an available connection and blocks
// until the correlated response arrives, ctx is cancelled, or the
// connection closes. The caller supplies a builder that stamps the
// request id onto the concrete oneof payload — this keeps id generation
// under the Connection's control while leaving payload shape to callers.
func (m *Manager) Send(
	ctx context.Context,
	build func(id string) *runtimev1pb.SubscribeActorEventsResponseAlpha1,
) (*runtimev1pb.SubscribeActorEventsRequestAlpha1, error) {
	conn := m.pickConnection()
	if conn == nil {
		return nil, ErrNoConnection
	}
	return conn.sendLocked(ctx, build)
}

// pickConnection returns the most recently registered connection, or nil.
// Using LIFO keeps semantics predictable when duplicate connections exist
// (usually during a restart overlap).
func (m *Manager) pickConnection() *Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.conns) == 0 {
		return nil
	}
	return m.conns[len(m.conns)-1]
}

func (c *Connection) sendLocked(
	ctx context.Context,
	build func(id string) *runtimev1pb.SubscribeActorEventsResponseAlpha1,
) (*runtimev1pb.SubscribeActorEventsRequestAlpha1, error) {
	id := strconv.FormatUint(c.idSeq.Add(1), 10)
	resCh := make(chan pendingResult, 1)
	c.pending.Store(id, resCh)

	msg := build(id)

	select {
	case c.Outbox <- msg:
	case <-ctx.Done():
		c.pending.Delete(id)
		return nil, ctx.Err()
	case <-c.ctx.Done():
		c.pending.Delete(id)
		return nil, context.Cause(c.ctx)
	}

	select {
	case r := <-resCh:
		return r.msg, r.err
	case <-ctx.Done():
		c.pending.Delete(id)
		return nil, ctx.Err()
	case <-c.ctx.Done():
		c.pending.Delete(id)
		return nil, context.Cause(c.ctx)
	}
}

// requestID extracts the correlation id from any app → daprd oneof variant
// that carries one.
func requestID(msg *runtimev1pb.SubscribeActorEventsRequestAlpha1) (string, bool) {
	switch v := msg.GetRequestType().(type) {
	case *runtimev1pb.SubscribeActorEventsRequestAlpha1_InvokeResponse:
		return v.InvokeResponse.GetId(), true
	case *runtimev1pb.SubscribeActorEventsRequestAlpha1_ReminderResponse:
		return v.ReminderResponse.GetId(), true
	case *runtimev1pb.SubscribeActorEventsRequestAlpha1_TimerResponse:
		return v.TimerResponse.GetId(), true
	case *runtimev1pb.SubscribeActorEventsRequestAlpha1_DeactivateResponse:
		return v.DeactivateResponse.GetId(), true
	case *runtimev1pb.SubscribeActorEventsRequestAlpha1_RequestFailed:
		return v.RequestFailed.GetId(), true
	}
	return "", false
}
