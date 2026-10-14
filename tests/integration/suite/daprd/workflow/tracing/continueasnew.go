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

package tracing

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(continueasnew))
}

type continueasnew struct {
	wf        *workflow.Workflow
	collector *otel.Collector
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.collector = otel.New(t)

	c.wf = workflow.New(t,
		workflow.WithDaprdOptions(0,
			c.collector.GRPCDaprdConfiguration(t),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.collector, c.wf),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.wf.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("can", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var input string
		require.NoError(t, ctx.GetInput(&input))
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		if input == "first" {
			ctx.ContinueAsNew("second")
		}
		return nil, nil
	})
	reg.AddActivityN("bar", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClientWithLogger(c.wf.Dapr().GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "can",
		dworkflow.WithInstanceID("canid"),
		dworkflow.WithInput("first"),
	)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		var orchestrations []*v1.Span
		activities := make(map[string]int)
		var startTraceID string
		for _, span := range c.collector.GetSpans() {
			for _, scopeSpan := range span.GetScopeSpans() {
				for _, span := range scopeSpan.GetSpans() {
					switch span.GetName() {
					case "orchestration||can":
						orchestrations = append(orchestrations, span)
					case "activity||bar":
						activities[hex.EncodeToString(span.GetTraceId())]++
					case "create_orchestration||can":
						startTraceID = hex.EncodeToString(span.GetTraceId())
					}
				}
			}
		}

		if !assert.Len(col, orchestrations, 2, "expected an orchestration span per generation") {
			return
		}

		gen1 := hex.EncodeToString(orchestrations[0].GetTraceId())
		gen2 := hex.EncodeToString(orchestrations[1].GetTraceId())
		assert.NotEqual(col, gen1, gen2,
			"each ContinueAsNew generation must be rooted in its own trace")

		gens := map[string]bool{gen1: true, gen2: true}
		assert.True(col, gens[startTraceID],
			"one generation must belong to the scheduling client's trace")

		assert.Equal(col, 1, activities[gen1], "first generation's activity must share its trace")
		assert.Equal(col, 1, activities[gen2], "second generation's activity must share its trace")
	}, time.Second*10, time.Millisecond*10)
}
