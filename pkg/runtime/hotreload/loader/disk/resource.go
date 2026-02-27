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
	"errors"
	"fmt"
	"sync/atomic"

	internalloader "github.com/dapr/dapr/pkg/internal/loader"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
)

// resource is a generic implementation of a disk resource loader. resource
// will watch and load resources from disk.
type resource[T differ.Resource] struct {
	updateCh  chan struct{}               // capacity 1, notified by disk on fs changes
	streamCh  chan chan *loader.Event[T]  // used to pass eventCh from Stream() to run()
	store     store.Store[T]
	diskLoader internalloader.Loader[T]

	running chan struct{}
	closeCh chan struct{}
	closed  atomic.Bool
}

type resourceOptions[T differ.Resource] struct {
	loader internalloader.Loader[T]
	store  store.Store[T]
}

func newResource[T differ.Resource](opts resourceOptions[T]) *resource[T] {
	return &resource[T]{
		updateCh:   make(chan struct{}, 1),
		streamCh:   make(chan chan *loader.Event[T], 1),
		store:      opts.store,
		diskLoader: opts.loader,
		running:    make(chan struct{}),
		closeCh:    make(chan struct{}),
	}
}

// notify is called by disk to notify this resource that a filesystem change
// was detected. It is a non-blocking send; if a notification is already
// pending it is a no-op.
func (r *resource[T]) notify() {
	select {
	case r.updateCh <- struct{}{}:
	default:
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

// Stream returns a connection of events that will be sent when a resource is
// created, updated, or deleted.
func (r *resource[T]) Stream(ctx context.Context) (*loader.StreamConn[T], error) {
	if r.closed.Load() {
		return nil, errors.New("stream is closed")
	}

	eventCh := make(chan *loader.Event[T])
	select {
	case r.streamCh <- eventCh:
	case <-r.closeCh:
		return nil, errors.New("stream is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &loader.StreamConn[T]{
		EventCh:     eventCh,
		ReconcileCh: make(chan struct{}),
	}, nil
}

func (r *resource[T]) run(ctx context.Context) error {
	defer func() {
		if r.closed.CompareAndSwap(false, true) {
			close(r.closeCh)
		}
	}()

	close(r.running)

	// Wait for Stream() to register the event channel before processing updates.
	var eventCh chan *loader.Event[T]
	select {
	case <-ctx.Done():
		return nil
	case <-r.closeCh:
		return nil
	case eventCh = <-r.streamCh:
	}

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
		// exists on disk.
		result := differ.Diff(resources)
		if result == nil {
			continue
		}

		r.sendEvents(ctx, eventCh, result)
	}
}

// sendEvents sends the diff result as individual events to the event channel.
// It is critical that we send the events in the order of deleted, updated, and
// created. This ensures we close before initialising a resource with the same
// name.
func (r *resource[T]) sendEvents(ctx context.Context, eventCh chan *loader.Event[T], result *differ.Result[T]) {
	for _, group := range []struct {
		resources []T
		eventType operatorpb.ResourceEventType
	}{
		{result.Deleted, operatorpb.ResourceEventType_DELETED},
		{result.Updated, operatorpb.ResourceEventType_UPDATED},
		{result.Created, operatorpb.ResourceEventType_CREATED},
	} {
		for _, res := range group.resources {
			select {
			case eventCh <- &loader.Event[T]{
				Resource: res,
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

