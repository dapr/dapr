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

package list

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(multipule))
}

type multipule struct {
	workflow *workflow.Workflow
}

func (m *multipule) Setup(t *testing.T) []framework.Option {
	uuid := uuid.NewString()
	m.workflow = workflow.New(t,
		workflow.WithDaprds(5),
		workflow.WithDaprdOptions(0, daprd.WithAppID(uuid)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(uuid)),
		workflow.WithDaprdOptions(2, daprd.WithAppID(uuid)),
		workflow.WithDaprdOptions(3, daprd.WithAppID(uuid)),
		workflow.WithDaprdOptions(4, daprd.WithAppID(uuid)),
	)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *multipule) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, nil
	})

	const numWf = 5

	wfs := make([]*dworkflow.Client, 0, numWf)
	for i := range numWf {
		wfs = append(wfs, m.workflow.WorkflowClientN(t, ctx, i))
		require.NoError(t, wfs[i].StartWorker(ctx, reg))
	}

	ids := make([]string, 0, 20)
	for range 20 {
		id, err := wfs[0].ScheduleWorkflow(ctx, "foo")
		require.NoError(t, err)
		ids = append(ids, id)
	}

	for i := range numWf {
		resp, err := wfs[i].ListInstanceIDs(ctx)
		require.NoError(t, err)
		assert.Equal(t, ids, resp.InstanceIds)
		assert.Nil(t, resp.ContinuationToken)
	}
}
