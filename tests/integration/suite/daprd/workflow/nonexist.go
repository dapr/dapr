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

package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(nonexist))
}

type nonexist struct {
	workflow *workflow.Workflow
}

func (n *nonexist) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *nonexist) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	client := n.workflow.BackendClient(t, ctx)

	n.workflow.Registry().AddOrchestratorN("dummy", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	id, err := client.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := client.FetchOrchestrationMetadata(ctx, id)
		require.NoError(t, err)
		assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED.String(), meta.RuntimeStatus.String())
	}, time.Second*10, time.Millisecond*10)
}
