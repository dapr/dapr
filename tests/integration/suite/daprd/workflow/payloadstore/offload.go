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

package payloadstore

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(offload))
}

// offload asserts the persistence-side contract of workflow payload
// offloading through a real daprd: payloads at or above the configured
// threshold are replaced by store references in every persisted history
// row, the checksums embedded in those references match the original
// payloads, and the original payload bytes never reach the state store.
//
// The dereference hook that resolves references back to payloads at the
// SDK boundary lives in durabletask-go and is not part of this repo, so
// the workflow code here never reads offloaded values (no GetInput, and
// Await(nil) for the activity result); only persisted state is asserted.
type offload struct {
	workflow *workflow.Workflow
}

func (o *offload) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t, workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)))

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *offload) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	input := "input-payload-marker-" + strings.Repeat("i", 4096)
	activityInput := "actinput-payload-marker-" + strings.Repeat("n", 4096)
	activityResult := "activity-payload-marker-" + strings.Repeat("a", 4096)
	output := "output-payload-marker-" + strings.Repeat("o", 4096)
	const smallActivityInput = "small-activity-input"

	o.workflow.Registry().AddWorkflowN("payloads", func(ctx *task.WorkflowContext) (any, error) {
		// The activity result is offloaded before it reaches this code, so
		// it must not be unmarshaled here.
		if err := ctx.CallActivity("produce", task.WithActivityInput(activityInput)).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("small", task.WithActivityInput(smallActivityInput)).Await(nil); err != nil {
			return nil, err
		}
		return output, nil
	})
	o.workflow.Registry().AddActivityN("produce", func(task.ActivityContext) (any, error) {
		return activityResult, nil
	})
	o.workflow.Registry().AddActivityN("small", func(task.ActivityContext) (any, error) {
		return nil, nil
	})

	client := o.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "payloads", api.WithInput(input))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	events := fworkflow.ReadHistoryEvents(t, ctx, o.workflow.DB(), string(id))

	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.ExecutionStartedPayload), input)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, taskScheduledInputFor("produce")), activityInput)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.TaskCompletedPayload), activityResult)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.ExecutionCompletedPayload), output)

	// The below-threshold activity input must stay inline, exactly as the
	// SDK marshaled it.
	assert.Equal(t, fworkflow.JSONString(t, smallActivityInput),
		fworkflow.FindPayload(t, events, taskScheduledInputFor("small")))

	// No persisted state-store row may carry the original payload bytes.
	fworkflow.RequireMarkersAbsent(t, ctx, o.workflow.DB(),
		"input-payload-marker-", "actinput-payload-marker-", "activity-payload-marker-", "output-payload-marker-")
}

// taskScheduledInputFor selects the input of the TaskScheduled event for
// the activity with the given name.
func taskScheduledInputFor(name string) func(*protos.HistoryEvent) *wrapperspb.StringValue {
	return func(e *protos.HistoryEvent) *wrapperspb.StringValue {
		if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == name {
			return ts.GetInput()
		}
		return nil
	}
}
