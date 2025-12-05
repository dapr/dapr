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
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
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
	lock    *lock.Lock
}

func (r *retentioner) InvokeMethod(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	return nil, errors.New("invoke not implemented")
}

func (r *retentioner) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	unlock, err := r.lock.ContextLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	defer r.Deactivate(ctx)

	log.Debugf("Invoking retention purge reminder for workflow instance %s", r.actorID)

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.PurgeWorkflowStateMethod).
		WithMetadata(map[string][]string{
			todo.MetadataPurgeRetentionCall: {"true"},
		}).
		WithActor(r.wfActorType, r.actorID)

	start := time.Now()
	_, err = r.router.Call(ctx, req)
	elapsed := diag.ElapsedSince(start)
	if err != nil && !strings.HasSuffix(err.Error(), api.ErrInstanceNotFound.Error()) {
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
	retentionerCache.Put(r)
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
