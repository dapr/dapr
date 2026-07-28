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
	suite.Register(new(suspendresume))
}

// suspendresume asserts that large user-supplied suspend and resume
// reasons are offloaded in the persisted ExecutionSuspended and
// ExecutionResumed events. Unlike inputs and results, reasons are plain
// strings on the API, not SDK-marshaled JSON, so the raw-payload
// assertion variant is used.
type suspendresume struct {
	workflow *workflow.Workflow
}

func (s *suspendresume) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t, workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)))

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *suspendresume) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	suspendReason := "suspend-reason-marker-" + strings.Repeat("s", 4096)
	resumeReason := "resume-reason-marker-" + strings.Repeat("r", 4096)

	s.workflow.Registry().AddWorkflowN("pausable", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("go", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "pausable")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.NoError(t, client.SuspendWorkflow(ctx, id, suspendReason))
	require.NoError(t, client.ResumeWorkflow(ctx, id, resumeReason))
	require.NoError(t, client.RaiseEvent(ctx, id, "go"))

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	events := fworkflow.ReadHistoryEvents(t, ctx, s.workflow.DB(), string(id))
	fworkflow.RequireOffloadedRawPayload(t,
		fworkflow.FindPayload(t, events, fworkflow.ExecutionSuspendedPayload), []byte(suspendReason))
	fworkflow.RequireOffloadedRawPayload(t,
		fworkflow.FindPayload(t, events, fworkflow.ExecutionResumedPayload), []byte(resumeReason))
	fworkflow.RequireMarkersAbsent(t, ctx, s.workflow.DB(), "suspend-reason-marker-", "resume-reason-marker-")
}
