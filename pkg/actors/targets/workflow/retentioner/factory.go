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

package retentioner

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
)

var retentionerCache = &sync.Pool{
	New: func() any {
		return &retentioner{
			lock: lock.New(),
		}
	},
}

type Options struct {
	Actors            actors.Interface
	WorkflowActorType string
	ActorType         string
}

type factory struct {
	wfActorType string
	actorType   string

	router router.Interface
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	router, err := opts.Actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	return &factory{
		wfActorType: opts.WorkflowActorType,
		actorType:   opts.ActorType,
		router:      router,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	r := retentionerCache.Get().(*retentioner)
	r.factory = f
	r.actorID = actorID
	return r
}

func (f *factory) HaltAll(context.Context) error {
	return nil
}

func (f *factory) HaltNonHosted(context.Context) error {
	return nil
}

func (f *factory) Exists(actorID string) bool {
	return false
}

func (f *factory) Len() int {
	return 0
}
