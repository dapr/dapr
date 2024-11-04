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

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/ring"
)

var ErrLockClosed = errors.New("actor lock is closed")

type LockOptions struct {
	ActorType         string
	ReentrancyEnabled bool
	MaxStackDepth     int
}

// Lock is a fifo Mutex which respects reentrancy and stack depth.
type Lock struct {
	actorType string

	maxStackDepth     int
	reentrancyEnabled bool

	lock      *fifo.Mutex
	reqCh     chan *req
	inflights *ring.Buffered[inflight]

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
	maxStackDepth := opts.MaxStackDepth
	if opts.ReentrancyEnabled && opts.MaxStackDepth < 1 {
		maxStackDepth = 1
	}

	l := &Lock{
		actorType:         opts.ActorType,
		reqCh:             make(chan *req),
		maxStackDepth:     maxStackDepth,
		reentrancyEnabled: opts.ReentrancyEnabled,
		inflights:         ring.NewBuffered[inflight](1, 64),
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
			if v := l.inflights.RemoveFront(); v != nil {
				close(v.startCh)
			}
		}
	}

	return &resp{startCh: inflight.startCh, cancel: cancel}
}

func (l *Lock) handleInflight(req *req) (*inflight, error) {
	id, ok := l.idFromRequest(req.msg)

	// If this is:
	// 1. a new request which is not accociated with any inflight (the usual base
	//   case)
	// 2. reentry is not enabled
	// 3. there is no current inflight requests
	// then create a new inflight request and append to the back of the ring
	// (queue).
	if !ok || !l.reentrancyEnabled || l.inflights.Len() == 0 {
		flight := newInflight(id)
		if l.inflights.Front() == nil {
			close(flight.startCh)
		}
		l.inflights.AppendBack(flight)
		return flight, nil
	}

	// Range over the ring to find the inflight request with the same id. If found,
	// increment the depth and check if it exceeds the max stack depth.
	var flight *inflight
	var err error
	l.inflights.Range(func(v *inflight) bool {
		if v.id != id {
			return true
		}

		flight = v
		v.depth++
		if v.depth > l.maxStackDepth {
			err = messages.ErrActorMaxStackDepthExceeded
		}

		return false
	})
	if err != nil {
		return nil, err
	}

	// If we did not find the inflight request with the same id, create a new one
	// and append to the back of the ring.
	if flight == nil {
		flight = newInflight(id)
		l.inflights.AppendBack(flight)
	}

	return flight, nil
}

func newInflight(id string) *inflight {
	return &inflight{id: id, depth: 1, startCh: make(chan struct{})}
}

func (l *Lock) idFromRequest(req *invokev1.InvokeMethodRequest) (string, bool) {
	if req == nil {
		return uuid.New().String(), false
	}

	if md := req.Metadata()["Dapr-Reentrancy-Id"]; md != nil && len(md.GetValues()) > 0 {
		return md.GetValues()[0], true
	}

	id := uuid.New().String()
	req.AddMetadata(map[string][]string{
		"Dapr-Reentrancy-Id": {id},
	})

	return id, true
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
