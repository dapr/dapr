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

package reconciler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload.reconciler")

type Options[T differ.Resource] struct {
	Loader    loader.Interface
	CompStore *compstore.ComponentStore
	Processor *processor.Processor
}

type Reconciler[T differ.Resource] struct {
	channels *channels.Channels
	kind     string
	manager  manager[T]

	closeCh chan struct{}
	closed  atomic.Bool
	wg      sync.WaitGroup
	clock   clock.WithTicker
}

type manager[T differ.Resource] interface {
	loader.Loader[T]
	update(context.Context, T)
	delete(T)
}

func NewComponent(opts Options[compapi.Component], authz *authorizer.Authorizer) *Reconciler[compapi.Component] {
	return &Reconciler[compapi.Component]{
		clock:   clock.RealClock{},
		closeCh: make(chan struct{}),
		kind:    "Component",
		manager: &component{
			Loader: opts.Loader.Components(),
			store:  opts.CompStore,
			proc:   opts.Processor,
			auth:   authz,
		},
	}
}

func NewHTTPEndpoint(opts Options[httpendapi.HTTPEndpoint], channels *channels.Channels) *Reconciler[httpendapi.HTTPEndpoint] {
	return &Reconciler[httpendapi.HTTPEndpoint]{
		clock:    clock.RealClock{},
		closeCh:  make(chan struct{}),
		channels: channels,
		kind:     "HTTPEndpoint",
		manager: &endpoint{
			Loader: opts.Loader.HTTPEndpoints(),
			store:  opts.CompStore,
			proc:   opts.Processor,
		},
	}
}

func (r *Reconciler[T]) Run(ctx context.Context) error {
	r.wg.Add(1)
	defer r.wg.Done()

	stream, err := r.manager.Stream(ctx)
	if err != nil {
		return fmt.Errorf("error starting component stream: %s", err)
	}

	r.watchForEvents(ctx, stream)

	return nil
}

func (r *Reconciler[T]) Close() error {
	defer r.wg.Wait()
	if r.closed.CompareAndSwap(false, true) {
		close(r.closeCh)
	}

	return nil
}

func (r *Reconciler[T]) watchForEvents(ctx context.Context, stream <-chan *loader.Event[T]) {
	log.Infof("Starting to watch %s updates", r.kind)

	ticker := r.clock.NewTicker(time.Second * 60)
	defer ticker.Stop()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.closeCh:
			return
		case <-ticker.C():
			log.Debugf("Running scheduled %s reconcile", r.kind)
			resources, err := r.manager.List(ctx)
			if err != nil {
				log.Errorf("Error listing %s: %s", r.kind, err)
				continue
			}

			if err := r.reconcile(ctx, differ.Diff(resources)); err != nil {
				log.Errorf("Error reconciling %s: %s", r.kind, err)
			}
		case event := <-stream:
			r.handleEvent(ctx, event)
			if r.channels != nil {
				if err := r.channels.Refresh(); err != nil {
					log.Error(err)
					continue
				} else {
					log.Info("Channels refreshed")
				}
			}
		}
	}
}

func (r *Reconciler[T]) reconcile(ctx context.Context, result *differ.Result[T]) error {
	if result == nil {
		return nil
	}

	var wg sync.WaitGroup
	for _, group := range []struct {
		resources []T
		eventType operatorpb.ResourceEventType
	}{
		{result.Deleted, operatorpb.ResourceEventType_DELETED},
		{result.Updated, operatorpb.ResourceEventType_UPDATED},
		{result.Created, operatorpb.ResourceEventType_CREATED},
	} {
		wg.Add(len(group.resources))
		for _, resource := range group.resources {
			go func(resource T) {
				defer wg.Done()
				r.handleEvent(ctx, &loader.Event[T]{
					Type:     group.eventType,
					Resource: resource,
				})
			}(resource)
		}
		wg.Wait()
	}

	if r.channels != nil &&
		(len(result.Created) > 0 || len(result.Updated) > 0 || len(result.Deleted) > 0) {
		if err := r.channels.Refresh(); err != nil {
			return fmt.Errorf("error refreshing channels after update: %s", err)
		}

		log.Info("Channels refreshed")
	}

	return nil
}

func (r *Reconciler[T]) handleEvent(ctx context.Context, event *loader.Event[T]) {
	log.Debugf("Received %s event %s: %s", event.Resource.Kind(), event.Type, event.Resource.LogName())

	switch event.Type {
	case operatorv1pb.ResourceEventType_CREATED:
		log.Infof("Received %s creation: %s", r.kind, event.Resource.LogName())
		r.manager.update(ctx, event.Resource)
	case operatorv1pb.ResourceEventType_UPDATED:
		log.Infof("Received %s update: %s", r.kind, event.Resource.LogName())
		r.manager.update(ctx, event.Resource)
	case operatorv1pb.ResourceEventType_DELETED:
		log.Infof("Received %s deletion, closing: %s", r.kind, event.Resource.LogName())
		r.manager.delete(event.Resource)
	}
}
