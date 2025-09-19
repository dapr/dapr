/*
Copyright 2024 The Dapr Authors
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

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/lock"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.orchestrator")

type EventSink func(*backend.OrchestrationMetadata)

type orchestrator struct {
	*factory

	actorID string

	state  *wfenginestate.State
	rstate *backend.OrchestrationRuntimeState
	ometa  *backend.OrchestrationMetadata

	activityResultAwaited atomic.Bool
	lock                  *lock.Lock
	closed                atomic.Bool
	wg                    sync.WaitGroup

	streamFns map[int64]*streamFn
	streamIDx int64
}

type streamFn struct {
	fn    func(*internalsv1pb.InternalInvokeResponse) (bool, error)
	errCh chan error
	done  atomic.Bool
}

// InvokeMethod implements actors.InternalActor
func (o *orchestrator) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	o.wg.Add(1)
	defer o.wg.Done()

	unlock, err := o.lock.ContextLock(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke method for workflow '%s': %w", o.actorID, err)
	}
	defer unlock()

	return o.handleInvoke(ctx, req)
}

// InvokeReminder implements actors.InternalActor
func (o *orchestrator) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	o.wg.Add(1)
	defer o.wg.Done()

	unlock, err := o.lock.ContextLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to invoke reminder for workflow '%s': %w", o.actorID, err)
	}
	defer unlock()

	return o.handleReminder(ctx, reminder)
}

// InvokeTimer implements actors.InternalActor
func (o *orchestrator) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (o *orchestrator) InvokeStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
	o.wg.Add(1)
	defer o.wg.Done()

	unlock, err := o.lock.ContextLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to invoke reminder for workflow '%s': %w", o.actorID, err)
	}

	var ok bool
	ok, err = o.handleStream(ctx, req, stream, unlock)
	if !ok {
		unlock()
	}
	return err
}

// DeactivateActor implements actors.InternalActor
func (o *orchestrator) Deactivate(ctx context.Context) error {
	unlock, err := o.lock.ContextLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to deactivate workflow '%s': %w", o.actorID, err)
	}
	defer unlock()

	o.table.Delete(o.actorID)
	o.state = nil
	o.rstate = nil
	o.ometa = nil
	o.lock.Close()
	for _, stream := range o.streamFns {
		stream.errCh <- targeterrors.NewClosed("deactivated")
	}
	clear(o.streamFns)
	o.wg.Wait()
	orchestratorCache.Put(o)

	return nil
}

// Key returns the key for this unique actor.
func (o *orchestrator) Key() string {
	return o.actorType + actorapi.DaprSeparator + o.actorID
}

// Type returns the type for this unique actor.
func (o *orchestrator) Type() string {
	return o.actorType
}

// ID returns the ID for this unique actor.
func (o *orchestrator) ID() string {
	return o.actorID
}
