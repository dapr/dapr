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

package pubsub

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

var log = logger.NewLogger("dapr.runtime.processor.loops.pubsub")

// Manager is the slice of the pubsub sub-processor used by this loop. It is
// just instance.Manager today (Init / Close).
type Manager interface {
	instance.Manager
}

type Options struct {
	AppID      string
	PubSub     Manager
	Subscriber Subscriber
	CompStore  *compstore.ComponentStore
	Reporter   registry.Reporter
	Security   security.Handler
}

// Category is the pubsub category loop handler.
type Category struct {
	appID      string
	pubsub     Manager
	subscriber Subscriber
	compStore  *compstore.ComponentStore
	reporter   registry.Reporter
	security   security.Handler

	instances map[string]loop.Interface[loops.EventInstance]

	loop loop.Interface[loops.EventCategory]
	wg   sync.WaitGroup
}

func New(opts Options) *Category {
	c := &Category{
		appID:      opts.AppID,
		pubsub:     opts.PubSub,
		subscriber: opts.Subscriber,
		compStore:  opts.CompStore,
		reporter:   opts.Reporter,
		security:   opts.Security,
		instances:  make(map[string]loop.Interface[loops.EventInstance]),
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

// Handle dispatches every event the pubsub category loop receives. The
// bodies of the per-resource cases live in sibling files (subscriber.go,
// subscription.go).
func (c *Category) Handle(ctx context.Context, e loops.EventCategory) error {
	switch ev := e.(type) {
	case *loops.Init:
		c.routeInstance(ctx, ev.Component.Name, ev)
	case *loops.Close:
		c.routeInstance(ctx, ev.Component.Name, ev)
	case *loops.ReloadPubSub:
		sendResult(ev.Result, c.subscriber.ReloadPubSub(ev.Name))
	case *loops.StopPubSub:
		c.subscriber.StopPubSub(ev.Name)
		closeIfSet(ev.Done)
	case *loops.StartAppSubscriptions:
		sendResult(ev.Result, c.subscriber.StartAppSubscriptions())
	case *loops.StopAppSubscriptions:
		c.subscriber.StopAppSubscriptions()
		closeIfSet(ev.Done)
	case *loops.StopAllSubscriptionsForever:
		c.subscriber.StopAllSubscriptionsForever()
		closeIfSet(ev.Done)
	case *loops.StartStreamerSubscription:
		sendResult(ev.Result, c.subscriber.StartStreamerSubscription(ev.Subscription, ev.ConnectionID))
	case *loops.StopStreamerSubscription:
		c.subscriber.StopStreamerSubscription(ev.Subscription, ev.ConnectionID)
		closeIfSet(ev.Done)
	case *loops.ReloadDeclaredAppSubscription:
		sendResult(ev.Result, c.subscriber.ReloadDeclaredAppSubscription(ev.Name, ev.PubSubName))
	case *loops.InitProgrammaticSubscriptions:
		sendResult(ev.Result, c.subscriber.InitProgramaticSubscriptions(ctx))
	case *loops.SubscriptionAdd:
		c.handleSubscriptionAdd(ev)
	case *loops.SubscriptionClose:
		c.handleSubscriptionClose(ev)
	case *loops.Shutdown:
		shutdown := *ev
		for name, inst := range c.instances {
			inst.Close(&shutdown)
			delete(c.instances, name)
		}
	default:
		log.Errorf("pubsub category: unknown event type %T", ev)
	}
	return nil
}

// routeInstance forwards an instance-level event to the named pubsub instance
// loop, creating the instance loop on first use.
func (c *Category) routeInstance(ctx context.Context, name string, ev loops.EventInstance) {
	inst, ok := c.instances[name]
	if !ok {
		i := instance.New(instance.Options{
			Manager:   c.pubsub,
			CompStore: c.compStore,
			Reporter:  c.reporter,
			Security:  c.security,
		})
		inst = i.Loop()
		c.instances[name] = inst

		c.wg.Go(func() {
			if err := inst.Run(ctx); err != nil {
				log.Errorf("pubsub instance loop %s error: %s", name, err)
			}
		})
	}
	inst.Enqueue(ev)
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

func closeIfSet(ch chan struct{}) {
	if ch != nil {
		close(ch)
	}
}
