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

// Package ordering is a workflow actor's side of its ordering key: it queues a
// keyed instance on its key, holds the instance until the key releases it, and
// gives the key back once the instance is done.
package ordering

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/detached"
	keyactor "github.com/dapr/dapr/pkg/actors/targets/workflow/ordering"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.orchestrator.ordering")

// Ordering serves one workflow instance.
type Ordering struct {
	ActorID   string
	ActorType string
	Router    router.Interface
	Detached  *detached.Runner

	// Save commits the instance's state under the actor lock.
	Save func(context.Context, *wfenginestate.State) error
	// ArmDoneRetry arms the reminder that re-settles the completed instance.
	ArmDoneRetry func(workflowName string) error
}

func (o *Ordering) call(ctx context.Context, method, key string, payload any) ([]byte, error) {
	return keyactor.Call(ctx, o.Router, o.ActorType, method, key, payload)
}

// entry is the instance's key and its entry in the key's queue.
func (o *Ordering) entry(state *wfenginestate.State) (string, keyactor.Entry, error) {
	opts := state.OrderingOptions()
	if opts == nil {
		return "", keyactor.Entry{}, errors.New("workflow has no ordering key")
	}
	e := keyactor.Entry{
		InstanceID:        o.ActorID,
		CreateExecutionID: state.Ordering.GetCreateExecutionId(),
		Policy:            opts.GetOnPredecessorFailure(),
	}
	if es := state.ExecutionStarted(); es != nil {
		e.WorkflowName = es.GetName()
		if ts := es.GetScheduledStartTimestamp(); ts != nil {
			e.DueTime = new(ts.AsTime())
		}
	}
	return opts.GetKey(), e, nil
}

// Enqueue adds the instance to its key's queue, and reports whether it was
// already there.
func (o *Ordering) Enqueue(ctx context.Context, state *wfenginestate.State) (bool, error) {
	key, e, err := o.entry(state)
	if err != nil {
		return false, err
	}
	data, err := o.call(ctx, keyactor.EnqueueMethod, key, e)
	if err != nil {
		return false, err
	}
	var existing bool
	return existing, json.Unmarshal(data, &existing)
}

// EnqueueDetached enqueues a saved instance off the invocation whose own
// enqueue failed. If this one fails too, the client's retry or a status read
// of the instance re-enqueues it, as for any stranded pending start.
func (o *Ordering) EnqueueDetached(state *wfenginestate.State) {
	key, e, err := o.entry(state)
	if err != nil {
		return
	}
	o.Detached.GoKeyed(o.ActorType+"||"+o.ActorID+"||enqueue", func(ctx context.Context) {
		if _, err := o.call(ctx, keyactor.EnqueueMethod, key, e); err != nil {
			log.Warnf("Workflow actor '%s': failed to queue on its ordering key, a status read re-drives it: %v", o.ActorID, err)
		}
	})
}

// Done tells the instance's key that this run no longer holds or waits for
// it, and how it came to. An unkeyed instance owes nothing.
func (o *Ordering) Done(ctx context.Context, state *wfenginestate.State, outcome keyactor.Outcome) error {
	if state == nil || state.OrderingOptions() == nil {
		return nil
	}
	key, e, err := o.entry(state)
	if err != nil {
		return err
	}
	return o.done(ctx, key, e, outcome)
}

func (o *Ordering) done(ctx context.Context, key string, e keyactor.Entry, outcome keyactor.Outcome) error {
	_, err := o.call(ctx, keyactor.DoneMethod, key, keyactor.DoneRequest{Entry: e, Outcome: outcome})
	return err
}

// Purged tells the key that the instance is purged, before its state is
// deleted: that also skips a failure of the instance that holds up the key.
func (o *Ordering) Purged(ctx context.Context, state *wfenginestate.State) error {
	return o.Done(ctx, state, keyactor.OutcomePurged)
}
