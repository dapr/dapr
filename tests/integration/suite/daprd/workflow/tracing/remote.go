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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(remote))
}

type remote struct {
	wf        *workflow.Workflow
	collector *otel.Collector
	n         int
}

func (r *remote) Setup(t *testing.T) []framework.Option {
	r.n = 10

	r.collector = otel.New(t)

	uuid, err := uuid.NewRandom()
	require.NoError(t, err)

	opts := []daprd.Option{
		r.collector.GRPCDaprdConfiguration(t),
		daprd.WithAppID(uuid.String()),
	}

	wfopts := make([]workflow.Option, r.n+1)
	wfopts[0] = workflow.WithDaprds(r.n)
	for i := range r.n {
		wfopts[i+1] = workflow.WithDaprdOptions(i, opts...)
	}

	r.wf = workflow.New(t, wfopts...)

	return []framework.Option{
		framework.WithProcesses(r.collector, r.wf),
	}
}

func (r *remote) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)

	tp := r.collector.GRPCProvider(t, ctx)
	tracer := tp.Tracer(t.Name())

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		for range r.n {
			require.NoError(t, ctx.CallActivity("bar").Await(nil))
		}
		return nil, nil
	})

	var count atomic.Int64
	reg.AddActivityN("bar", func(ctx dworkflow.ActivityContext) (any, error) {
		_, span := tracer.Start(ctx.Context(), "this-is-my-activity-"+strconv.FormatInt(count.Add(1), 10))
		span.AddEvent("Started activity")
		span.AddEvent("Finishing activity")
		span.End()
		return nil, nil
	})

	client := dworkflow.NewClient(r.wf.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	for i := 1; i < r.n; i++ {
		cl := dworkflow.NewClient(r.wf.DaprN(i).GRPCConn(t, ctx))
		require.NoError(t, cl.StartWorker(ctx, reg))
	}

	cctx, span := tracer.Start(ctx, "schedule-my-workflow")

	id, err := client.ScheduleWorkflow(cctx, "foo", dworkflow.WithInstanceID("helloworld"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	span.End()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		spans := r.collector.TraceSpans(span.SpanContext().TraceID())
		names := make([]string, len(spans))
		for i, span := range spans {
			names[i] = span.GetName()
		}

		exp := []string{
			"schedule-my-workflow",
			"create_orchestration||foo",
			"orchestration||foo",
			"activity||bar",
			"this-is-my-activity-1",
			"activity||bar",
			"this-is-my-activity-2",
			"activity||bar",
			"this-is-my-activity-3",
			"activity||bar",
			"this-is-my-activity-4",
			"activity||bar",
			"this-is-my-activity-5",
			"activity||bar",
			"this-is-my-activity-6",
			"activity||bar",
			"this-is-my-activity-7",
			"activity||bar",
			"this-is-my-activity-8",
			"activity||bar",
			"this-is-my-activity-9",
			"activity||bar",
			"this-is-my-activity-10",
		}

		for i := range names {
			if names[i] == "CallActor/dapr.internal.default."+r.wf.Dapr().AppID()+".workflow/CreateWorkflowInstance" {
				exp = append(exp,
					"CallActor/dapr.internal.default."+r.wf.Dapr().AppID()+".workflow/CreateWorkflowInstance",
				)
			}
			if names[i] == "/dapr.proto.internals.v1.ServiceInvocation/CallActorStream" {
				exp = append(exp,
					"/dapr.proto.internals.v1.ServiceInvocation/CallActorStream",
				)
			}
		}

		assert.ElementsMatch(c, exp, names)
	}, time.Second*10, time.Millisecond*10)
}
