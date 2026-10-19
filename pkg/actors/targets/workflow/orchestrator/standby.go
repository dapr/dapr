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

package orchestrator

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
)

// errStandbyTurn refuses a reminder on a standby host (Options.Standby): it
// would run a workflow turn, and no worker is connected to run it. A standby
// host does not advertise the workflow actor type to the scheduler, so a
// reminder reaches it only when another daprd forwards one through a stale
// placement table while a worker connects. FailedPrecondition fails the
// trigger, and the reminder's retry policy delivers it again once the table
// routes the actor to the daprd with the worker.
func errStandbyTurn(actorID, reminder string) error {
	return status.Errorf(codes.FailedPrecondition,
		"workflow actor '%s' is on a standby host with no workflow worker connected; not running reminder '%s'",
		actorID, reminder)
}

// armStandbyJanitor stands in on a standby host for dispatching the
// activities a rerun schedules: the standby activity actors refuse every
// call, as no worker is connected to run one. It arms the instance's janitor
// due at once. The standby host does not advertise the workflow actor type to
// the scheduler, so the janitor fires once a worker connects, on the daprd
// with the worker, where its re-dispatch of unresolved tasks sends the
// activities.
func (o *orchestrator) armStandbyJanitor(ctx context.Context, state *wfenginestate.State) error {
	wfName := o.getExecutionStartedEvent(state).GetName()
	return common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType: o.actorTypeBuilder.Workflow(o.appID),
		ActorID:   o.actorID,
		Name:      janitorReminderName,
		DueTime:   time.Now().UTC().Format(time.RFC3339Nano),
		Period:    janitorPeriod().String(),
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Drop{
				Drop: new(commonv1pb.JobFailurePolicyDrop),
			},
		},
		ConcurrencyKey: &wfName,
	})
}
