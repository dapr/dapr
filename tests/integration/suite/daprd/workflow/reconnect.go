/*
Copyright 2024 The Dapr Authors
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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reconnect))
}

type reconnect struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (d *reconnect) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	sched := scheduler.New(t)
	d.place = placement.New(t)
	d.daprd = daprd.New(t,
		daprd.WithScheduler(sched),
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	return []framework.Option{
		framework.WithProcesses(sched, d.place, srv, d.daprd),
	}
}

func (d *reconnect) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	client := client.NewTaskHubGrpcClient(d.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("reconnect_during_wait_for_event", func(t *testing.T) {
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("ReconnectDuringWaitForEvent", func(ctx *task.OrchestrationContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			var output string
			if err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output); err != nil {
				return nil, err
			}
			err := ctx.WaitForSingleEvent("event1", 1*time.Minute).Await(nil)
			return output, err
		})
		r.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
			var name string
			if err := ctx.GetInput(&name); err != nil {
				return nil, err
			}
			return fmt.Sprintf("Hello, %s!", name), nil
		})
		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, client.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id, err := client.ScheduleNewOrchestration(ctx, "ReconnectDuringWaitForEvent", api.WithInstanceID("Dapr"), api.WithInput("Dapr"))
		require.NoError(t, err)

		_, err = client.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		cancelTaskhub() // disconnect the listener

		taskhubCtx, cancelTaskhub = context.WithCancel(ctx)
		require.NoError(t, client.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		_, err = client.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		err = client.RaiseEvent(ctx, id, "event1")
		require.NoError(t, err)

		metadata, err := client.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.GetRuntimeStatus())
		assert.Equal(t, `"Hello, Dapr!"`, metadata.GetOutput().GetValue())
	})

	t.Run("reconnect_during_activity", func(t *testing.T) {
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("ReconnectDuringActivity", func(ctx *task.OrchestrationContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			ctx.WaitForSingleEvent("event", time.Minute).Await(nil)

			return "Hello, " + input + "!", nil
		})
		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, client.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		var id api.InstanceID
		var err error
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			id, err = client.ScheduleNewOrchestration(ctx, "ReconnectDuringActivity", api.WithInstanceID("Dapr"), api.WithInput("Dapr"))
			assert.NoError(c, err)
		}, time.Second*10, time.Millisecond*10)

		_, err = client.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		cancelTaskhub() // disconnect the listener

		taskhubCtx, cancelTaskhub = context.WithCancel(ctx)
		require.NoError(t, client.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		_, err = client.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		client.RaiseEvent(ctx, id, "event")

		metadata, err := client.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.GetRuntimeStatus())
		assert.Equal(t, `"Hello, Dapr!"`, metadata.GetOutput().GetValue())
	})
}
