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

package actors

import (
	"context"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Standby hosts (the WorkflowsStandbyHosts preview): while no workflow worker
// is connected, a daprd registers the workflow actor type as a standby host.
// Placement routes the type to standby hosts only while no daprd of the app
// has a worker, so starts, events, status queries and purges keep working on
// the normal path. A standby host runs no turn: the type is not advertised to
// the scheduler, so the reminders armed on it fire once a worker connects
// somewhere. The activity and retention actor types are registered as standby
// hosts too, so the workflow actor can delete their reminders when it purges
// an instance, but their actors refuse every call: running an activity needs
// a worker, and both are otherwise reached only from turns or their own
// reminders.

// RegisterStandbyActors registers the workflow, activity and retention actor
// types as a standby host.
// ctx bounds the factory's background work, as for RegisterActors.
func (abe *Actors) RegisterStandbyActors(ctx context.Context) error {
	atable, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	oopts := abe.orchestratorOptions()
	oopts.FastPath = false
	oopts.Standby = true

	factory, err := orchestrator.New(ctx, oopts)
	if err != nil {
		return err
	}

	atable.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories: []table.ActorTypeFactory{
			{Type: abe.workflowActorType, Factory: factory},
			{Type: abe.activityActorType, Factory: refusingFactory{}},
			{Type: abe.retentionerActorType, Factory: refusingFactory{}},
		},
		Standby: true,
	})

	return nil
}

// refusingFactory is the factory of a standby actor type whose actors cannot
// serve anything without a worker. Its actors hold no state, so it tracks
// none.
type refusingFactory struct{}

func (refusingFactory) GetOrCreate(actorID string) targets.Interface {
	return refusingActor{id: actorID}
}
func (refusingFactory) Exists(string) bool            { return false }
func (refusingFactory) HaltAll(context.Context) error { return nil }
func (refusingFactory) Len() int                      { return 0 }
func (refusingFactory) HaltNonHosted(context.Context, func(*actorsapi.LookupActorRequest) bool) error {
	return nil
}

// refusingActor fails every call with FailedPrecondition, the code a caller
// gets when no host has the actor type: callers retry as they would then.
type refusingActor struct {
	id string
}

func (a refusingActor) refuse() error {
	return status.Errorf(codes.FailedPrecondition, "actor '%s' is on a standby host with no workflow worker connected", a.id)
}

func (a refusingActor) Key() string  { return a.id }
func (a refusingActor) Type() string { return "" }
func (a refusingActor) ID() string   { return a.id }

func (a refusingActor) InvokeMethod(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	return nil, a.refuse()
}

func (a refusingActor) InvokeReminder(context.Context, *actorsapi.Reminder) error {
	return a.refuse()
}

func (a refusingActor) InvokeTimer(context.Context, *actorsapi.Reminder) error {
	return a.refuse()
}

func (a refusingActor) InvokeStream(context.Context, *internalsv1pb.InternalInvokeRequest, func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
	return a.refuse()
}

func (a refusingActor) Deactivate(context.Context) error { return nil }

type standbyMarkerKey struct{}

// WithStandbyMarker returns ctx carrying a marker that records whether an
// operation made with it was served by a standby host.
// WatchWorkflowRuntimeStatus returns at once under a marked ctx: the instance
// does not change until a worker connects, so the durabletask executor's wait
// after a start, terminate, suspend or resume would block the caller until
// its deadline.
func WithStandbyMarker(ctx context.Context) context.Context {
	return context.WithValue(ctx, standbyMarkerKey{}, new(atomic.Bool))
}

// markStandby records on ctx's marker, if any, that res was served by a
// standby host.
func markStandby(ctx context.Context, res *internalsv1pb.InternalInvokeResponse) {
	if !common.IsStandby(res) {
		return
	}
	if m, ok := ctx.Value(standbyMarkerKey{}).(*atomic.Bool); ok {
		m.Store(true)
	}
}

// servedByStandby returns whether an operation made with ctx was served by a
// standby host.
func servedByStandby(ctx context.Context) bool {
	m, ok := ctx.Value(standbyMarkerKey{}).(*atomic.Bool)
	return ok && m.Load()
}
