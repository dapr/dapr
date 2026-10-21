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
	"errors"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Fallback hosts (the WorkflowsStagedOperations preview): a daprd hosts the
// workflow, activity and retention actor types if it has a local executor
// (a workflow worker connected, or an in-process executor), or if no daprd of
// its app has one. A daprd with a local executor also registers the executor
// actor type, which is how the app's other daprds learn that one exists: it
// is the type WorkflowsClusteredDeployment registers on exactly those daprds
// already. A daprd hosting the workflow types without a local executor is a
// fallback host. Its actors are the normal ones and serve every operation,
// but it does not advertise the types to the scheduler for reminder
// delivery, and a turn on it fails at once: it has nothing to run workflow
// code with. The reminders its actors arm therefore fire on a daprd with an
// executor, which forwards them to the actor's owner until ownership moves
// there. The engine decides when to register what; see wfengine.

// errNoLocalExecutor fails a workflow or activity turn on a daprd with no
// local executor. The turn's reminder is retried, and lands on a daprd with
// an executor once the placement tables move the actor there.
var errNoLocalExecutor = errors.New("no workflow worker is connected to this daprd")

// requireLocalExecutor fails fast under WorkflowsStagedOperations when this
// daprd has no local executor. Without it the work item would be queued for
// a worker that is not connected: the pending tracker cancels its
// completion, but the item stays in the executor's queue, is handed to the
// next worker to connect, and once the queue is full blocks the turn until
// its deadline.
func (abe *Actors) requireLocalExecutor() error {
	if abe.fallbackHosts && !abe.pendingTasksBackend.ExecutorAvailable() {
		return errNoLocalExecutor
	}
	return nil
}

// fallbackFunc returns the factories' Fallback option: nil with the feature
// off.
func (abe *Actors) fallbackFunc() func() bool {
	if !abe.fallbackHosts {
		return nil
	}
	return func() bool { return !abe.pendingTasksBackend.ExecutorAvailable() }
}

type fallbackMarkerKey struct{}

// WithFallbackMarker returns ctx carrying a marker that records whether an
// operation made with it was served by a fallback host.
// WatchWorkflowRuntimeStatus returns at once under a marked ctx: the instance
// does not change until it moves to a daprd with an executor, so the
// durabletask executor's wait after a start, rerun, terminate, suspend or
// resume would block the caller until its deadline.
func WithFallbackMarker(ctx context.Context) context.Context {
	return context.WithValue(ctx, fallbackMarkerKey{}, new(atomic.Bool))
}

// markFallback records on ctx's marker, if any, that res was served by a
// fallback host.
func markFallback(ctx context.Context, res *internalsv1pb.InternalInvokeResponse) {
	if !common.IsFallback(res) {
		return
	}
	if m, ok := ctx.Value(fallbackMarkerKey{}).(*atomic.Bool); ok {
		m.Store(true)
	}
}

// servedByFallback returns whether an operation made with ctx was served by
// a fallback host.
func servedByFallback(ctx context.Context) bool {
	m, ok := ctx.Value(fallbackMarkerKey{}).(*atomic.Bool)
	return ok && m.Load()
}

// HasLocalExecutor reports whether this daprd has a local executor, under
// WorkflowsStagedOperations. Always true with the feature off.
func (abe *Actors) HasLocalExecutor() bool {
	return abe.requireLocalExecutor() == nil
}

// RegisterHostedActors registers the workflow, activity and retention actor
// types under WorkflowsStagedOperations. With executor set, this daprd has a
// local executor: the executor actor type is registered too and the types
// are advertised to the scheduler. Without, it is a fallback host. ctx bounds
// the wait for the actor runtime; factoryCtx is the lifetime of the
// factories, which may outlive the call that registered them, as they stay
// registered when the daprd turns into a fallback host or back.
func (abe *Actors) RegisterHostedActors(ctx, factoryCtx context.Context, executor bool) error {
	atable, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	opts := abe.factoryOptions()
	opts.Executor = nil
	factories, err := workflow.Factories(factoryCtx, opts)
	if err != nil {
		return err
	}

	if executor {
		ef, eerr := abe.executorFactory(factoryCtx)
		if eerr != nil {
			return eerr
		}
		factories = append(factories, ef)
	}

	atable.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories:          factories,
		NoReminderDelivery: !executor,
	})

	return nil
}

