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
	"time"

	"k8s.io/utils/clock"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/healthz"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/reconciler/loops"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload.reconciler")

type Options[T differ.Resource] struct {
	Loader     loader.Interface
	CompStore  *compstore.ComponentStore
	Processor  *processor.Processor
	Authorizer *authorizer.Authorizer
	Healthz    healthz.Healthz
}

type Reconciler[T differ.Resource] struct {
	kind    string
	manager manager[T]
	htarget healthz.Target

	clock clock.WithTicker
}

type manager[T differ.Resource] interface {
	loader.Loader[T]
	update(context.Context, T)
	delete(context.Context, T)
}

func NewComponents(opts Options[compapi.Component]) *Reconciler[compapi.Component] {
	return &Reconciler[compapi.Component]{
		clock:   clock.RealClock{},
		kind:    compapi.Kind,
		htarget: opts.Healthz.AddTarget("component-reconciler"),
		manager: &components{
			Loader: opts.Loader.Components(),
			store:  opts.CompStore,
			proc:   opts.Processor,
			auth:   opts.Authorizer,
		},
	}
}

func NewSubscriptions(opts Options[subapi.Subscription]) *Reconciler[subapi.Subscription] {
	return &Reconciler[subapi.Subscription]{
		clock:   clock.RealClock{},
		kind:    subapi.Kind,
		htarget: opts.Healthz.AddTarget("subscription-reconciler"),
		manager: &subscriptions{
			Loader: opts.Loader.Subscriptions(),
			store:  opts.CompStore,
			proc:   opts.Processor,
		},
	}
}

func (r *Reconciler[T]) Run(ctx context.Context) error {
	conn, err := r.manager.Stream(ctx)
	if err != nil {
		return fmt.Errorf("error running component stream: %w", err)
	}

	r.htarget.Ready()

	log.Infof("Starting to watch %s updates", r.kind)

	ticker := r.clock.NewTicker(time.Second * 60)
	defer ticker.Stop()

	l := loop.New[loops.Event](1024).NewLoop(r)

	return concurrency.NewRunnerManager(
		l.Run,
		func(ctx context.Context) error {
			defer l.Close(nil)
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C():
					log.Debugf("Running scheduled %s reconcile", r.kind)
					l.Enqueue(&loops.Tick{})
				case <-conn.ReconcileCh:
					log.Debugf("Reconciling all %s", r.kind)
					l.Enqueue(&loops.Reconcile{})
				case event := <-conn.EventCh:
					l.Enqueue(&loops.Resource{Event: event})
				}
			}
		},
	).Run(ctx)
}

// Handle implements loop.Handler[loops.Event] for the reconciler loop.
func (r *Reconciler[T]) Handle(ctx context.Context, event loops.Event) error {
	switch e := event.(type) {
	case *loops.Tick, *loops.Reconcile:
		resources, err := r.manager.List(ctx)
		if err != nil {
			log.Errorf("Error listing %s: %s", r.kind, err)
			return nil
		}
		r.reconcile(ctx, differ.Diff(resources))
	case *loops.Resource:
		if le, ok := e.Event.(*loader.Event[T]); ok {
			r.handleEvent(ctx, le)
		}
	}
	return nil
}

func (r *Reconciler[T]) reconcile(ctx context.Context, result *differ.Result[T]) {
	if result == nil {
		return
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
			go func(resource T, eventType operatorpb.ResourceEventType) {
				defer wg.Done()
				r.handleEvent(ctx, &loader.Event[T]{
					Type:     eventType,
					Resource: resource,
				})
			}(resource, group.eventType)
		}

		wg.Wait()
	}
}

func (r *Reconciler[T]) handleEvent(ctx context.Context, event *loader.Event[T]) {
	log.Debugf("Received %s event %s: %s", event.Resource.Kind(), event.Type, event.Resource.LogName())

	switch event.Type {
	case operatorpb.ResourceEventType_CREATED:
		log.Debugf("Received %s creation: %s", r.kind, event.Resource.LogName())
		r.manager.update(ctx, event.Resource)
	case operatorpb.ResourceEventType_UPDATED:
		log.Debugf("Received %s update: %s", r.kind, event.Resource.LogName())
		r.manager.update(ctx, event.Resource)
	case operatorpb.ResourceEventType_DELETED:
		log.Debugf("Received %s deletion, closing: %s", r.kind, event.Resource.LogName())
		r.manager.delete(ctx, event.Resource)
	}
}
