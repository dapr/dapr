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

package activity

import (
	"context"
	"sync"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/internal/scheduler"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/kit/crypto/spiffe/signer"
)

func newActivity() *activity {
	return &activity{
		lock: lock.New(),
	}
}

type Options struct {
	AppID             string
	Namespace         string
	ActivityActorType string
	WorkflowActorType string
	Scheduler         todo.ActivityScheduler
	Actors            actors.Interface
	ActorTypeBuilder  *common.ActorTypeBuilder
	// Signer produces activity completion attestations so a receiving
	// parent workflow can cryptographically verify the activity's identity,
	// input, and output. Nil when signing is disabled for this deployment.
	Signer *signer.Signer

	// May be nil when the feature is disabled.
	WorkflowAccessPolicies *workflowacl.Holder

	WorkflowsRemoteActivityReminder bool
}

type factory struct {
	appID             string
	actorType         string
	workflowActorType string

	// TODO: @joshvanl: remove in the next version.
	workflowsRemoteActivityReminder bool

	router                 router.Interface
	state                  state.Interface
	reminders              scheduler.Interface
	placement              placement.Interface
	actorTypeBuilder       *common.ActorTypeBuilder
	workflowAccessPolicies *workflowacl.Holder
	signing                *signing.Signing

	scheduler todo.ActivityScheduler

	table sync.Map
	lock  sync.Mutex

	// inflight tracks activity executions whose WorkItem is currently in
	// the durabletask queue or being processed by the SDK. Keyed by the
	// composite (activity actor ID, TaskExecutionId) value produced by
	// inflight.Key. See the inflight subpackage for semantics.
	inflight inflight.Map
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	router, err := opts.Actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	state, err := opts.Actors.State(ctx)
	if err != nil {
		return nil, err
	}

	reminders, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}

	sreminders, err := reminders.Scheduler()
	if err != nil {
		return nil, err
	}

	placement, err := opts.Actors.Placement(ctx)
	if err != nil {
		return nil, err
	}

	return &factory{
		appID:                  opts.AppID,
		actorType:              opts.ActivityActorType,
		router:                 router,
		reminders:              sreminders,
		scheduler:              opts.Scheduler,
		placement:              placement,
		workflowActorType:      opts.WorkflowActorType,
		actorTypeBuilder:       opts.ActorTypeBuilder,
		workflowAccessPolicies: opts.WorkflowAccessPolicies,
		state:                  state,

		signing: &signing.Signing{
			Signer:    opts.Signer,
			Namespace: opts.Namespace,
		},

		workflowsRemoteActivityReminder: opts.WorkflowsRemoteActivityReminder,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	a, ok := f.table.Load(actorID)
	if !ok {
		fresh := newActivity()
		fresh.factory = f
		fresh.actorID = actorID
		a, _ = f.table.LoadOrStore(actorID, fresh)
	}

	return a.(*activity)
}

func (f *factory) HaltAll(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.table.Range(func(key, val any) bool {
		val.(*activity).Deactivate(ctx)
		return true
	})
	f.table.Clear()
	return nil
}

func (f *factory) HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.table.Range(func(key, val any) bool {
		if !fn(&api.LookupActorRequest{
			ActorType: f.actorType,
			ActorID:   key.(string),
		}) {
			val.(*activity).Deactivate(ctx)
			f.table.Delete(key)
		}
		return true
	})
	return nil
}

func (f *factory) Exists(actorID string) bool {
	_, ok := f.table.Load(actorID)
	return ok
}

func (f *factory) Len() int {
	var count int
	f.table.Range(func(_, _ any) bool { count++; return true })
	return count
}
