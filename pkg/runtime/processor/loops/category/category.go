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

package category

import (
	"context"
	"sync"

	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	"github.com/dapr/dapr/pkg/runtime/processor/loops/instance"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/security"
)

var log = logger.NewLogger("dapr.runtime.processor.loops.category")

// InstanceFactory builds an Instance loop for a given named component. The
// factory may return a category-specialised handler that satisfies
// loop.Interface[loops.EventInstance].
type InstanceFactory func(name string) loop.Interface[loops.EventInstance]

type Options struct {
	Name      string
	Manager   instance.Manager
	CompStore *compstore.ComponentStore
	Reporter  registry.Reporter
	Security  security.Handler
	// InstanceFactory, if non-nil, overrides the default per-name instance
	// loop construction. Specialised categories (bindings) use this to inject
	// per-instance handlers that hold local state.
	InstanceFactory InstanceFactory
}

// Category is the per-resource-category loop handler.
type Category struct {
	name      string
	manager   instance.Manager
	compStore *compstore.ComponentStore
	reporter  registry.Reporter
	security  security.Handler

	instances       map[string]loop.Interface[loops.EventInstance]
	instanceFactory InstanceFactory

	loop loop.Interface[loops.EventCategory]
	wg   sync.WaitGroup
}

// New builds a Category. The returned Category's loop must be started by the
// caller (e.g. inside concurrency.NewRunnerManager).
func New(opts Options) *Category {
	c := &Category{
		name:            opts.Name,
		manager:         opts.Manager,
		compStore:       opts.CompStore,
		reporter:        opts.Reporter,
		security:        opts.Security,
		instanceFactory: opts.InstanceFactory,
		instances:       make(map[string]loop.Interface[loops.EventInstance]),
	}
	c.loop = loops.CategoryFactory.NewLoop(c)
	return c
}

// Loop returns the underlying category loop.
func (c *Category) Loop() loop.Interface[loops.EventCategory] { return c.loop }

// Run starts the category loop. It returns when the loop is closed.
func (c *Category) Run(ctx context.Context) error {
	err := c.loop.Run(ctx)
	c.wg.Wait()
	loops.CategoryFactory.CacheLoop(c.loop)
	return err
}

// Handle is the loop handler.
func (c *Category) Handle(ctx context.Context, e loops.EventCategory) error {
	switch ev := e.(type) {
	case *loops.Init:
		c.routeToInstance(ctx, ev.Component.Name, ev)
	case *loops.Close:
		c.routeToInstance(ctx, ev.Component.Name, ev)
	case *loops.Shutdown:
		c.handleShutdown(ev)
	default:
		log.Errorf("category %s: unknown event type %T", c.name, ev)
	}
	return nil
}

// routeToInstance forwards an instance-level event to the named instance loop,
// creating the instance loop on first use.
func (c *Category) routeToInstance(ctx context.Context, name string, ev loops.EventInstance) {
	inst := c.getOrCreateInstance(ctx, name)
	inst.Enqueue(ev)
}

// getOrCreateInstance returns (and lazily creates) the instance loop for name.
func (c *Category) getOrCreateInstance(ctx context.Context, name string) loop.Interface[loops.EventInstance] {
	if inst, ok := c.instances[name]; ok {
		return inst
	}
	var instLoop loop.Interface[loops.EventInstance]
	if c.instanceFactory != nil {
		instLoop = c.instanceFactory(name)
	} else {
		inst := instance.New(instance.Options{
			Manager:   c.manager,
			CompStore: c.compStore,
			Reporter:  c.reporter,
			Security:  c.security,
		})
		instLoop = inst.Loop()
	}
	c.instances[name] = instLoop

	c.wg.Go(func() {
		if err := instLoop.Run(ctx); err != nil {
			log.Errorf("instance loop %s/%s error: %s", c.name, name, err)
		}
	})

	return instLoop
}

// handleShutdown closes every instance loop and waits for them to drain.
func (c *Category) handleShutdown(ev *loops.Shutdown) {
	for name, inst := range c.instances {
		shutdown := *ev
		inst.Close(&shutdown)
		delete(c.instances, name)
	}
}

// EnqueueWithFanout enqueues the same event to every existing instance loop.
// Useful for category broadcasts (StartReadingFromBindings sends a StartInput
// to every binding instance).
func (c *Category) EnqueueWithFanout(make func(name string) loops.EventInstance) {
	for name, inst := range c.instances {
		inst.Enqueue(make(name))
	}
}

// Instances returns a snapshot of currently active instance names. Only safe
// to call from inside the category loop's Handle.
func (c *Category) Instances() map[string]loop.Interface[loops.EventInstance] {
	return c.instances
}
