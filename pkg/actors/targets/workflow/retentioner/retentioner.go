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
	"errors"
	"strings"
	"time"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.retentioner")

type retentioner struct {
	*factory
	actorID string
}

func (r *retentioner) InvokeMethod(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	return nil, errors.New("invoke not implemented")
}

func (r *retentioner) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Invoking retention purge reminder for workflow instance %s", r.actorID)

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.PurgeWorkflowStateMethod).
		WithMetadata(map[string][]string{
			todo.MetadataPurgeRetentionCall: {"true"},
		}).
		WithActor(r.wfActorType, r.actorID)

	start := time.Now()
	_, err := r.router.Call(ctx, req)
	elapsed := diag.ElapsedSince(start)
	// The drain-silently set covers the cases where this reminder is no
	// longer applicable to its target run:
	//   - ErrInstanceNotFound: the run was already purged out-of-band.
	//   - ErrNotCompleted: the run was superseded by a fresh re-schedule
	//     of the same deterministic ID that has not yet completed; the
	//     fresh run will queue its own retention reminder on completion.
	//   - ErrStalled / targeterrors stalled: the run (or a superseding
	//     re-schedule) is currently stalled. The stalled run, on its
	//     next successful tick, will either complete and queue a fresh
	//     retention reminder or stay stalled until operator
	//     intervention; in either case the previous run's anchored
	//     reminder is no longer the right driver.
	// Returning any of these here would put the reminder into a tight
	// retry loop via its MaxRetries=nil failure policy.
	if err != nil &&
		!strings.HasSuffix(err.Error(), api.ErrInstanceNotFound.Error()) &&
		!strings.HasSuffix(err.Error(), api.ErrNotCompleted.Error()) &&
		!strings.HasSuffix(err.Error(), api.ErrStalled.Error()) &&
		!targeterrors.IsStalled(err) {
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.PurgeWorkflow, diag.StatusFailed, elapsed)
		return err
	}

	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.PurgeWorkflow, diag.StatusSuccess, elapsed)

	return nil
}

func (r *retentioner) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (r *retentioner) Deactivate(context.Context) error {
	return nil
}

func (r *retentioner) InvokeStream(ctx context.Context,
	req *internalsv1pb.InternalInvokeRequest,
	stream func(*internalsv1pb.InternalInvokeResponse) (bool, error),
) error {
	return errors.New("invoke stream is not implemented")
}

func (r *retentioner) Key() string {
	return r.actorType + actorapi.DaprSeparator + r.actorID
}

func (r *retentioner) Type() string {
	return r.actorType
}

func (r *retentioner) ID() string {
	return r.actorID
}
