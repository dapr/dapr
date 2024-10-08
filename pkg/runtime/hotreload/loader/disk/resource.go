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
	sourceBatcher *batcher.Batcher[int, struct{}]
	streamBatcher *batcher.Batcher[int, struct{}]
	store         store.Store[T]
	diskLoader    internalloader.Loader[T]

	lock          sync.RWMutex
	currentResult *differ.Result[T]

	wg      sync.WaitGroup
	running chan struct{}
	closeCh chan struct{}
	closed  atomic.Bool
}

type resourceOptions[T differ.Resource] struct {
	loader  internalloader.Loader[T]
	store   store.Store[T]
	batcher *batcher.Batcher[int, struct{}]
}

func newResource[T differ.Resource](opts resourceOptions[T]) *resource[T] {
	return &resource[T]{
		sourceBatcher: opts.batcher,
		store:         opts.store,
		diskLoader:    opts.loader,
		streamBatcher: batcher.New[int, struct{}](0),
		running:       make(chan struct{}),
		closeCh:       make(chan struct{}),
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
	r.streamBatcher.Subscribe(ctx, batchCh)

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

func (r *resource[T]) run(ctx context.Context) error {
	defer func() {
		if r.closed.CompareAndSwap(false, true) {
			close(r.closeCh)
		}
		r.streamBatcher.Close()
		r.wg.Wait()
	}()

	updateCh := make(chan struct{})
	r.sourceBatcher.Subscribe(ctx, updateCh)
	close(r.running)

	var i int
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.closeCh:
			return nil
		case <-updateCh:
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
		r.streamBatcher.Batch(i, struct{}{})
	}
}
