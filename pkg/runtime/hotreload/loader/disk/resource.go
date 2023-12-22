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
	"sync"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/components"
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
	diskLoader components.ManifestLoader[T]

	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

func newResource[T differ.Resource](opts Options, batcher *batcher.Batcher[int], store store.Store[T]) *resource[T] {
	return &resource[T]{
		batcher:    batcher,
		store:      store,
		diskLoader: components.NewDiskManifestLoader[T](opts.Dirs...),
		closeCh:    make(chan struct{}),
	}
}

// List returns the current list of resources loaded from disk.
func (r *resource[T]) List(ctx context.Context) (*differ.LocalRemoteResources[T], error) {
	remotes, err := r.diskLoader.Load()
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
func (r *resource[T]) Stream(ctx context.Context) (<-chan *loader.Event[T], error) {
	batchCh := make(chan struct{})
	r.batcher.Subscribe(batchCh)

	eventCh := make(chan *loader.Event[T])
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.streamResources(ctx, batchCh, eventCh)
	}()

	return eventCh, nil
}

func (r *resource[T]) close() error {
	defer r.wg.Wait()
	if r.closed.CompareAndSwap(false, true) {
		close(r.closeCh)
	}

	r.batcher.Close()

	return nil
}

// streamResources will stream resources from disk and send events to eventCh.
func (r *resource[T]) streamResources(ctx context.Context, batchCh <-chan struct{}, eventCh chan<- *loader.Event[T]) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.closeCh:
			return
		case <-batchCh:
		}

		// List the resources which exist locally (those loaded already), and those
		// which reside as in a resource file on disk.
		resources, err := r.List(ctx)
		if err != nil {
			log.Errorf("Failed to load resources from disk: %s", err)
			continue
		}

		// Reconcile the differences between what we have loaded locally, and what
		// exists on disk.k
		result := differ.Diff(resources)
		if result == nil {
			continue
		}

		// Each group is a list of resources which have been created, updated, or
		// deleted. It is critical that we send the events in the order of deleted,
		// updated, and created. This ensures we close before initing a resource
		// with the same name.
		for _, group := range []struct {
			resources []T
			eventType operatorpb.ResourceEventType
		}{
			{result.Deleted, operatorpb.ResourceEventType_DELETED},
			{result.Updated, operatorpb.ResourceEventType_UPDATED},
			{result.Created, operatorpb.ResourceEventType_CREATED},
		} {
			for _, resource := range group.resources {
				select {
				case eventCh <- &loader.Event[T]{
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
}
