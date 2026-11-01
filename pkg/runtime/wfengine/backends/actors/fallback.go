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

	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/marker"
	"github.com/dapr/dapr/pkg/actors/targets/workflow"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
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

	hostedCtx, hostedCancel := context.WithCancel(factoryCtx)
	factories, err := workflow.Factories(hostedCtx, abe.factoryOptions())
	if err != nil {
		hostedCancel()
		return err
	}
	retireTogether(factories, hostedCancel)

	var execCancel context.CancelFunc
	if executor {
		ef, cancel, eerr := abe.newExecutorFactory(factoryCtx)
		if eerr != nil {
			hostedCancel()
			return eerr
		}
		factories = append(factories, ef)
		execCancel = cancel
	}

	atable.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories:          factories,
		NoReminderDelivery: !executor,
	})

	// The factories these replace are halted by now: released before this
	// call, and halted and retired by RegisterActorTypes if they were still
	// registered.
	replaceCancel(&abe.hostedCancel, hostedCancel)
	if executor {
		replaceCancel(&abe.executorCancel, execCancel)
	}

	return nil
}

// replaceCancel cancels the background work of the factories a registration
// replaced, and keeps the cancel of the new ones. Every registration builds
// new factories, whose background goroutines run until their context is done:
// without it, each fallback transition would leave those of the factories it
// replaced running until shutdown. The table ends that work already for the
// factories it retires (see retireTogether); this covers the ones it does
// not, and a cancel func runs only once. Called under the engine's actor
// registration lock.
func replaceCancel(current *context.CancelFunc, next context.CancelFunc) {
	if *current != nil {
		(*current)()
	}
	*current = next
}

// newExecutorFactory returns the executor actor type's factory, on a context
// of its own under factoryCtx, and the cancel of that context.
func (abe *Actors) newExecutorFactory(factoryCtx context.Context) (table.ActorTypeFactory, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(factoryCtx)
	ef, err := abe.executorFactory(ctx)
	if err != nil {
		cancel()
		return table.ActorTypeFactory{}, nil, err
	}
	factories := []table.ActorTypeFactory{ef}
	retireTogether(factories, cancel)
	return factories[0], cancel, nil
}

// retireTogether has the table end the background work of factories, which
// share the context cancel ends, once it has retired them all: drained, or
// unregistered, and halted. A drained daprd may host nothing again for long,
// so that work cannot wait for the next registration to replace them; nor
// can it end when they are drained, as they serve the calls placement still
// routes here until a round moves their actors.
func retireTogether(factories []table.ActorTypeFactory, cancel context.CancelFunc) {
	shared := &sharedCancel{cancel: cancel}
	shared.left.Store(int32(len(factories))) //nolint:gosec
	for i := range factories {
		factories[i].Factory = &retiringFactory{Factory: factories[i].Factory, shared: shared}
	}
}

// sharedCancel is a context cancel run once the factories sharing the
// context have all retired.
type sharedCancel struct {
	left   atomic.Int32
	cancel context.CancelFunc
}

// retiringFactory is a factory which ends its share of the background work
// once the table retires it.
type retiringFactory struct {
	targets.Factory
	shared  *sharedCancel
	retired atomic.Bool
}

// Retire implements targets.Retirer.
func (f *retiringFactory) Retire() {
	if f.retired.CompareAndSwap(false, true) && f.shared.left.Add(-1) == 0 {
		f.shared.cancel()
	}
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
		ef, execCancel, eerr := abe.newExecutorFactory(factoryCtx)
		if eerr != nil {
			return eerr
		}
		atable.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{ef},
		})
		replaceCancel(&abe.executorCancel, execCancel)
		atable.SetReminderDelivery(true, types...)
		return nil
	}

	// Stop the reminders first: nothing on this daprd can run them any more.
	atable.SetReminderDelivery(false, types...)
	// A halt error comes after the executor actor type left the table: the
	// switch is made all the same, so it is not reported as a failed one,
	// which would make the engine give up the types it must keep hosting.
	if err := atable.UnRegisterActorTypes(abe.executorActorType); err != nil {
		log.Warnf("Failed to halt the executor actors of a daprd turning into a fallback host: %s", err)
	}
	replaceCancel(&abe.executorCancel, nil)
	return nil
}

// DrainActors gives up the workflow, activity, retention and executor actor
// types gracefully under WorkflowsStagedOperations: they leave the placement
// report at once, and keep their actors until a dissemination round moves
// them to the daprds which host them, so the calls routed here meanwhile are
// served rather than failed with the type not registered. See the actor
// table's DrainActorTypes. The factories' background work ends once the table
// retires them; see retireTogether.
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
		factory, err := executor.New(ctx, executor.Options{
			ActorType: abe.executorActorType,
			Actors:    abe.actors,
			Pending:   abe.pendingCompletions,
		})
		if err != nil {
			return table.ActorTypeFactory{}, err
		}
		return table.ActorTypeFactory{Type: abe.executorActorType, Factory: factory}, nil
	}

	return table.ActorTypeFactory{
		Type:    abe.executorActorType,
		Factory: marker.New(abe.executorActorType),
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
