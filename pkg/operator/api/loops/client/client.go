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

package client

import (
	"context"
	"encoding/json"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/operator/api/informer"
	"github.com/dapr/dapr/pkg/operator/api/loops"
	"github.com/dapr/dapr/pkg/operator/api/loops/sender"
	"github.com/dapr/dapr/pkg/operator/api/loops/stream"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var (
	log              = logger.NewLogger("dapr.operator.api.loops.client")
	LoopFactoryCache = loop.New[loops.EventClient](16)
)

type Options[T meta.Resource] struct {
	EventCh        <-chan *informer.Event[T]
	CancelWatch    context.CancelFunc
	Stream         sender.Interface
	Namespace      string
	PodName        string
	KubeClient     client.Client
	ProcessSecrets func(context.Context, *T, string, client.Client) error
}

type Client[T meta.Resource] struct {
	eventCh        <-chan *informer.Event[T]
	cancelWatch    context.CancelFunc
	namespace      string
	podName        string
	kubeClient     client.Client
	processSecrets func(context.Context, *T, string, client.Client) error

	loop       loop.Interface[loops.EventClient]
	streamLoop loop.Interface[loops.EventStream]
}

func New[T meta.Resource](ctx context.Context, opts Options[T]) *Client[T] {
	c := &Client[T]{
		eventCh:        opts.EventCh,
		cancelWatch:    opts.CancelWatch,
		namespace:      opts.Namespace,
		podName:        opts.PodName,
		kubeClient:     opts.KubeClient,
		processSecrets: opts.ProcessSecrets,
		streamLoop: stream.New(stream.Options{
			Stream: opts.Stream,
		}),
	}
	c.loop = LoopFactoryCache.NewLoop(c)
	return c
}

func (c *Client[T]) Run(ctx context.Context) error {
	return concurrency.NewRunnerManager(
		c.watchEvents,
		c.streamLoop.Run,
		c.loop.Run,
	).Run(ctx)
}

func (c *Client[T]) CacheLoop() {
	LoopFactoryCache.CacheLoop(c.loop)
}

func (c *Client[T]) watchEvents(ctx context.Context) error {
	defer c.cancelWatch()

	for {
		select {
		case <-ctx.Done():
			c.loop.Close(&loops.Shutdown{Error: ctx.Err()})
			return nil
		case event, ok := <-c.eventCh:
			if !ok {
				c.loop.Close(&loops.Shutdown{})
				return nil
			}
			c.loop.Enqueue(&loops.ResourceUpdate[T]{
				Resource:  event.Manifest,
				EventType: event.Type,
			})
		}
	}
}

func (c *Client[T]) Handle(ctx context.Context, event loops.EventClient) error {
	switch e := event.(type) {
	case *loops.ResourceUpdate[T]:
		return c.handleResourceUpdate(ctx, e)
	case *loops.Shutdown:
		c.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown component client event type: %T", e))
	}

	return nil
}

func (c *Client[T]) handleResourceUpdate(ctx context.Context, e *loops.ResourceUpdate[T]) error {
	r := e.Resource
	if r.GetNamespace() != c.namespace {
		return nil
	}

	if c.processSecrets != nil {
		if err := c.processSecrets(ctx, &r, c.namespace, c.kubeClient); err != nil {
			return fmt.Errorf("error processing %s %s secrets from pod %s/%s: %s",
				r.Kind(), r.GetName(), c.namespace, c.podName, err)
		}
	}

	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("error serializing %s %s from pod %s/%s: %s",
			r.Kind(), r.GetName(), c.namespace, c.podName, err)
	}

	c.streamLoop.Enqueue(&loops.StreamSend{
		Resource:  b,
		EventType: e.EventType,
	})

	log.Debugf("updated sidecar with %s %s %s from pod %s/%s", r.Kind(), e.EventType.String(), r.GetName(), c.namespace, c.podName)
	return nil
}

func (c *Client[T]) handleShutdown(e *loops.Shutdown) {
	c.streamLoop.Close(&loops.Shutdown{Error: e.Error})
	stream.LoopFactory.CacheLoop(c.streamLoop)
}
