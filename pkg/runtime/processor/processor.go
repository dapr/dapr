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

// Package processor manages the lifecycle of every resource that flows
// through daprd: components, HTTP endpoints, MCP servers and subscriptions.
// It is structured as a three-level dapr/kit/events/loop hierarchy:
//
//	root  ->  per-resource category  ->  per-named-instance
//
// Per-resource public entry points are defined in sibling files
// (components.go, httpendpoint.go, mcpserver.go, subscription.go); this file
// holds the shared scaffolding: Options, the Processor struct, construction,
// the runner manager that drives every loop, the Flush barrier and the
// default reporter.
package processor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors"
	grpcmanager "github.com/dapr/dapr/pkg/api/grpc/manager"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/outbox"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/binding"
	"github.com/dapr/dapr/pkg/runtime/processor/configuration"
	"github.com/dapr/dapr/pkg/runtime/processor/conversation"
	"github.com/dapr/dapr/pkg/runtime/processor/crypto"
	"github.com/dapr/dapr/pkg/runtime/processor/lock"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	loopbindings "github.com/dapr/dapr/pkg/runtime/processor/loops/bindings"
	"github.com/dapr/dapr/pkg/runtime/processor/loops/category"
	looppubsub "github.com/dapr/dapr/pkg/runtime/processor/loops/pubsub"
	"github.com/dapr/dapr/pkg/runtime/processor/loops/root"
	loopsecret "github.com/dapr/dapr/pkg/runtime/processor/loops/secret"
	loopstatestore "github.com/dapr/dapr/pkg/runtime/processor/loops/statestore"
	"github.com/dapr/dapr/pkg/runtime/processor/middleware"
	"github.com/dapr/dapr/pkg/runtime/processor/pubsub"
	"github.com/dapr/dapr/pkg/runtime/processor/secret"
	"github.com/dapr/dapr/pkg/runtime/processor/state"
	"github.com/dapr/dapr/pkg/runtime/processor/subscriber"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/runtime/wfengine/wfregistrar"
	"github.com/dapr/dapr/pkg/security"
)

var log = logger.NewLogger("dapr.runtime.processor")

type Options struct {
	ID                              string
	Namespace                       string
	Mode                            modes.DaprMode
	ActorsEnabled                   bool
	Actors                          actors.Interface
	IsHTTP                          bool
	Registry                        *registry.Registry
	ComponentStore                  *compstore.ComponentStore
	Meta                            *meta.Meta
	GlobalConfig                    *config.Configuration
	Resiliency                      resiliency.Provider
	GRPC                            *grpcmanager.Manager
	Channels                        *channels.Channels
	OperatorClient                  operatorv1.OperatorClient
	MiddlewareHTTP                  *http.HTTP
	Security                        security.Handler
	Outbox                          outbox.Outbox
	Adapter                         rtpubsub.Adapter
	AdapterStreamer                 rtpubsub.AdapterStreamer
	Reporter                        registry.Reporter
	ProgrammaticSubscriptionEnabled bool
	BindingOptionsTimeout           time.Duration
}

// Processor manages the lifecycle of all components, HTTP endpoints, MCP
// servers and subscription resources via a three level dapr/kit/events/loop
// hierarchy: root -> per-category -> per-named-instance.
//
// Concurrency: every mutation to component lifecycle state happens inside one
// of the loops in this hierarchy. The pending-secret-store-dependents map
// (for components blocked on a secret store) is owned by the root loop.
// Sub-processor read paths (SendToOutputBinding, ActorStateStoreName,
// ProcessResource, ...) still call directly into the sub-processor; the
// sub-processors retain their internal mutexes to guard those concurrent
// read callers from concurrent lifecycle writes.
type Processor struct {
	appID     string
	compStore *compstore.ComponentStore
	security  security.Handler
	reporter  registry.Reporter

	// Accessor interfaces used by the State/Secret/Binding/Subscriber
	// methods. Backed by the concrete sub-processor instances constructed in
	// New.
	state      StateManager
	secret     SecretManager
	binding    BindingManager
	subscriber *subscriber.Subscriber

	// workflowBackend is held only to satisfy the WorkflowBackend accessor.
	workflowBackend WorkflowBackendManager

	// kubernetesMode is true when running in Kubernetes mode. Used by MCPServer
	// security validation to reject configurations that are unsafe in a
	// cluster.
	kubernetesMode bool

	// inProcessWorkflows is installed after the workflow engine is created via
	// SetInProcessWorkflows and is used to register workflows when MCPServer
	// resources are loaded or hot-reloaded. Read under inProcessWorkflowsLock
	// to allow installation from a different goroutine than the consumers
	// inside the root loop.
	inProcessWorkflowsLock sync.RWMutex
	inProcessWorkflows     wfregistrar.Registrar

	// Root loop and per-category loops.
	rootLoop  *root.Root
	stateCat  *loopstatestore.Category
	secCat    *loopsecret.Category
	bindCat   *loopbindings.Category
	psCat     *looppubsub.Category
	confCat   *category.Category
	cryptoCat *category.Category
	lockCat   *category.Category
	midCat    *category.Category
	convCat   *category.Category

	// inlineManagers is used by Init/Close when Process is not running
	// (test-only path). The loop path never reads this map.
	inlineManagers map[components.Category]inlineManager

	running atomic.Bool
	closed  atomic.Bool
}

