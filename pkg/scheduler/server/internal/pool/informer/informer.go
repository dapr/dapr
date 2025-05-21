/*
Copyright 2025 The Dapr Authors
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

package informer

import (
	"context"
	"fmt"

	"github.com/diagridio/go-etcd-cron/api"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
)

type Options struct {
	ConsumerSink <-chan *api.InformerEvent
	ControlLoop  loop.Interface[loops.Event]
}

type Informer struct {
	sink <-chan *api.InformerEvent
	loop loop.Interface[loops.Event]
}

func New(opts Options) *Informer {
	return &Informer{
		sink: opts.ConsumerSink,
		loop: opts.ControlLoop,
	}
}

func (i *Informer) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			go func() {
				// drain the sink channel to avoid blocking shutdown. All event can be
				// ignored.
				for range i.sink {
					fmt.Printf(">>IGNORING INFORMER event\n")
				}
			}()
			return ctx.Err()

		case event := <-i.sink:
			if err := i.handle(ctx, event); err != nil {
				return err
			}
		}
	}
}

func (i *Informer) handle(ctx context.Context, event *api.InformerEvent) error {
	switch ev := event.Event.(type) {
	case *api.InformerEvent_Delete:
		i.handleDelete(ev.Delete)
	case *api.InformerEvent_DropAll:
		i.handleDropAll()
	case *api.InformerEvent_Put:
		// ignore until broadcast support.
	default:
		return fmt.Errorf("unknown informer event type: %T", event.Event)
	}
	return nil
}

// handleDelete handles the delete event from the informer.
func (i *Informer) handleDelete(job *api.InformerEventJob) {
	fmt.Printf(">>HANDING INFORMER DELETE event: %s\n", job.Name)
	i.loop.Enqueue(&loops.EventJobDelete{Key: job.GetName()})
}

// handleDropAll handles the drop all event from the informer.
func (i *Informer) handleDropAll() {
	fmt.Printf(">>HANDING INFORMER DROPALL event\n")
	i.loop.Enqueue(new(loops.EventJobDropAll))
}
