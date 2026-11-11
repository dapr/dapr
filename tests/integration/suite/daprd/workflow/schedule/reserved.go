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

package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reserved))
}

// reserved checks that schedules can only be made through the schedule API,
// which rejects invalid schedules and needs a connected worker, that no
// rerun reaches a schedule's instance ID, and that an instance under a
// schedule's ID running another workflow is not a schedule.
type reserved struct {
	workflow *workflow.Workflow
}

func (r *reserved) Setup(t *testing.T) []framework.Option {
	// Staged operations would hold the create until a worker connects,
	// instead of failing it.
	r.workflow = workflow.New(t, workflow.WithStagedOperations(false))
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reserved) Run(t *testing.T, ctx context.Context) {
	const noop, every1s = "noop", "@every 1s"

	r.workflow.WaitUntilRunning(t, ctx)

	// No worker is connected yet, so the schedule could never run.
	_, err := r.workflow.WorkflowClient(t, ctx).CreateWorkflowSchedule(ctx, "early", noop, every1s)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), err)

	r.workflow.Registry().AddWorkflowN(noop, func(*task.WorkflowContext) (any, error) { return nil, nil })
	r.workflow.Registry().AddWorkflowN("planter", func(ctx *task.WorkflowContext) (any, error) {
		_, serr := ctx.ScheduleNewDetachedWorkflow(noop, task.WithDetachedWorkflowInstanceID("dapr-internal-schedule-p"))
		return nil, serr
	})
	client := r.workflow.BackendClient(t, ctx)

	// A workflow can start another under a schedule's instance ID, which is
	// then not a schedule.
	_, err = client.ScheduleNewWorkflow(ctx, "planter")
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ferr := client.FetchWorkflowMetadata(ctx, "dapr-internal-schedule-p")
		assert.NoError(c, ferr)
	}, 10*time.Second, 10*time.Millisecond)
	_, err = client.GetWorkflowSchedule(ctx, "p")
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.TriggerWorkflowSchedule(ctx, "p")
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.DeleteWorkflowSchedule(ctx, "p")
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.CreateWorkflowSchedule(ctx, "p", noop, every1s, api.WithScheduleOverwrite())
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
	_, err = client.FetchWorkflowMetadata(ctx, "dapr-internal-schedule-p")
	require.NoError(t, err, "the schedule API leaves the planted instance alone")

	_, err = client.ScheduleNewWorkflow(ctx, "dapr.internal.schedule", api.WithInstanceID("dapr-internal-schedule-x"))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.ScheduleNewWorkflow(ctx, noop, api.WithInstanceID("dapr-internal-schedule-y"))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = r.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr", WorkflowName: noop, InstanceId: "dapr-internal-schedule-z",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = client.RerunWorkflowFromEvent(ctx, "dapr-internal-schedule-x", 0)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.RerunWorkflowFromEvent(ctx, "x", 0, api.WithRerunNewInstanceID("dapr-internal-schedule-x"))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	for name, c := range map[string]struct {
		name, workflow, schedule string
		opts                     []api.CreateWorkflowScheduleOptions
	}{
		"bad name":         {"a_b", noop, every1s, nil},
		"bad schedule":     {"a", noop, "* * *", nil},
		"too frequent":     {"a", noop, "@every 10ms", nil},
		"no workflow":      {"a", "", every1s, nil},
		"zero repeats":     {"a", noop, every1s, []api.CreateWorkflowScheduleOptions{api.WithScheduleRepeats(0)}},
		"schedule itself":  {"a", "dapr.internal.schedule", every1s, nil},
		"namespace no app": {"a", noop, every1s, []api.CreateWorkflowScheduleOptions{api.WithScheduleTargetAppNamespace("ns")}},
		"other namespace": {"a", noop, every1s, []api.CreateWorkflowScheduleOptions{
			api.WithScheduleTargetAppID("b"), api.WithScheduleTargetAppNamespace("other"),
		}},
		"reserved name": {"dapr-internal-schedule-a", noop, every1s, nil},
	} {
		_, err = client.CreateWorkflowSchedule(ctx, c.name, c.workflow, c.schedule, c.opts...)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}

	_, err = protos.NewTaskHubSidecarServiceClient(r.workflow.Dapr().GRPCConn(t, ctx)).
		CreateWorkflowSchedule(ctx, new(protos.CreateWorkflowScheduleRequest))
	assert.Equal(t, codes.InvalidArgument, status.Code(err), "a request without a schedule")

	// daprd is still up, and the planted instance is not listed.
	resp, err := client.ListWorkflowSchedules(ctx)
	if assert.NoError(t, err) {
		assert.Empty(t, resp.GetSchedules())
	}
}
