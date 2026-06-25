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

package statestore

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

var log = logger.NewLogger("dapr.runtime.processor.loops.statestore")

// Manager is the slice of the state sub-processor used by this loop.
type Manager interface {
	instance.Manager
	ActorStateStoreName() (string, bool)
}

type Options struct {
	State     Manager
	CompStore *compstore.ComponentStore
	Reporter  registry.Reporter
	Security  security.Handler
}

type Category struct {
	state     Manager
	compStore *compstore.ComponentStore
	reporter  registry.Reporter
	security  security.Handler

	instances map[string]loop.Interface[loops.EventInstance]

	loop loop.Interface[loops.EventCategory]
	wg   sync.WaitGroup
}

func New(opts Options) *Category {
	c := &Category{
		state:     opts.State,
		compStore: opts.CompStore,
		reporter:  opts.Reporter,
		security:  opts.Security,
		instances: make(map[string]loop.Interface[loops.EventInstance]),
	}
	c.loop = loops.CategoryFactory.NewLoop(c)
	return c
}

func (c *Category) Loop() loop.Interface[loops.EventCategory] { return c.loop }

func (c *Category) Run(ctx context.Context) error {
	err := c.loop.Run(ctx)
	c.wg.Wait()
	loops.CategoryFactory.CacheLoop(c.loop)
	return err
}

func (c *Category) Handle(ctx context.Context, e loops.EventCategory) error {
	switch ev := e.(type) {
	case *loops.Init:
		c.routeInstance(ctx, ev.Component.Name, ev)
	case *loops.Close:
		c.routeInstance(ctx, ev.Component.Name, ev)
	case *loops.Shutdown:
		shutdown := *ev
		for name, inst := range c.instances {
			inst.Close(&shutdown)
			delete(c.instances, name)
		}
	default:
		log.Errorf("statestore category: unknown event type %T", ev)
	}
	return nil
}

// ActorStateStoreName is read directly from the sub-processor's atomic field.
// Safe to call from any goroutine without going through the loop.
func (c *Category) ActorStateStoreName() (string, bool) {
	return c.state.ActorStateStoreName()
}

func (c *Category) routeInstance(ctx context.Context, name string, ev loops.EventInstance) {
	inst, ok := c.instances[name]
	if !ok {
		i := instance.New(instance.Options{
			Manager:   c.state,
			CompStore: c.compStore,
			Reporter:  c.reporter,
			Security:  c.security,
		})
		inst = i.Loop()
		c.instances[name] = inst

		c.wg.Go(func() {
			if err := inst.Run(ctx); err != nil {
				log.Errorf("statestore instance loop %s error: %s", name, err)
			}
		})
	}
	inst.Enqueue(ev)
}
