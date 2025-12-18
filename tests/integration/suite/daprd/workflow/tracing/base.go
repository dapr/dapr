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
	"encoding/hex"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(base))
}

type base struct {
	wf        *workflow.Workflow
	collector *otel.Collector
}

func (b *base) Setup(t *testing.T) []framework.Option {
	b.collector = otel.New(t)

	b.wf = workflow.New(t,
		workflow.WithDaprdOptions(0,
			b.collector.GRPCDaprdConfiguration(t),
		),
	)

	return []framework.Option{
		framework.WithProcesses(b.collector, b.wf),
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	b.wf.WaitUntilRunning(t, ctx)

	tp := b.collector.GRPCProvider(t, ctx)
	tracer := tp.Tracer(t.Name())

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	reg.AddActivityN("bar", func(ctx dworkflow.ActivityContext) (any, error) {
		_, span := tracer.Start(ctx.Context(), "this-is-my-activity")
		span.AddEvent("Started activity")
		span.AddEvent("Finishing activity")
		span.End()

		return nil, nil
	})

	client := dworkflow.NewClient(b.wf.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("helloworld"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		traces := make(map[string][]*v1.Span)
		for _, span := range b.collector.GetSpans() {
			for _, scopeSpan := range span.GetScopeSpans() {
				for _, span := range scopeSpan.GetSpans() {
					traceID := hex.EncodeToString(span.GetTraceId())
					traces[traceID] = append(traces[traceID], span)
				}
			}
		}

		var found bool
		for _, spans := range traces {
			sort.SliceStable(spans, func(i, j int) bool {
				return spans[i].GetStartTimeUnixNano() < spans[j].GetStartTimeUnixNano()
			})

			if len(spans) > 0 && spans[0].GetName() == "/TaskHubSidecarService/StartInstance" {
				found = true
				names := make([]string, len(spans))
				for i, span := range spans {
					names[i] = span.GetName()
				}
				assert.Equal(c, []string{
					"/TaskHubSidecarService/StartInstance",
					"create_orchestration||foo",
					"orchestration||foo",
					"activity||bar",
					"this-is-my-activity",
				}, names)
			}
		}

		assert.True(c, found, "did not find expected workflow start span")
	}, time.Second*10, time.Millisecond*10)
}
