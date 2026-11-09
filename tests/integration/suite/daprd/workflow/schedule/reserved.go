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

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reserved))
}

// reserved checks that schedules can only be made through the schedule API,
// that it rejects invalid schedules, and that it needs a connected worker.
type reserved struct {
	workflow *workflow.Workflow
}

func (r *reserved) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reserved) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	// No worker is connected yet, so the schedule could never run.
	_, err := r.workflow.WorkflowClient(t, ctx).CreateWorkflowSchedule(ctx, "early", "noop", "@every 1s")
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), err)

	r.workflow.Registry().AddWorkflowN("noop", func(*task.WorkflowContext) (any, error) { return nil, nil })
	client := r.workflow.BackendClient(t, ctx)

	_, err = client.ScheduleNewWorkflow(ctx, "dapr.internal.schedule", api.WithInstanceID("dapr-internal-schedule-x"))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.ScheduleNewWorkflow(ctx, "noop", api.WithInstanceID("dapr-internal-schedule-y"))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = r.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr", WorkflowName: "noop", InstanceId: "dapr-internal-schedule-z",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	for name, c := range map[string]struct {
		name, workflow, schedule string
		opts                     []api.CreateWorkflowScheduleOptions
	}{
		"bad name":         {"a_b", "noop", "@every 1s", nil},
		"bad schedule":     {"a", "noop", "* * *", nil},
		"too frequent":     {"a", "noop", "@every 10ms", nil},
		"no workflow":      {"a", "", "@every 1s", nil},
		"zero repeats":     {"a", "noop", "@every 1s", []api.CreateWorkflowScheduleOptions{api.WithScheduleRepeats(0)}},
		"schedule itself":  {"a", "dapr.internal.schedule", "@every 1s", nil},
		"namespace no app": {"a", "noop", "@every 1s", []api.CreateWorkflowScheduleOptions{api.WithScheduleTargetAppNamespace("ns")}},
	} {
		_, err := client.CreateWorkflowSchedule(ctx, c.name, c.workflow, c.schedule, c.opts...)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}

	resp, err := client.ListWorkflowSchedules(ctx)
	if assert.NoError(t, err) {
		assert.Empty(t, resp.GetSchedules())
	}
}
