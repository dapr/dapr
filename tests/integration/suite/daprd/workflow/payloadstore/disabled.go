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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	wfpayloadstore "github.com/dapr/durabletask-go/backend/payloadstore"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(disabled))
}

// disabled asserts the dormancy invariant: without the enablement
// environment variable no payload store exists, so payloads far above the
// default threshold persist inline, no persisted value ever carries the
// reference encoding, and payload data round-trips through workflow
// execution unchanged.
type disabled struct {
	workflow *workflow.Workflow
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	// Twice the 16KiB default threshold: would be offloaded if a store
	// were configured.
	input := strings.Repeat("x", 32*1024)

	d.workflow.Registry().AddWorkflowN("inline", func(ctx *task.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	})

	client := d.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "inline", api.WithInput(input))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// The workflow saw and echoed the real payload.
	assert.Equal(t, fworkflow.JSONString(t, input), meta.GetOutput().GetValue())

	// Every persisted payload is inline; the reference encoding never
	// appears.
	events := fworkflow.ReadHistoryEvents(t, ctx, d.workflow.DB(), string(id))
	assert.Equal(t, fworkflow.JSONString(t, input), fworkflow.FindPayload(t, events, fworkflow.ExecutionStartedPayload))
	assert.Equal(t, fworkflow.JSONString(t, input), fworkflow.FindPayload(t, events, fworkflow.ExecutionCompletedPayload))
	for _, e := range events {
		if p := wfpayloadstore.Payload(e); p != nil {
			assert.False(t, wfpayloadstore.IsReference(p.GetValue()),
				"no persisted payload may be a reference when the store is not enabled")
		}
	}
}
