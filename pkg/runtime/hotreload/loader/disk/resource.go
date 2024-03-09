/*
Copyright 2023 The Dapr Authors
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

package disk

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	internalloader "github.com/dapr/dapr/pkg/internal/loader"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
	"github.com/dapr/kit/events/batcher"
)

// resource is a generic implementation of a disk resource loader. resource
// will watch and load resources from disk.
type resource[T differ.Resource] struct {
	batcher    *batcher.Batcher[int]
	store      store.Store[T]
	diskLoader internalloader.Loader[T]
	updateCh   <-chan struct{}

	lock          sync.RWMutex
	currentResult *differ.Result[T]

	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

func newResource[T differ.Resource](loader internalloader.Loader[T], store store.Store[T], updateCh <-chan struct{}) *resource[T] {
	return &resource[T]{
		batcher:    batcher.New[int](0),
		store:      store,
		diskLoader: loader,
		updateCh:   updateCh,
		closeCh:    make(chan struct{}),
	}
}

// List returns the current list of resources loaded from disk.
func (r *resource[T]) List(ctx context.Context) (*differ.LocalRemoteResources[T], error) {
	remotes, err := r.diskLoader.Load(ctx)
	if err != nil {
		return nil, err
	}

	return &differ.LocalRemoteResources[T]{
		Local:  r.store.List(),
		Remote: remotes,
	}, nil
}

// Stream returns a channel of events that will be sent when a resource is
// created, updated, or deleted.
func (r *resource[T]) Stream(ctx context.Context) (*loader.StreamConn[T], error) {
	conn := &loader.StreamConn[T]{
		EventCh:     make(chan *loader.Event[T]),
		ReconcileCh: make(chan struct{}),
	}

	batchCh := make(chan struct{})
	r.batcher.Subscribe(batchCh)

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.closeCh:
				return
			case <-batchCh:
				r.triggerDiff(ctx, conn)
			}
		}
	}()

	return conn, nil
}

func (r *resource[T]) triggerDiff(ctx context.Context, conn *loader.StreamConn[T]) {
	r.lock.RLock()
	defer r.lock.RUnlock()

	// Each group is a list of resources which have been created, updated, or
	// deleted. It is critical that we send the events in the order of deleted,
	// updated, and created. This ensures we close before initing a resource
	// with the same name.
	for _, group := range []struct {
		resources []T
		eventType operatorpb.ResourceEventType
	}{
		{r.currentResult.Deleted, operatorpb.ResourceEventType_DELETED},
		{r.currentResult.Updated, operatorpb.ResourceEventType_UPDATED},
		{r.currentResult.Created, operatorpb.ResourceEventType_CREATED},
	} {
		for _, resource := range group.resources {
			select {
			case conn.EventCh <- &loader.Event[T]{
				Resource: resource,
				Type:     group.eventType,
			}:
			case <-r.closeCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}
}

func (r *resource[T]) start(ctx context.Context) error {
	defer func() {
		if r.closed.CompareAndSwap(false, true) {
			close(r.closeCh)
		}
		r.batcher.Close()
		r.wg.Wait()
	}()

	var i int
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.closeCh:
			return nil
		case <-r.updateCh:
		}

		// List the resources which exist locally (those loaded already), and those
		// which reside as in a resource file on disk.
		resources, err := r.List(ctx)
		if err != nil {
			return fmt.Errorf("failed to load resources from disk: %s", err)
		}

		// Reconcile the differences between what we have loaded locally, and what
		// exists on disk.k
		result := differ.Diff(resources)

		r.lock.Lock()
		r.currentResult = result
		r.lock.Unlock()

		if result == nil {
			continue
		}

		// Use a separate: index every batch to prevent deduplicates of separate
		// file updates happening at the same time.
		i++
		r.batcher.Batch(i)
	}
}
