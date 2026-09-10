/*
Copyright 2026 The Dapr Authors
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

package root

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

var log = logger.NewLogger("dapr.runtime.processor.loops.root")

// SecretProcessor resolves secret references on a resource. The root uses it
// to (a) preprocess incoming components for unresolved secret-store
// dependencies and (b) resolve secrets on HTTPEndpoint and MCPServer
// resources.
type SecretProcessor interface {
	ProcessResource(ctx context.Context, r meta.Resource) (bool, string)
}

// Options configures the root loop.
type Options struct {
	CompStore *compstore.ComponentStore

	// Categories maps a category to its category loop. The root looks up by
	// category prefix on the component's Spec.Type.
	Categories map[components.Category]loop.Interface[loops.EventCategory]

	// PubSubCategory is the pubsub category loop. Subscription resource
	// events are routed there.
	PubSubCategory loop.Interface[loops.EventCategory]

	// Secret resolves references on incoming resources.
	Secret SecretProcessor

	// KubernetesMode is true when running in Kubernetes mode. Used by
	// MCPServer security validation to reject configurations that are unsafe
	// in a cluster.
	KubernetesMode bool

	// RegisterMCPServer registers any in-process workflows backing an MCP
	// server. nil-safe: if no workflow registrar has been installed yet, the
	// callback is a no-op. Called from inside the root loop's Handle.
	RegisterMCPServer func(ctx context.Context, s mcpserverapi.MCPServer) error

	// UnregisterMCPServer is the inverse of RegisterMCPServer.
	UnregisterMCPServer func(name string)
}

// Root is the processor's root loop handler.
type Root struct {
	compStore  *compstore.ComponentStore
	categories map[components.Category]loop.Interface[loops.EventCategory]
	pubsubCat  loop.Interface[loops.EventCategory]
	secret     SecretProcessor

	kubernetesMode      bool
	registerMCPServer   func(ctx context.Context, s mcpserverapi.MCPServer) error
	unregisterMCPServer func(name string)

	// fatalErr retains the first non-ignored component init failure seen by a
	// finalizer. Run returns it after the finalizers drain, so a failure that
	// races with shutdown still propagates out (the runtime's init path masks
	// cancelled-context errors, so it cannot be relied on for this).
	fatalErr atomic.Pointer[error]

	// mcpServers holds one loop per MCPServer name. Entries are created
	// lazily on first event and persist for the life of the root loop so the
	// per-name FIFO ordering survives delete + re-add for the same name.
	mcpServers   map[string]loop.Interface[loops.EventMCPServer]
	mcpServersWG sync.WaitGroup

	// httpEndpoints holds one loop per HTTPEndpoint name. Same lifecycle
	// rules as mcpServers above.
	httpEndpoints   map[string]loop.Interface[loops.EventHTTPEndpoint]
	httpEndpointsWG sync.WaitGroup

	// pendingDependents stashes components that depend on an uninitialised
	// secret store. Keyed by secret store name. Mutated only inside Handle.
	pendingDependents map[string][]compapi.Component

	// inFlight is the count of outstanding async work the root must wait for
	// before a Barrier may complete. Three kinds of work are counted:
	//   - Component Inits dispatched to category loops (drained by
	//     InstanceInitDone);
	//   - MCPServer Adds dispatched to per-name MCPServer loops (drained by
	//     MCPServerRegistered);
	//   - HTTPEndpoint Adds dispatched to per-name HTTPEndpoint loops
	//     (drained by HTTPEndpointAdded).
	// Deletes are not counted: callers that need to wait for a Delete pass
	// the synchronous Done channel on the event itself. Mutated only inside
	// Handle.
	inFlight int

	// pendingBarriers holds Done channels for Barriers that arrived while
	// inFlight > 0. They are closed when inFlight returns to 0.
	pendingBarriers []chan struct{}

	// runCtx is captured from Run so per-name MCPServer loops created from
	// inside Handle can be launched with the same lifecycle as the root.
	runCtx context.Context

	loop loop.Interface[loops.EventRoot]

	// finalizers tracks the goroutines spawned to wrap category Result
	// channels. We wait for them on shutdown.
	finalizers sync.WaitGroup
}

func New(opts Options) *Root {
	r := &Root{
		compStore:           opts.CompStore,
		categories:          opts.Categories,
		pubsubCat:           opts.PubSubCategory,
		secret:              opts.Secret,
		kubernetesMode:      opts.KubernetesMode,
		registerMCPServer:   opts.RegisterMCPServer,
		unregisterMCPServer: opts.UnregisterMCPServer,
		pendingDependents:   make(map[string][]compapi.Component),
		mcpServers:          make(map[string]loop.Interface[loops.EventMCPServer]),
		httpEndpoints:       make(map[string]loop.Interface[loops.EventHTTPEndpoint]),
	}
	r.loop = loops.RootFactory.NewLoop(r)
	return r
}

// Loop returns the underlying root loop. External callers enqueue events
// through it.
func (r *Root) Loop() loop.Interface[loops.EventRoot] { return r.loop }

// Run starts the root loop. Returns when the loop is closed.
func (r *Root) Run(ctx context.Context) error {
	r.runCtx = ctx
	err := r.loop.Run(ctx)
	// Wait for in-flight init finalizers before reading fatalErr: a component
	// whose init overruns the shutdown still records its failure here.
	r.finalizers.Wait()
	r.mcpServersWG.Wait()
	r.httpEndpointsWG.Wait()
	loops.RootFactory.CacheLoop(r.loop)
	// A non-ignored component init failure is fatal to the runtime. Surface it
	// over a clean loop shutdown so it propagates out of the processor's
	// Process runner and, in turn, the runtime's Run.
	if err == nil || errors.Is(err, context.Canceled) {
		if fatal := r.fatalErr.Load(); fatal != nil {
			return *fatal
		}
	}
	return err
}

// recordFatalInitError stores the first non-ignored component init failure.
func (r *Root) recordFatalInitError(err error) {
	r.fatalErr.CompareAndSwap(nil, &err)
}

// Handle dispatches every event the root loop receives. The bodies of the
// per-resource cases live in sibling files (component.go, httpendpoint.go,
// mcpserver.go).
func (r *Root) Handle(ctx context.Context, e loops.EventRoot) error {
	switch ev := e.(type) {
	case *loops.Init:
		r.handleInit(ctx, ev)
	case *loops.Close:
		r.handleClose(ctx, ev)
	case *loops.AddHTTPEndpoint:
		r.handleHTTPEndpoint(ev)
	case *loops.HTTPEndpointAdded:
		r.handleHTTPEndpointAdded()
	case *loops.AddMCPServer:
		r.handleMCPServer(ev)
	case *loops.DeleteMCPServer:
		r.handleDeleteMCPServer(ev)
	case *loops.MCPServerRegistered:
		r.handleMCPServerRegistered()
	case *loops.SubscriptionAdd:
		if r.pubsubCat == nil {
			sendResult(ev.Result, errors.New("subscription add: no pubsub category loop"))
			return nil
		}
		r.pubsubCat.Enqueue(ev)
	case *loops.SubscriptionClose:
		if r.pubsubCat == nil {
			sendResult(ev.Result, errors.New("subscription close: no pubsub category loop"))
			return nil
		}
		r.pubsubCat.Enqueue(ev)
	case *loops.InstanceInitDone:
		r.handleInstanceInitDone(ev)
	case *loops.Barrier:
		r.handleBarrier(ev)
	case *loops.Shutdown:
		r.handleShutdown(ev)
	default:
		log.Errorf("root: unknown event type %T", ev)
	}
	return nil
}

// handleBarrier closes the Barrier's Done channel once all currently in-flight
// Init operations have completed (i.e. inFlight returns to 0). If there is
// no in-flight work, the Done is closed immediately.
func (r *Root) handleBarrier(ev *loops.Barrier) {
	if ev.Done == nil {
		return
	}
	if r.inFlight == 0 {
		close(ev.Done)
		return
	}
	r.pendingBarriers = append(r.pendingBarriers, ev.Done)
}

func (r *Root) handleShutdown(ev *loops.Shutdown) {
	// Close every category loop. Each category will close its instance loops.
	for _, cl := range r.categories {
		shutdown := *ev
		cl.Close(&shutdown)
	}
	// Close every per-name MCPServer loop.
	for name, ml := range r.mcpServers {
		shutdown := *ev
		ml.Close(&shutdown)
		delete(r.mcpServers, name)
	}
	// Close every per-name HTTPEndpoint loop.
	for name, hl := range r.httpEndpoints {
		shutdown := *ev
		hl.Close(&shutdown)
		delete(r.httpEndpoints, name)
	}
}

// category returns the components.Category for the given component, or "" if
// the type prefix is not recognised.
func (r *Root) category(comp compapi.Component) components.Category {
	for cat := range r.categories {
		if strings.HasPrefix(comp.Spec.Type, string(cat)+".") {
			return cat
		}
	}
	return ""
}

func sendResult(ch chan<- error, err error) {
	if ch == nil {
		return
	}
	select {
	case ch <- err:
	default:
	}
}
