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

package input

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(overwrite))
}

type overwrite struct {
	workflow *workflow.Workflow
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallChildWorkflow("bar",
			dworkflow.WithChildWorkflowInput("a-custom-input"),
		).Await(nil))
		return nil, nil
	})
	reg.AddWorkflowN("bar", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, nil
	})

	client := o.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc"))
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	_, err = client.RerunWorkflowFromEvent(ctx, "abc", 0,
		dworkflow.WithRerunNewInstanceID("hello"),
		dworkflow.WithRerunInput("a-different-input"),
	)
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, "hello")
	require.NoError(t, err)

	history, err := client.GetInstanceHistory(ctx, "hello")
	require.NoError(t, err)
	require.Len(t, history.Events, 6)

	exp := &protos.SubOrchestrationInstanceCreatedEvent{
		InstanceId: "hello:0000",
		Name:       "bar",
		Input:      wrapperspb.String(`"a-different-input"`),
		RerunParentInstanceInfo: &protos.RerunParentInstanceInfo{
			InstanceID: "abc",
		},
	}
	assert.True(t, proto.Equal(
		exp,
		history.Events[2].GetSubOrchestrationInstanceCreated(),
	), "%v != %v", exp, history.Events[2].GetSubOrchestrationInstanceCreated())

	ids, err := client.ListInstanceIDs(ctx)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"abc", "abc:0000", "hello", "hello:0000"}, ids.InstanceIds)
}
