/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rerun

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(suspend))
}

type suspend struct {
	workflow *workflow.Workflow
}

func (s *suspend) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *suspend) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	var act atomic.Int64
	waitCh := make(chan struct{})
	s.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallActivity("wait").Await(nil))
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	s.workflow.Registry().AddActivityN("wait", func(ctx task.ActivityContext) (any, error) {
		<-waitCh
		return nil, nil
	})
	s.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		act.Add(1)
		return nil, nil
	})

	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("abc"))
	require.NoError(t, err)
	require.NoError(t, client.SuspendOrchestration(ctx, id, "because"))
	meta, err := client.FetchOrchestrationMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_SUSPENDED, meta.GetRuntimeStatus())
	close(waitCh)
	assert.Equal(t, int64(0), act.Load())

	_, err = client.RerunWorkflowFromEvent(ctx, api.InstanceID("abc"), 0)
	assert.Equal(t, status.Error(codes.InvalidArgument, "'abc' is not in a terminal state"), err)

	require.NoError(t, client.ResumeOrchestration(ctx, id, "because"))
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), act.Load())

	newID, err := client.RerunWorkflowFromEvent(ctx, api.InstanceID("abc"), 0)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, newID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), act.Load())
}
