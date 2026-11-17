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

package ordering

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/actors"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/kit/events/loop"
)

var loopFactory = loop.New[any](64)

type Options struct {
	Actors            actors.Interface
	WorkflowActorType string
	ActorType         string
}

type factory struct {
	actorType   string
	wfActorType string

	state     state.Interface
	reminders reminders.Interface
	ctx       context.Context

	// keys holds the loop of each key with calls in flight.
	lock sync.Mutex
	keys map[string]*key
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	astate, err := opts.Actors.State(ctx)
	if err != nil {
		return nil, err
	}
	rem, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}
	return &factory{
		actorType:   opts.ActorType,
		wfActorType: opts.WorkflowActorType,
		state:       astate,
		reminders:   rem,
		ctx:         ctx,
		keys:        make(map[string]*key),
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	return &ordering{factory: f, actorID: actorID}
}

// dispatch hands c to the loop of its key, starting one if the key has none.
func (f *factory) dispatch(id string, c *call) {
	f.lock.Lock()
	defer f.lock.Unlock()
	k, ok := f.keys[id]
	if !ok {
		k = &key{factory: f, id: id}
		k.loop = loopFactory.NewLoop(k)
		f.keys[id] = k
		go k.loop.Run(f.ctx)
	}
	k.pending++
	k.loop.Enqueue(c)
}

// HaltAll stops every key's loop once its calls are done.
func (f *factory) HaltAll(context.Context) error {
	f.lock.Lock()
	keys := f.keys
	f.keys = make(map[string]*key)
	f.lock.Unlock()
	for _, k := range keys {
		k.loop.Close(nil)
	}
	return nil
}

// HaltNonHosted has nothing to stop: a key's loop lives only while it has
// calls in flight, and finish stops it after the last one.
func (f *factory) HaltNonHosted(context.Context, func(*actorapi.LookupActorRequest) bool) error {
	return nil
}

func (f *factory) Exists(actorID string) bool {
	f.lock.Lock()
	defer f.lock.Unlock()
	_, ok := f.keys[actorID]
	return ok
}

func (f *factory) Len() int {
	f.lock.Lock()
	defer f.lock.Unlock()
	return len(f.keys)
}