// SetLocalExecutor switches a daprd registered by RegisterHostedActors in
// place between a host with a local executor and a fallback host: the types
// stay registered with the same actors throughout. Gaining an executor
// registers the executor actor type and advertises the types to the
// scheduler; losing it stops advertising them and unregisters the executor
// actor type.
func (abe *Actors) SetLocalExecutor(ctx, factoryCtx context.Context, executor bool) error {
	atable, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	types := []string{abe.workflowActorType, abe.activityActorType, abe.retentionerActorType}

	if executor {
		ef, eerr := abe.executorFactory(factoryCtx)
		if eerr != nil {
			return eerr
		}
		atable.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{ef},
		})
		atable.SetReminderDelivery(true, types...)
		return nil
	}

	// Stop the reminders first: nothing on this daprd can run them any more.
	atable.SetReminderDelivery(false, types...)
	return atable.UnRegisterActorTypes(abe.executorActorType)
}

// DrainActors gives up the workflow, activity, retention and executor actor
// types gracefully under WorkflowsStagedOperations: they leave the placement
// report at once, and keep their actors until a dissemination round moves
// them to the daprds which host them, so the calls routed here meanwhile are
// served rather than failed with the type not registered. See the actor
// table's DrainActorTypes.
func (abe *Actors) DrainActors(ctx context.Context) error {
	atable, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	atable.DrainActorTypes(
		abe.workflowActorType,
		abe.activityActorType,
		abe.retentionerActorType,
		abe.executorActorType,
	)
	return nil
}

// executorFactory returns the executor actor type's factory. Under
// WorkflowsClusteredDeployment it is the executor actor, which shares the
// pending tasks of the daprds with an executor. Otherwise nothing calls the
// type, which is registered only so the app's other daprds see that this
// one has an executor.
func (abe *Actors) executorFactory(ctx context.Context) (table.ActorTypeFactory, error) {
	if abe.enableClusteredDeployment {
		factory, err := executor.New(ctx, abe.executorOptions())
		if err != nil {
			return table.ActorTypeFactory{}, err
		}
		return table.ActorTypeFactory{Type: abe.executorActorType, Factory: factory}, nil
	}

	return table.ActorTypeFactory{
		Type:    abe.executorActorType,
		Factory: presenceFactory{actorType: abe.executorActorType},
	}, nil
}

// WatchExecutorPresence returns a channel which receives whether a daprd of
// this app other than this one has a local executor: whether another daprd
// hosts the executor actor type. See placement's WatchRemoteHosts.
func (abe *Actors) WatchExecutorPresence(ctx context.Context) (<-chan bool, error) {
	p, err := abe.actors.Placement(ctx)
	if err != nil {
		return nil, err
	}
	return p.WatchRemoteHosts(ctx, abe.executorActorType), nil
}

// ExecutorActorType returns the executor actor type.
func (abe *Actors) ExecutorActorType() string {
	return abe.executorActorType
}

// presenceFactory is the executor actor type's factory without
// WorkflowsClusteredDeployment: the type only marks this daprd as one with
// an executor. Nothing calls its actors, which hold no state.
type presenceFactory struct {
	actorType string
}

func (f presenceFactory) GetOrCreate(actorID string) targets.Interface {
	return presenceActor{actorType: f.actorType, id: actorID}
}
func (presenceFactory) Exists(string) bool            { return false }
func (presenceFactory) HaltAll(context.Context) error { return nil }
func (presenceFactory) Len() int                      { return 0 }
func (presenceFactory) HaltNonHosted(context.Context, func(*actorsapi.LookupActorRequest) bool) error {
	return nil
}

type presenceActor struct {
	actorType string
	id        string
}

func (a presenceActor) unimplemented() error {
	return status.Errorf(codes.Unimplemented, "actor '%s' only marks a daprd with a workflow executor", a.Key())
}

func (a presenceActor) Key() string  { return a.actorType + actorsapi.DaprSeparator + a.id }
func (a presenceActor) Type() string { return a.actorType }
func (a presenceActor) ID() string   { return a.id }

func (a presenceActor) InvokeMethod(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	return nil, a.unimplemented()
}

func (a presenceActor) InvokeReminder(context.Context, *actorsapi.Reminder) error {
	return a.unimplemented()
}

func (a presenceActor) InvokeTimer(context.Context, *actorsapi.Reminder) error {
	return a.unimplemented()
}

func (a presenceActor) InvokeStream(context.Context, *internalsv1pb.InternalInvokeRequest, func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
	return a.unimplemented()
}

func (presenceActor) Deactivate(context.Context) error { return nil }
