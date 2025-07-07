/*
Copyright 2025 The Dapr Authors
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

	"github.com/google/uuid"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/ring"
)

const headerReentrancyID = "Dapr-Reentrancy-Id"

var (
	ErrLockClosed = errors.New("actor lock is closed")

	lockCache = &sync.Pool{
		New: func() any {
			var l *Lock
			return l
		},
	}
)

type Options struct {
	ActorType   string
	ConfigStore *reentrancystore.Store
}

type inflight struct {
	id      string
	depth   int
	startCh chan struct{}
}

type Lock struct {
	reentrancyEnabled bool
	maxStackDepth     int
	actorType         string

	inflights *ring.Buffered[inflight]
	lock      chan struct{}
	closeCh   chan struct{}
	wg        sync.WaitGroup
}

func New(opts Options) *Lock {
	var reentrancyEnabled bool
	maxStackDepth := api.DefaultReentrancyStackLimit

	ree, ok := opts.ConfigStore.Load(opts.ActorType)
	if ok {
		reentrancyEnabled = ree.Enabled
		if ree.MaxStackDepth != nil {
			maxStackDepth = *ree.MaxStackDepth
		}
	}

	l := lockCache.Get().(*Lock)
	if l == nil {
		return &Lock{
			actorType:         opts.ActorType,
			reentrancyEnabled: reentrancyEnabled,
			maxStackDepth:     maxStackDepth,
			inflights:         ring.NewBuffered[inflight](2, 8),
			lock:              make(chan struct{}, 1),
			closeCh:           make(chan struct{}),
		}
	}

	l.actorType = opts.ActorType
	l.maxStackDepth = maxStackDepth
	l.reentrancyEnabled = reentrancyEnabled
	l.closeCh = make(chan struct{})
	for range l.inflights.Len() {
		l.inflights.RemoveFront()
	}

	return l
}

func (l *Lock) Lock(ctx context.Context) (context.Context, context.CancelFunc, error) {
	return l.LockRequest(ctx, nil)
}

func (l *Lock) LockRequest(ctx context.Context, msg *internalv1pb.InternalInvokeRequest) (context.Context, context.CancelFunc, error) {
	diag.DefaultMonitoring.ReportActorPendingCalls(l.actorType, 1)
	defer diag.DefaultMonitoring.ReportActorPendingCalls(l.actorType, -1)

	select {
	case l.lock <- struct{}{}:
	case <-l.closeCh:
		return nil, nil, ErrLockClosed
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	flight, err := l.handleLock(ctx, msg)
	<-l.lock
	if err != nil {
		return nil, nil, err
	}

	doneCh := make(chan struct{})
	release := func() {
		close(doneCh)
		l.lock <- struct{}{}
		defer func() { <-l.lock }()

		flight.depth--
		if flight.depth == 0 {
			if v := l.inflights.RemoveFront(); v != nil {
				close(v.startCh)
			}
		}
	}

	select {
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	case <-l.closeCh:
		release()
		return nil, nil, ErrLockClosed
	case <-flight.startCh:
		cctx, cancel := context.WithCancelCause(ctx)

		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			select {
			case <-doneCh:
			case <-l.closeCh:
			}
			cancel(ErrLockClosed)
		}()

		return cctx, release, nil
	}
}

func (l *Lock) Close(ctx context.Context) {
	l.Lock(ctx)
	close(l.closeCh)
	l.wg.Wait()
	lockCache.Put(l)
}

func (l *Lock) handleLock(ctx context.Context, msg *internalv1pb.InternalInvokeRequest) (*inflight, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	id, ok := l.idFromRequest(msg)

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

func (l *Lock) idFromRequest(req *internalv1pb.InternalInvokeRequest) (string, bool) {
	if !l.reentrancyEnabled || req == nil {
		return uuid.New().String(), false
	}

	if md := req.GetMetadata()[headerReentrancyID]; md != nil && len(md.GetValues()) > 0 {
		return md.GetValues()[0], true
	}

	id := uuid.New().String()
	if req.Metadata == nil {
		req.Metadata = make(map[string]*internalv1pb.ListStringValue)
	}
	req.Metadata[headerReentrancyID] = &internalv1pb.ListStringValue{
		Values: []string{id},
	}

	return id, true
}

func newInflight(id string) *inflight {
	return &inflight{id: id, depth: 1, startCh: make(chan struct{})}
}
