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

package child

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(async))
}

type async struct {
	workflow *workflow.Workflow
}

func (a *async) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *async) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	var calls atomic.Int64
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		c1 := ctx.CallChildWorkflow("bar")
		c2 := ctx.CallChildWorkflow("bar")
		require.NoError(t, c1.Await(nil))
		require.NoError(t, c2.Await(nil))
		return nil, nil
	})
	reg.AddWorkflowN("bar", func(ctx *dworkflow.WorkflowContext) (any, error) {
		time.Sleep(time.Second * 3)
		calls.Add(1)
		return nil, nil
	})

	client := a.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc"))
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, int64(2), calls.Load())

	_, err = client.RerunWorkflowFromEvent(ctx, "abc", 1, dworkflow.WithRerunNewInstanceID("hello"))
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, "hello")
	require.NoError(t, err)

	history, err := client.GetInstanceHistory(ctx, "hello")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(history.Events), 8)

	assert.True(t, proto.Equal(
		history.Events[2].GetSubOrchestrationInstanceCreated(),
		&protos.SubOrchestrationInstanceCreatedEvent{
			InstanceId: "hello:0000",
			Name:       "bar",
		},
	))

	assert.True(t, proto.Equal(
		history.Events[3].GetSubOrchestrationInstanceCreated(),
		&protos.SubOrchestrationInstanceCreatedEvent{
			InstanceId: "hello:0001",
			Name:       "bar",
			RerunParentInstanceInfo: &protos.RerunParentInstanceInfo{
				InstanceID: "abc",
			},
		},
	))

	ids, err := client.ListInstanceIDs(ctx)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"abc", "abc:0000", "abc:0001", "hello", "hello:0000", "hello:0001"}, ids.InstanceIds)
	assert.Equal(t, int64(4), calls.Load())
}
