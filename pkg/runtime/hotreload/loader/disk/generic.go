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

type generic[T differ.Resource] struct {
	batcher    *batcher.Batcher[string]
	store      store.Store[T]
	diskLoader components.ManifestLoader[T]

	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

func newGeneric[T differ.Resource](opts Options, batcher *batcher.Batcher[string], store store.Store[T]) *generic[T] {
	return &generic[T]{
		batcher:    batcher,
		store:      store,
		diskLoader: components.NewDiskManifestLoader[T](opts.Dirs...),
		closeCh:    make(chan struct{}),
	}
}

func (g *generic[T]) List(ctx context.Context) (*differ.LocalRemoteResources[T], error) {
	remotes, err := g.diskLoader.Load()
	if err != nil {
		return nil, err
	}

	return &differ.LocalRemoteResources[T]{
		Local:  g.store.List(),
		Remote: remotes,
	}, nil
}

func (g *generic[T]) Stream(ctx context.Context) (<-chan *loader.Event[T], error) {
	batchCh := make(chan struct{})
	g.batcher.Subscribe(batchCh)

	eventCh := make(chan *loader.Event[T])
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.streamResources(ctx, batchCh, eventCh)
	}()

	return eventCh, nil
}

func (g *generic[T]) close() error {
	defer g.wg.Wait()
	if g.closed.CompareAndSwap(false, true) {
		close(g.closeCh)
	}

	g.batcher.Close()

	return nil
}

func (g *generic[T]) streamResources(ctx context.Context, batchCh <-chan struct{}, eventCh chan<- *loader.Event[T]) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.closeCh:
			return
		case <-batchCh:
		}

		resources, err := g.List(ctx)
		if err != nil {
			log.Errorf("Failed to load resources from disk: %s", err)
			continue
		}

		result := differ.Diff(resources)
		if result == nil {
			continue
		}

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
				case <-g.closeCh:
					return
				case <-ctx.Done():
					return
				}
			}
		}
	}
}
