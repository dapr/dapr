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
	"github.com/dapr/dapr/pkg/internal/loader/disk/dirdata"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
)

// resource is a generic implementation of a disk resource loader. resource
// will watch and load resources from disk.
type resource[T differ.Resource] struct {
	store      store.Store[T]
	diskLoader internalloader.Loader[T]

	triggerCh chan *dirdata.DirData
	closeCh   chan struct{}
	closed    atomic.Bool
	wg        sync.WaitGroup

	lock       sync.RWMutex
	diffResult *differ.Result[T]
	resultCh   chan struct{}
}

type resourceOptions[T differ.Resource] struct {
	loader internalloader.Loader[T]
	store  store.Store[T]
}

func newResource[T differ.Resource](opts resourceOptions[T]) *resource[T] {
	return &resource[T]{
		store:      opts.store,
		diskLoader: opts.loader,
		triggerCh:  make(chan *dirdata.DirData, 1),
		closeCh:    make(chan struct{}),
		resultCh:   make(chan struct{}, 1),
	}
}

// List returns the current list of resources loaded from disk.
func (r *resource[T]) List(ctx context.Context) (*differ.LocalRemoteResources[T], error) {
	return r.list(ctx, nil)
}

// list returns the current list of resources. If dirData is non-nil, it uses
// the pre-read file data instead of reading from disk.
func (r *resource[T]) list(ctx context.Context, dirData *dirdata.DirData) (*differ.LocalRemoteResources[T], error) {
	var remotes []T
	var err error

	if dirData != nil {
		if ddl, ok := r.diskLoader.(dirdata.DirDataLoader[T]); ok {
			remotes, err = ddl.LoadFromDirData(dirData)
		} else {
			remotes, err = r.diskLoader.Load(ctx)
		}
	} else {
		remotes, err = r.diskLoader.Load(ctx)
	}

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

	r.wg.Go(func() {
		r.streamLoop(ctx, conn)
	})

	return conn, nil
}

func (r *resource[T]) streamLoop(ctx context.Context, conn *loader.StreamConn[T]) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.closeCh:
			return
		case <-r.resultCh:
		}

		r.lock.Lock()
		result := r.diffResult
		r.diffResult = nil
		r.lock.Unlock()

		if result != nil {
			r.sendEvents(ctx, conn, result)
		}
	}
}

func (r *resource[T]) sendEvents(ctx context.Context, conn *loader.StreamConn[T], result *differ.Result[T]) {
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

// trigger sends a trigger signal to process file changes. If dirData is
// non-nil, the pre-read file data is used instead of re-reading files from
// disk, avoiding file handle contention when multiple resource types are
// loaded from the same directories.
func (r *resource[T]) trigger(ctx context.Context, dirData ...*dirdata.DirData) error {
	var data *dirdata.DirData
	if len(dirData) > 0 {
		data = dirData[0]
	}
	select {
	case r.triggerCh <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *resource[T]) run(ctx context.Context) error {
	defer func() {
		if r.closed.CompareAndSwap(false, true) {
			close(r.closeCh)
		}
		r.wg.Wait()
	}()

	for {
		var dirData *dirdata.DirData
		select {
		case <-ctx.Done():
			return nil
		case <-r.closeCh:
			return nil
		case dirData = <-r.triggerCh:
		}

		// List the resources which exist locally (those loaded already), and those
		// which reside as in a resource file on disk.
		resources, err := r.list(ctx, dirData)
		if err != nil {
			return fmt.Errorf("failed to load resources from disk: %s", err)
		}

		// Reconcile the differences between what we have loaded locally, and what
		// exists on disk.
		result := differ.Diff(resources)
		if result == nil {
			continue
		}

		r.lock.Lock()
		r.diffResult = result
		r.lock.Unlock()

		// Signal that a new result is available
		select {
		case r.resultCh <- struct{}{}:
		default:
		}
	}
}
