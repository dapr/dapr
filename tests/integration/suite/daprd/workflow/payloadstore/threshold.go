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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(threshold))
}

// threshold pins the offload boundary: a payload one byte below the
// configured threshold persists inline, a payload exactly at the
// threshold is offloaded. Sizes are of the SDK-marshaled payload (a JSON
// string, so two quote bytes on top of the raw input).
type threshold struct {
	workflow *workflow.Workflow
}

const thresholdBytes = 1024

func (h *threshold) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.New(t, workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, thresholdBytes)))

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *threshold) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	h.workflow.Registry().AddWorkflowN("boundary", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	})

	client := h.workflow.BackendClient(t, ctx)

	// Raw lengths chosen so the JSON-marshaled payloads ("..." with two
	// quote bytes) land exactly one byte below and exactly at the
	// threshold.
	below := strings.Repeat("b", thresholdBytes-3)
	at := strings.Repeat("a", thresholdBytes-2)
	require.Len(t, fworkflow.JSONString(t, below), thresholdBytes-1)
	require.Len(t, fworkflow.JSONString(t, at), thresholdBytes)

	idBelow, err := client.ScheduleNewWorkflow(ctx, "boundary", api.WithInput(below))
	require.NoError(t, err)
	idAt, err := client.ScheduleNewWorkflow(ctx, "boundary", api.WithInput(at))
	require.NoError(t, err)

	for _, id := range []api.InstanceID{idBelow, idAt} {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	}

	assert.Equal(t, fworkflow.JSONString(t, below),
		fworkflow.FindPayload(t, fworkflow.ReadHistoryEvents(t, ctx, h.workflow.DB(), string(idBelow)), fworkflow.ExecutionStartedPayload),
		"payload one byte below the threshold must persist inline")

	fworkflow.RequireOffloadedPayload(t,
		fworkflow.FindPayload(t, fworkflow.ReadHistoryEvents(t, ctx, h.workflow.DB(), string(idAt)), fworkflow.ExecutionStartedPayload), at)
}
