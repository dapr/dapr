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

package tracing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(childwf))
}

type childwf struct {
	wf        *workflow.Workflow
	collector *otel.Collector
}

func (c *childwf) Setup(t *testing.T) []framework.Option {
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

func (c *childwf) Run(t *testing.T, ctx context.Context) {
	c.wf.WaitUntilRunning(t, ctx)

	tp := c.collector.GRPCProvider(t, ctx)
	tracer := tp.Tracer(t.Name())

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallChildWorkflow("bar").Await(nil))
		return nil, nil
	})
	reg.AddWorkflowN("bar", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("car").Await(nil))
		return nil, nil
	})
	reg.AddActivityN("car", func(ctx dworkflow.ActivityContext) (any, error) {
		_, span := tracer.Start(ctx.Context(), "this-is-my-activity")
		span.AddEvent("Started activity")
		span.AddEvent("Finishing activity")
		span.End()

		return nil, nil
	})

	client := dworkflow.NewClient(c.wf.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	cctx, span := tracer.Start(ctx, "schedule-my-workflow")

	id, err := client.ScheduleWorkflow(cctx, "foo", dworkflow.WithInstanceID("helloworld"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	span.End()

	assert.EventuallyWithT(t, func(cc *assert.CollectT) {
		spans := c.collector.TraceSpans(span.SpanContext().TraceID())
		names := make([]string, len(spans))
		for i, span := range spans {
			names[i] = span.GetName()
		}

		assert.Equal(cc, []string{
			"schedule-my-workflow",
			"create_orchestration||foo",
			"orchestration||foo",
			"orchestration||bar",
			"activity||car",
			"this-is-my-activity",
		}, names)
	}, time.Second*10, time.Millisecond*10)
}
