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
//
// Multi-connection routing (rolling-restart semantic): Send always picks
// the most recently registered connection. Older connections do not
// receive any new Send traffic once a newer one registers, but their
// in-flight requests stay routed to them — Deliver looks up the response
// by request id on the connection that owns it, so the work an older
// stream had already accepted completes naturally. This matches the
// rolling-restart scenario where pod B opens its stream before pod A
// finishes draining: A wraps up its in-flight invocations while every
// new callback goes to B. See TestMultipleConns_OlderConnDrainsInFlight
// in callbackstream_test.go for the regression guard.
//
// Connection-set state (the active stream list and the latest registration
// config) is owned by a single goroutine that drains a kit/events/loop
// queue. Every state mutation goes through Handle, so there are no locks
// on the Manager itself. Per-connection request correlation uses
// sync.Map (lock-free) because Send and Deliver may interleave at high
// frequency and don't need full serialization.
package callbackstream

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.callbackstream")

// ErrNoConnection is returned when the actor transport tries to send a
// callback but no app has opened the stream.
var ErrNoConnection = errors.New("actor host is not connected")

// ErrDisconnected is returned when an in-flight callback fails because the
// app closed the stream.
var ErrDisconnected = errors.New("actor host disconnected before responding")

// loopFactory creates the per-Manager event loop. 64 is plenty for register
// and close events, which are rare relative to per-callback Send traffic
// (Send goes through atomic snapshots instead, see latest below).
var loopFactory = loop.New[event](64)

// Manager holds the set of active actor callback streams. Concurrent
// callers register/close streams and pick connections without locks: state
// mutations are serialized through an event loop, and the hot read path
// (Send / CurrentConfig / HasConnection) reads an atomic.Pointer snapshot
// maintained by that loop.
type Manager struct {
	loop loop.Interface[event]

	// latest is the loop's published view of "most recent registered
	// connection + config". Hot Send paths read it without any
	// synchronisation; the loop replaces it on Register/Close.
	latest atomic.Pointer[snapshot]

	connIDSeq atomic.Uint64

	// conns is the authoritative connection list. Only the loop reads or
	// writes it. Used to drive snapshot updates.
	conns []*Connection
}

// snapshot is an immutable view of Manager state, replaced atomically by
// the loop. Callers on the read path never mutate this struct.
type snapshot struct {
	latestConn *Connection
	cfg        *config.ApplicationConfig
}

// NewManager returns a new Manager with no active connections. The
// caller must invoke Run to drive the event loop.
func NewManager() *Manager {
	m := &Manager{}
	m.loop = loopFactory.NewLoop(m)
	m.latest.Store(&snapshot{})
	return m
}

// Run drives the Manager's event loop until ctx is cancelled. Mirrors the
// hotreload reconciler / scheduler stream-loop wiring: the loop runs in
// one goroutine, a sibling watcher closes it on ctx cancel.
func (m *Manager) Run(ctx context.Context) error {
	defer loopFactory.CacheLoop(m.loop)
	return concurrency.NewRunnerManager(
		m.loop.Run,
		func(ctx context.Context) error {
			<-ctx.Done()
			m.loop.Close(&eventStop{})
			return nil
		},
	).Run(ctx)
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
// Handlers that drain Outbox should select on this channel to exit.
func (c *Connection) Done() <-chan struct{} {
	return c.ctx.Done()
}

// Register installs a new connection. The caller is responsible for
// draining Outbox and calling Deliver for each inbound response. When the
// stream ends, the caller must invoke Close (or let the context cancel).
//
// Register blocks until the loop has published the new connection on the
// snapshot read path. This guarantees that callers which immediately
// invoke Send observe the connection — a property the test harness and
// the production API handler both rely on.
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

	done := make(chan struct{})
	m.loop.Enqueue(&eventRegister{conn: conn, done: done})
	<-done

	log.Debugf("Registered actor callback stream (connID=%d, entities=%v)", conn.ID, cfg.Entities)
	return conn
}

// Close terminates a connection: cancels its context, fails every pending
// request, and removes it from the manager. Safe to call multiple times.
//
// Close cancels the connection synchronously so concurrent Send callers
// observe the cancellation immediately. The loop only handles the
// bookkeeping (conns slice fixup, snapshot recompute).
func (m *Manager) Close(conn *Connection, cause error) {
	conn.once.Do(func() {
		if cause == nil {
			cause = ErrDisconnected
		}
		conn.cancel(cause)

		// Fail every pending request. Done outside the loop because each
		// in-flight Send is owned by its caller's goroutine and the result
		// channel is per-Send — no Manager-wide coordination needed.
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

		m.loop.Enqueue(&eventClose{conn: conn})
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
	return m.latest.Load().cfg
}

// HasConnection reports whether at least one app is currently streaming.
func (m *Manager) HasConnection() bool {
	return m.latest.Load().latestConn != nil
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
	conn := m.latest.Load().latestConn
	if conn == nil {
		return nil, ErrNoConnection
	}
	return conn.send(ctx, build)
}

func (c *Connection) send(
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

// event is the sealed set of messages handled by Manager's loop.
type event interface{ isCallbackEvent() }

type eventRegister struct {
	conn *Connection
	done chan<- struct{}
}
type (
	eventClose struct{ conn *Connection }
	eventStop  struct{}
)

func (*eventRegister) isCallbackEvent() {}
func (*eventClose) isCallbackEvent()    {}
func (*eventStop) isCallbackEvent()     {}

// Handle is the loop's serial event handler. It is the only goroutine that
// touches m.conns, so no locks are needed there. The atomic snapshot
// (m.latest) is updated here too — that's what every hot-path read sees.
func (m *Manager) Handle(_ context.Context, ev event) error {
	switch e := ev.(type) {
	case *eventRegister:
		m.conns = append(m.conns, e.conn)
		m.publishSnapshot()
		close(e.done)
	case *eventClose:
		for i, c := range m.conns {
			if c == e.conn {
				m.conns = append(m.conns[:i], m.conns[i+1:]...)
				break
			}
		}
		m.publishSnapshot()
	case *eventStop:
		// Loop exit signalled by Run's shutdown goroutine.
	}
	return nil
}

// publishSnapshot rebuilds the atomic snapshot to reflect the current
// loop-owned conns slice. Always called from inside Handle.
func (m *Manager) publishSnapshot() {
	if len(m.conns) == 0 {
		m.latest.Store(&snapshot{})
		return
	}
	last := m.conns[len(m.conns)-1]
	m.latest.Store(&snapshot{latestConn: last, cfg: last.Config})
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
