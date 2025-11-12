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

package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(base))
}

type base struct {
	workflow *workflow.Workflow
}

func (b *base) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "1s"
`)),
	)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc").Await(nil))
		return nil, nil
	})
	reg.AddActivityN("abc", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(b.workflow.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	_, err := client.ScheduleWorkflow(ctx, "foo")
	require.NoError(t, err)

	db := b.workflow.DB().GetConnection(t)
	tableName := b.workflow.DB().TableName()

	var count int
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
		assert.Equal(c, 0, count)
		assert.Empty(c, b.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*10, time.Millisecond*10)
}
