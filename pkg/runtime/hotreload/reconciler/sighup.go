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

package reconciler

import (
	"context"
	"fmt"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/healthz"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
)

// SIGHUPReconciler watches for changes to resources that require a full
// runtime restart and sends SIGHUP to trigger the restart.
type SIGHUPReconciler[T differ.Resource] struct {
	kind    string
	htarget healthz.Target
	loader  loader.Loader[T]

	// getExisting returns the existing resource from the component store by
	// name. Used to detect whether a resource has actually changed.
	getExisting func(name string) (T, bool)

	loop loop.Interface[Event]
}

// NewSIGHUPConfigurations creates a new reconciler for Configuration resources
// that triggers SIGHUP on changes.
func NewSIGHUPConfigurations(opts Options[configapi.Configuration]) *SIGHUPReconciler[configapi.Configuration] {
	var zero configapi.Configuration
	r := &SIGHUPReconciler[configapi.Configuration]{
		kind:        zero.Kind(),
		htarget:     opts.Healthz.AddTarget("configuration-sighup-reconciler"),
		loader:      opts.Loader.Configurations(),
		getExisting: opts.CompStore.GetConfigurationResource,
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

// NewSIGHUPHTTPEndpoints creates a new reconciler for HTTPEndpoint resources
// that triggers SIGHUP on changes.
func NewSIGHUPHTTPEndpoints(opts Options[httpendpointapi.HTTPEndpoint]) *SIGHUPReconciler[httpendpointapi.HTTPEndpoint] {
	var zero httpendpointapi.HTTPEndpoint
	r := &SIGHUPReconciler[httpendpointapi.HTTPEndpoint]{
		kind:        zero.Kind(),
		htarget:     opts.Healthz.AddTarget("httpendpoint-sighup-reconciler"),
		loader:      opts.Loader.HTTPEndpoints(),
		getExisting: opts.CompStore.GetHTTPEndpoint,
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

// NewSIGHUPResiliencies creates a new reconciler for Resiliency resources
// that triggers SIGHUP on changes.
func NewSIGHUPResiliencies(opts Options[resiliencyapi.Resiliency]) *SIGHUPReconciler[resiliencyapi.Resiliency] {
	var zero resiliencyapi.Resiliency
	r := &SIGHUPReconciler[resiliencyapi.Resiliency]{
		kind:        zero.Kind(),
		htarget:     opts.Healthz.AddTarget("resiliency-sighup-reconciler"),
		loader:      opts.Loader.Resiliencies(),
		getExisting: opts.CompStore.GetResiliencyResource,
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

func (r *SIGHUPReconciler[T]) Run(ctx context.Context) error {
	conn, err := r.loader.Stream(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("error running %s stream: %w", r.kind, err)
	}

	r.htarget.Ready()

	log.Infof("Starting to watch %s updates for SIGHUP reload", r.kind)

	defer loopFactory.CacheLoop(r.loop)

	return concurrency.NewRunnerManager(
		r.loop.Run,
		func(ctx context.Context) error {
			return r.watchConn(ctx, conn)
		},
	).Run(ctx)
}

func (r *SIGHUPReconciler[T]) watchConn(ctx context.Context, conn *loader.StreamConn[T]) error {
	for {
		select {
		case <-ctx.Done():
			r.loop.Close(&shutdown{Error: ctx.Err()})
			return nil
		case <-conn.ReconcileCh:
			// On reconnect, we don't need to reconcile since SIGHUP will
			// restart the runtime and reload everything anyway.
			log.Debugf("Reconnected %s stream, skipping reconcile (will use SIGHUP)", r.kind)
		case event := <-conn.EventCh:
			r.loop.Enqueue(&resourceEvent[T]{Event: event})
		}
	}
}

func (r *SIGHUPReconciler[T]) Handle(ctx context.Context, event Event) error {
	switch e := event.(type) {
	case *resourceEvent[T]:
		r.handleResourceEvent(e.Event)
	case *shutdown:
		r.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown SIGHUP reconciler event type: %T", e))
	}

	return nil
}

func (r *SIGHUPReconciler[T]) handleResourceEvent(event *loader.Event[T]) {
	name := event.Resource.GetName()

	switch event.Type {
	case operatorv1pb.ResourceEventType_DELETED:
		if _, exists := r.getExisting(name); !exists {
			log.Debugf("Ignoring %s %s event for %s: resource not known",
				r.kind, event.Type, event.Resource.LogName())
			return
		}

	case operatorv1pb.ResourceEventType_CREATED, operatorv1pb.ResourceEventType_UPDATED:
		if existing, exists := r.getExisting(name); exists {
			if differ.AreSame(existing, event.Resource) {
				log.Debugf("Ignoring %s %s event for %s: resource has not changed",
					r.kind, event.Type, event.Resource.LogName())
				return
			}
		}

	default:
		// Unknown event type, still trigger SIGHUP.
	}

	log.Infof("Received %s %s event: %s - triggering SIGHUP reload",
		r.kind, event.Type, event.Resource.LogName())

	// Send SIGHUP to ourselves to trigger runtime restart
	if err := sendSIGHUP(); err != nil {
		log.Errorf("Failed to send SIGHUP signal: %s", err)
	}
}

func (r *SIGHUPReconciler[T]) handleShutdown(e *shutdown) {
	log.Debugf("%s SIGHUP reconciler loop shutdown: %v", r.kind, e.Error)
}
