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
	"fmt"

	keyactor "github.com/dapr/dapr/pkg/actors/targets/workflow/ordering"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/kit/ptr"
)

// Deliver sends the Done a completed instance owes and records it. A failure
// arms the retry reminder, unless that reminder drove the call: its nack then
// retries under its failure policy.
func (o *Ordering) Deliver(ctx context.Context, state *wfenginestate.State, arm bool) error {
	if !state.OrderingDoneOwed() {
		return nil
	}
	if err := o.Done(ctx, state, outcome(state)); err != nil {
		if arm {
			if rerr := o.ArmDoneRetry(state.ExecutionStarted().GetName()); rerr != nil {
				err = fmt.Errorf("%w (and to arm the retry reminder: %v)", err, rerr)
			}
		}
		return wferrors.NewRecoverable(err)
	}
	state.Ordering.Done = ptr.Of(true)
	if err := o.Save(ctx, state); err != nil {
		return wferrors.NewRecoverable(fmt.Errorf("failed to record the ordering key's release: %w", err))
	}
	return nil
}

// Recreate settles the instance's key before its instance ID is reused for
// next: the old run delivers its Done, and when next leaves the key, or takes
// another one, the old run is skipped on its key, as if purged.
func (o *Ordering) Recreate(ctx context.Context, state *wfenginestate.State, next *protos.ExecutionStartedEvent) error {
	if err := o.Deliver(ctx, state, true); err != nil {
		return err
	}
	if opts := state.OrderingOptions(); opts != nil && opts.GetKey() != next.GetOrdering().GetKey() {
		return o.Purged(ctx, state)
	}
	return nil
}

// outcome is how a completed instance came to no longer hold its key: one
// that was never released never ran, so it is nobody's predecessor.
func outcome(state *wfenginestate.State) keyactor.Outcome {
	switch {
	case !state.Ordering.GetReleased():
		return keyactor.OutcomeSkipped
	case state.History[len(state.History)-1].GetExecutionCompleted().GetWorkflowStatus() == protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED:
		return keyactor.OutcomeSucceeded
	}
	return keyactor.OutcomeFailed
}
