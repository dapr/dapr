/*
Copyright 2023 The Dapr Authors
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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/client"
	"github.com/microsoft/durabletask-go/task"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reconnect))
}

type reconnect struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	httpClient *http.Client
	grpcClient runtimev1pb.DaprClient
}

func (d *reconnect) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	d.place = placement.New(t)
	d.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	return []framework.Option{
		framework.WithProcesses(d.place, srv, d.daprd),
	}
}

func (d *reconnect) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	d.httpClient = fclient.HTTP(t)

	conn, err := grpc.DialContext(ctx, //nolint:staticcheck
		d.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	d.grpcClient = runtimev1pb.NewDaprClient(conn)

	backendClient := client.NewTaskHubGrpcClient(conn, backend.DefaultLogger())

	t.Run("reconnect_during_wait_for_event", func(t *testing.T) {
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("ReconnectDuringWaitForEvent", func(ctx *task.OrchestrationContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			var output string
			err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
			if err != nil {
				return nil, err
			}
			err = ctx.WaitForSingleEvent("event1", 1*time.Minute).Await(nil)
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
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id, err := backendClient.ScheduleNewOrchestration(ctx, "ReconnectDuringWaitForEvent", api.WithInstanceID("Dapr"), api.WithInput("Dapr"))
		require.NoError(t, err)

		_, err = backendClient.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		cancelTaskhub() // disconnect the listener

		taskhubCtx, cancelTaskhub = context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		_, err = backendClient.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		err = backendClient.RaiseEvent(ctx, id, "event1")
		require.NoError(t, err)

		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, metadata.RuntimeStatus == api.RUNTIME_STATUS_COMPLETED)
		assert.Equal(t, `"Hello, Dapr!"`, metadata.SerializedOutput)
	})

	t.Run("reconnect_during_activity", func(t *testing.T) {
		numActivities := 5
		activities := make(chan struct{}, numActivities)
		for i := 0; i < numActivities-1; i++ {
			activities <- struct{}{}
		}

		r := task.NewTaskRegistry()
		r.AddOrchestratorN("ReconnectDuringActivity", func(ctx *task.OrchestrationContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			var output string
			for i := 0; i < numActivities; i++ {
				err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
				if err != nil {
					return nil, err
				}
			}

			return output, err
		})
		r.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
			var name string
			if err := ctx.GetInput(&name); err != nil {
				return nil, err
			}

			<-activities // the last activity will block until after the reconnection

			return fmt.Sprintf("Hello, %s!", name), nil
		})
		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id, err := backendClient.ScheduleNewOrchestration(ctx, "ReconnectDuringActivity", api.WithInstanceID("Dapr"), api.WithInput("Dapr"))
		require.NoError(t, err)

		_, err = backendClient.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		cancelTaskhub() // disconnect the listener

		taskhubCtx, cancelTaskhub = context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		_, err = backendClient.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		activities <- struct{}{}

		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, metadata.RuntimeStatus == api.RUNTIME_STATUS_COMPLETED)
		assert.Equal(t, `"Hello, Dapr!"`, metadata.SerializedOutput)
	})
}
