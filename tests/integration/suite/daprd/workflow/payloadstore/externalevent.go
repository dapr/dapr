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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(externalevent))
}

// externalevent asserts that a large external-event payload raised into a
// running workflow is offloaded when its EventRaised history event is
// persisted.
type externalevent struct {
	workflow *workflow.Workflow
}

func (e *externalevent) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t, workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)))

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *externalevent) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	eventPayload := "event-payload-marker-" + strings.Repeat("e", 4096)

	e.workflow.Registry().AddWorkflowN("waiter", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("bigdata", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	client := e.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "waiter")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.NoError(t, client.RaiseEvent(ctx, id, "bigdata", api.WithEventPayload(eventPayload)))

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	events := fworkflow.ReadHistoryEvents(t, ctx, e.workflow.DB(), string(id))
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.EventRaisedPayload), eventPayload)
	fworkflow.RequireMarkersAbsent(t, ctx, e.workflow.DB(), "event-payload-marker-")
}
