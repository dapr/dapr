/*
Copyright 2026 The Dapr Authors
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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(largeactivityresult))
}

type largeactivityresult struct {
	workflow *workflow.Workflow
}

func (l *largeactivityresult) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("10M"),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *largeactivityresult) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	// Generate a payload larger than the default gRPC send limit of 4MB.
	const payloadSize = 5 * 1024 * 1024 // 5MB
	largePayload := strings.Repeat("x", payloadSize)

	l.workflow.Registry().AddOrchestratorN("large-result", func(ctx *task.OrchestrationContext) (any, error) {
		var result string
		err := ctx.CallActivity("produce-large-result").Await(&result)
		return result, err
	})

	l.workflow.Registry().AddActivityN("produce-large-result", func(ctx task.ActivityContext) (any, error) {
		return largePayload, nil
	})

	client := l.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "large-result")
	require.NoError(t, err)

	metadata, err := client.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
}
