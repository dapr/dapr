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

package lock

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dapr/kit/concurrency/fifo"
)

var errLockClosed = errors.New("placement lock closed")

type hold struct {
	writeLock bool
	rctx      context.Context
	respCh    chan *holdresp
}

type holdresp struct {
	rctx   context.Context
	cancel context.CancelFunc
}

type Lock struct {
	ch chan *hold

	lock chan struct{}

	wg          sync.WaitGroup
	rcancelLock sync.Mutex
	rcancelx    uint64
	rcancels    map[uint64]context.CancelFunc

	closeCh      chan struct{}
	shutdownLock *fifo.Mutex
}

func New() *Lock {
	return &Lock{
		lock:         make(chan struct{}, 1),
		ch:           make(chan *hold, 1),
		rcancels:     make(map[uint64]context.CancelFunc),
		closeCh:      make(chan struct{}),
		shutdownLock: fifo.New(),
	}
}

func (l *Lock) Run(ctx context.Context) {
	defer func() {
		l.rcancelLock.Lock()
		defer l.rcancelLock.Unlock()

		for _, cancel := range l.rcancels {
			go cancel()
		}
	}()

	go func() {
		<-ctx.Done()
		close(l.closeCh)
	}()

	for {
		select {
		case <-l.closeCh:
			return
		case h := <-l.ch:
			l.handleHold(h)
		}
	}
}

func (l *Lock) handleHold(h *hold) {
	l.lock <- struct{}{}
	l.rcancelLock.Lock()

	if h.writeLock {
		for _, cancel := range l.rcancels {
			go cancel()
		}
		l.rcancelx = 0
		l.rcancelLock.Unlock()
		l.wg.Wait()

		h.respCh <- &holdresp{cancel: func() { <-l.lock }}

		return
	}

	l.wg.Add(1)
	var done bool
	doneCh := make(chan bool)
	rctx, cancel := context.WithCancel(h.rctx)
	i := l.rcancelx

	rcancel := func() {
		l.rcancelLock.Lock()
		if !done {
			close(doneCh)
			cancel()
			delete(l.rcancels, i)
			l.wg.Done()
			done = true
		}
		l.rcancelLock.Unlock()
	}

	rcancelGrace := func() {
		select {
		case <-time.After(2 * time.Second):
			rcancel()
		case <-doneCh:
		}
	}

	l.rcancels[i] = rcancelGrace
	l.rcancelx++

	l.rcancelLock.Unlock()

	h.respCh <- &holdresp{rctx: rctx, cancel: rcancel}

	<-l.lock
}

func (l *Lock) Lock() context.CancelFunc {
	h := hold{
		writeLock: true,
		respCh:    make(chan *holdresp, 1),
	}

	select {
	case <-l.closeCh:
		l.shutdownLock.Lock()
		return l.shutdownLock.Unlock
	case l.ch <- &h:
	}

	select {
	case <-l.closeCh:
		l.shutdownLock.Lock()
		return l.shutdownLock.Unlock
	case resp := <-h.respCh:
		return resp.cancel
	}
}

func (l *Lock) RLock(ctx context.Context) (context.Context, context.CancelFunc, error) {
	h := hold{
		writeLock: false,
		rctx:      ctx,
		respCh:    make(chan *holdresp, 1),
	}

	select {
	case <-l.closeCh:
		return nil, nil, errLockClosed
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case l.ch <- &h:
	}

	select {
	case <-l.closeCh:
		return nil, nil, errLockClosed
	case resp := <-h.respCh:
		return resp.rctx, resp.cancel, nil
	}
}
