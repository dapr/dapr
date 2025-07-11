/*
Copyright 2025 The Dapr Authors
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

package security

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(security))
}

type security struct {
	workflow *workflow.Workflow
}

func (c *security) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2))

	// App0 workflow that processes data locally
	c.workflow.Registry().AddOrchestratorN("app0Workflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app0: %w", err)
		}
		return "Processed by app0: " + input, nil
	})

	// App1 workflow that processes data locally
	c.workflow.RegistryN(1).AddOrchestratorN("app1Workflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}
		return "Processed by app1: " + input, nil
	})

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *security) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	// Start workflow listeners for each app with their respective registries
	client0 := c.workflow.BackendClient(t, ctx)
	client1 := c.workflow.BackendClientN(t, ctx, 1)

	id1, err := client0.ScheduleNewOrchestration(ctx, "app0Workflow", api.WithInput("Hello from app0"))
	require.NoError(t, err)
	id2, err := client1.ScheduleNewOrchestration(ctx, "app1Workflow", api.WithInput("Hello from app1"))
	require.NoError(t, err)

	_, err = client0.WaitForOrchestrationStart(ctx, id1)
	require.NoError(t, err)
	_, err = client1.WaitForOrchestrationStart(ctx, id2)
	require.NoError(t, err)

	// Test that app1 cannot access app0's orchestration metadata or orchestration
	_, err = client1.FetchOrchestrationMetadata(ctx, id1, api.WithFetchPayloads(true))
	require.Error(t, err, "app1 should not be able to access app0's orchestration metadata")
	err = client1.SuspendOrchestration(ctx, id1, "myreason")
	require.Error(t, err, "app1 should not be able to control app0's orchestration")

	// Test that app0 cannot access app1's orchestration metadata or orchesstration
	_, err = client0.FetchOrchestrationMetadata(ctx, id2, api.WithFetchPayloads(true))
	require.Error(t, err, "app0 should not be able to access app1's orchestration metadata")
	err = client0.SuspendOrchestration(ctx, id2, "myreason")
	require.Error(t, err, "app0 should not be able to control app1's orchestration")

	// Wait for both workflows
	metadata1, err := client0.WaitForOrchestrationCompletion(ctx, id1, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata1))
	assert.Equal(t, `"Processed by app0: Hello from app0"`, metadata1.GetOutput().GetValue())
	metadata2, err := client1.WaitForOrchestrationCompletion(ctx, id2, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata2))
	assert.Equal(t, `"Processed by app1: Hello from app1"`, metadata2.GetOutput().GetValue())
}
