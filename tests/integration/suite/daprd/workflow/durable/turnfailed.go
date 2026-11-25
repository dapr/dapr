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

package durable

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(turnfailed))
}

// turnfailed fails the save at the end of the first workflow turn and
// requires the worker's CompleteWorkflowTask call to be answered with an
// error. The turn is then run again, saved and answered as a success.
type turnfailed struct {
	workflow *workflow.Workflow
}

func (r *turnfailed) Setup(t *testing.T) []framework.Option {
	r.workflow = newWorkflow(t, "DAPR_WORKFLOW_TEST_FAIL_TURN_SAVES", "1")

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *turnfailed) Run(t *testing.T, ctx context.Context) {
	thub, items, cl := connect(t, ctx, r.workflow)
	id := schedule(t, ctx, cl, "durable-turn-failed")

	_, err := thub.CompleteWorkflowTask(ctx, completeResponse(nextWorkflowItem(t, items)))
	require.Error(t, err, "a turn that was not saved must not be answered as a success")
	assert.Equal(t, codes.Aborted, status.Code(err))

	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.NotEqual(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// The turn runs again and is saved.
	_, err = thub.CompleteWorkflowTask(ctx, completeResponse(nextWorkflowItem(t, items)))
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	meta, err = cl.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