type inlineManager interface {
	Init(ctx context.Context, comp compapi.Component) error
	Close(comp compapi.Component) error
}

// New constructs a Processor. The returned Processor is not running; call
// Process to start the loop hierarchy.
func New(opts Options) *Processor {
	if opts.GlobalConfig == nil {
		opts.GlobalConfig = new(config.Configuration)
	}

	subscriberProc := subscriber.New(subscriber.Options{
		AppID:                           opts.ID,
		Namespace:                       opts.Namespace,
		Resiliency:                      opts.Resiliency,
		TracingSpec:                     opts.GlobalConfig.Spec.TracingSpec,
		IsHTTP:                          opts.IsHTTP,
		Channels:                        opts.Channels,
		GRPC:                            opts.GRPC,
		CompStore:                       opts.ComponentStore,
		Adapter:                         opts.Adapter,
		AdapterStreamer:                 opts.AdapterStreamer,
		ProgrammaticSubscriptionEnabled: opts.ProgrammaticSubscriptionEnabled,
	})

	stateProc := state.New(state.Options{
		ActorsEnabled:  opts.ActorsEnabled,
		Registry:       opts.Registry.StateStores(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
		Outbox:         opts.Outbox,
	})

	secretProc := secret.New(secret.Options{
		Registry:       opts.Registry.SecretStores(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
		OperatorClient: opts.OperatorClient,
	})

	bindingProc := binding.New(binding.Options{
		Registry:              opts.Registry.Bindings(),
		ComponentStore:        opts.ComponentStore,
		Meta:                  opts.Meta,
		IsHTTP:                opts.IsHTTP,
		Resiliency:            opts.Resiliency,
		GRPC:                  opts.GRPC,
		TracingSpec:           opts.GlobalConfig.Spec.TracingSpec,
		Channels:              opts.Channels,
		BindingOptionsTimeout: opts.BindingOptionsTimeout,
	})

	pubsubProc := pubsub.New(pubsub.Options{
		AppID:          opts.ID,
		Registry:       opts.Registry.PubSubs(),
		Meta:           opts.Meta,
		ComponentStore: opts.ComponentStore,
		Subscriber:     subscriberProc,
	})

	confProc := configuration.New(configuration.Options{
		Registry:       opts.Registry.Configurations(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
	})
	cryptoProc := crypto.New(crypto.Options{
		Registry:       opts.Registry.Crypto(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
	})
	lockProc := lock.New(lock.Options{
		Registry:       opts.Registry.Locks(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
	})
	middleProc := middleware.New(middleware.Options{
		Meta:         opts.Meta,
		RegistryHTTP: opts.Registry.HTTPMiddlewares(),
		HTTP:         opts.MiddlewareHTTP,
	})
	convProc := conversation.New(conversation.Options{
		Meta:     opts.Meta,
		Registry: opts.Registry.Conversations(),
		Store:    opts.ComponentStore,
	})

	reporter := DefaultReporter
	if opts.Reporter != nil {
		reporter = opts.Reporter
	}

	p := &Processor{
		appID:          opts.ID,
		kubernetesMode: opts.Mode == modes.KubernetesMode,
		compStore:      opts.ComponentStore,
		security:       opts.Security,
		reporter:       reporter,
		state:          stateProc,
		secret:         secretProc,
		binding:        bindingProc,
		subscriber:     subscriberProc,
		inlineManagers: map[components.Category]inlineManager{
			components.CategoryBindings:       bindingProc,
			components.CategoryConfiguration:  confProc,
			components.CategoryCryptoProvider: cryptoProc,
			components.CategoryLock:           lockProc,
			components.CategoryMiddleware:     middleProc,
			components.CategoryPubSub:         pubsubProc,
			components.CategorySecretStore:    secretProc,
			components.CategoryStateStore:     stateProc,
			components.CategoryConversation:   convProc,
		},
	}

	// Category loops -----------------------------------------------------
	p.stateCat = loopstatestore.New(loopstatestore.Options{
		State:     stateProc,
		CompStore: opts.ComponentStore,
		Reporter:  reporter,
		Security:  opts.Security,
	})
	p.secCat = loopsecret.New(loopsecret.Options{
		Secret:    secretProc,
		CompStore: opts.ComponentStore,
		Reporter:  reporter,
		Security:  opts.Security,
	})
	p.bindCat = loopbindings.New(loopbindings.Options{
		Binding:   bindingProc,
		CompStore: opts.ComponentStore,
		Reporter:  reporter,
		Security:  opts.Security,
	})
	p.psCat = looppubsub.New(looppubsub.Options{
		AppID:      opts.ID,
		PubSub:     pubsubProc,
		Subscriber: subscriberProc,
		CompStore:  opts.ComponentStore,
		Reporter:   reporter,
		Security:   opts.Security,
	})
	p.confCat = category.New(category.Options{
		Name:      string(components.CategoryConfiguration),
		Manager:   confProc,
		CompStore: opts.ComponentStore,
		Reporter:  reporter,
		Security:  opts.Security,
	})
	p.cryptoCat = category.New(category.Options{
		Name:      string(components.CategoryCryptoProvider),
		Manager:   cryptoProc,
		CompStore: opts.ComponentStore,
		Reporter:  reporter,
		Security:  opts.Security,
	})
	p.lockCat = category.New(category.Options{
		Name:      string(components.CategoryLock),
		Manager:   lockProc,
		CompStore: opts.ComponentStore,
		Reporter:  reporter,
		Security:  opts.Security,
	})
	p.midCat = category.New(category.Options{
		Name:      string(components.CategoryMiddleware),
		Manager:   middleProc,
		CompStore: opts.ComponentStore,
		Reporter:  reporter,
		Security:  opts.Security,
	})
	p.convCat = category.New(category.Options{
		Name:      string(components.CategoryConversation),
		Manager:   convProc,
		CompStore: opts.ComponentStore,
		Reporter:  reporter,
		Security:  opts.Security,
	})

	// Root loop ----------------------------------------------------------
	cats := map[components.Category]loop.Interface[loops.EventCategory]{
		components.CategoryBindings:       p.bindCat.Loop(),
		components.CategoryConfiguration:  p.confCat.Loop(),
		components.CategoryCryptoProvider: p.cryptoCat.Loop(),
		components.CategoryLock:           p.lockCat.Loop(),
		components.CategoryMiddleware:     p.midCat.Loop(),
		components.CategoryPubSub:         p.psCat.Loop(),
		components.CategorySecretStore:    p.secCat.Loop(),
		components.CategoryStateStore:     p.stateCat.Loop(),
		components.CategoryConversation:   p.convCat.Loop(),
	}
	p.rootLoop = root.New(root.Options{
		CompStore:      opts.ComponentStore,
		Categories:     cats,
		PubSubCategory: p.psCat.Loop(),
		Secret:         secretProc,
		KubernetesMode: p.kubernetesMode,
		RegisterMCPServer: func(ctx context.Context, s mcpserverapi.MCPServer) error {
			reg := p.getInProcessWorkflows()
			if reg == nil {
				return nil
			}
			return reg.RegisterMCPServer(ctx, s, opts.ComponentStore, opts.Security)
		},
		UnregisterMCPServer: func(name string) {
			reg := p.getInProcessWorkflows()
			if reg == nil {
				return
			}
			reg.UnregisterMCPServer(name)
		},
	})

	return p
}

// Process runs the loop hierarchy until ctx is cancelled. It coordinates
// orderly shutdown by closing the root loop, which fans out to category and
// instance loops.
func (p *Processor) Process(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("processor is already running")
	}

	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			p.closed.Store(true)
			p.rootLoop.Loop().Close(new(loops.Shutdown))
		})
	}

	return concurrency.NewRunnerManager(
		p.rootLoop.Run,
		p.stateCat.Run,
		p.secCat.Run,
		p.bindCat.Run,
		p.psCat.Run,
		p.confCat.Run,
		p.cryptoCat.Run,
		p.lockCat.Run,
		p.midCat.Run,
		p.convCat.Run,
		p.subscriber.Run,
		func(ctx context.Context) error {
			<-ctx.Done()
			shutdown()
			return nil
		},
	).Run(ctx)
}

// Flush blocks until every Init currently enqueued in the root loop has
// completed. It is a no-op when the processor has already shut down. The
// Barrier event is queued unconditionally, so it is safe to call Flush
// concurrently with Process; the call simply waits until the root loop has
// processed every event queued ahead of the Barrier.
func (p *Processor) Flush(ctx context.Context) error {
	if p.closed.Load() {
		return nil
	}
	done := make(chan struct{})
	p.rootLoop.Loop().Enqueue(&loops.Barrier{Done: done})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// DefaultReporter is the default resource reporter for the registry. It does
// nothing.
func DefaultReporter(context.Context, compapi.Component, *operatorv1.ResourceResult) error {
	return nil
}
