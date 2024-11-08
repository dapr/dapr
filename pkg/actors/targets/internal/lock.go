/*
Copyright 2024 The Dapr Authors
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

package internal

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/ring"
)

const defaultMaxStackDepth = 32

var ErrLockClosed = errors.New("actor lock is closed")

type LockOptions struct {
	ActorType  string
	Reentrancy config.ReentrancyConfig
}

type Lock struct {
	actorType string

	maxStackDepth     int
	reentrancyEnabled bool

	pending int

	lock      *fifo.Mutex
	reqCh     chan *req
	inflights *ring.Ring[*inflight]

	closeCh chan struct{}
	closed  atomic.Bool
	wg      sync.WaitGroup
}

type req struct {
	msg    *invokev1.InvokeMethodRequest
	respCh chan *resp
}

type resp struct {
	startCh chan struct{}
	cancel  context.CancelFunc
	err     error
}

type inflight struct {
	id      string
	depth   int
	startCh chan struct{}
}

func NewLock(opts LockOptions) *Lock {
	maxStackDepth := defaultMaxStackDepth
	if opts.Reentrancy.Enabled && opts.Reentrancy.MaxStackDepth != nil {
		maxStackDepth = *opts.Reentrancy.MaxStackDepth
	}

	l := &Lock{
		actorType:         opts.ActorType,
		reqCh:             make(chan *req),
		maxStackDepth:     maxStackDepth,
		reentrancyEnabled: opts.Reentrancy.Enabled,
		inflights:         ring.New[*inflight](8),
		lock:              fifo.New(),
		closeCh:           make(chan struct{}),
	}

	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		for {
			select {
			case <-l.closeCh:
				// Ensure all pending requests are handled before closing.
				l.LockRequest(nil)
				return

			case req := <-l.reqCh:
				resp := l.handleLock(req)

				l.wg.Add(1)
				go func() {
					defer l.wg.Done()
					select {
					case req.respCh <- resp:
					case <-l.closeCh:
					}
				}()
			}
		}
	}()

	return l
}

func (l *Lock) Lock() (context.CancelFunc, error) {
	return l.LockRequest(nil)
}

func (l *Lock) LockRequest(msg *invokev1.InvokeMethodRequest) (context.CancelFunc, error) {
	select {
	case <-l.closeCh:
		return nil, ErrLockClosed
	default:
	}

	diag.DefaultMonitoring.ReportActorPendingCalls(l.actorType, 1)
	defer diag.DefaultMonitoring.ReportActorPendingCalls(l.actorType, -1)

	req := &req{msg: msg, respCh: make(chan *resp)}

	select {
	case l.reqCh <- req:
	case <-l.closeCh:
		return nil, ErrLockClosed
	}

	resp := <-req.respCh
	if resp.err != nil {
		return nil, resp.err
	}

	select {
	case <-resp.startCh:
	case <-l.closeCh:
		return nil, ErrLockClosed
	}

	return resp.cancel, nil
}

func (l *Lock) handleLock(req *req) *resp {
	l.lock.Lock()
	defer l.lock.Unlock()

	inflight, err := l.handleInflight(req)
	if err != nil {
		return &resp{err: err}
	}

	cancel := func() {
		l.lock.Lock()
		defer l.lock.Unlock()

		inflight.depth--
		if inflight.depth == 0 {
			l.inflights = l.inflights.Next()

			if l.inflights.Value != nil {
				l.pending--

				if l.pending < l.inflights.Len()-8 {
					l.inflights.Move(l.inflights.Len() - 8).Unlink(8)
				}

				close(l.inflights.Value.startCh)
			}
		}
	}

	return &resp{startCh: inflight.startCh, cancel: cancel}
}

func (l *Lock) handleInflight(req *req) (*inflight, error) {
	id := l.idFromRequest(req.msg)

	if l.inflights.Value == nil {
		l.inflights.Value = newInflight(id)
		close(l.inflights.Value.startCh)
		return l.inflights.Value, nil
	}

	if l.reentrancyEnabled && l.inflights.Value.id == id {
		l.inflights.Value.depth++
		if l.inflights.Value.depth > l.maxStackDepth {
			return nil, messages.ErrActorMaxStackDepthExceeded
		}

		return l.inflights.Value, nil
	}

	flight := newInflight(id)
	l.pending++
	l.inflights.Move(l.pending).Value = flight
	l.checkExpand()

	return flight, nil
}

func newInflight(id string) *inflight {
	return &inflight{id: id, depth: 1, startCh: make(chan struct{})}
}

func (l *Lock) checkExpand() {
	if l.inflights.Len() == l.pending {
		exp := ring.New[*inflight](8)
		l.inflights.Move(l.pending).Link(exp)
	}
}

func (l *Lock) idFromRequest(req *invokev1.InvokeMethodRequest) string {
	id := uuid.New().String()
	if req == nil {
		return id
	}

	if md := req.Metadata()["Dapr-Reentrancy-Id"]; md != nil && len(md.GetValues()) > 0 {
		return md.GetValues()[0]
	}

	req.AddMetadata(map[string][]string{
		"Dapr-Reentrancy-Id": {id},
	})

	return id
}

func (l *Lock) Close() {
	if l.closed.CompareAndSwap(false, true) {
		close(l.closeCh)
		l.wg.Wait()
	}
}

func (l *Lock) CloseUntil(d time.Duration) {
	done := make(chan struct{})
	go func() {
		l.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
}
