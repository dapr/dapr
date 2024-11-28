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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/client"
	"github.com/microsoft/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(multiListener))
}

type multiListener struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (m *multiListener) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	m.place = placement.New(t)
	m.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	return []framework.Option{
		framework.WithProcesses(m.place, srv, m.daprd),
	}
}

func (m *multiListener) Run(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	t.Run("connect_multiple_workers_to_single_daprd", func(t *testing.T) {
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("ConnectMultipleListenersToSingleDaprd", func(ctx *task.OrchestrationContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			var output string
			err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
			return output, err
		})
		r.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
			var name string
			if err := ctx.GetInput(&name); err != nil {
				return nil, err
			}
			return fmt.Sprintf("Hello, %s!", name), nil
		})

		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, err := grpc.DialContext(ctx, //nolint:staticcheck
					m.daprd.GRPCAddress(),
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithBlock(), //nolint:staticcheck
				)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, conn.Close()) })
				backendClient := client.NewTaskHubGrpcClient(conn, backend.DefaultLogger())

				taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
				require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
				defer cancelTaskhub()

				if i == 0 {
					// only the first worker will schedule the orchestration
					_, err = backendClient.ScheduleNewOrchestration(ctx, "ConnectMultipleListenersToSingleDaprd", api.WithInstanceID("Dapr"), api.WithInput("Dapr"))
					require.NoError(t, err)
				}

				metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, api.InstanceID("Dapr"), api.WithFetchPayloads(true))
				require.NoError(t, err)
				assert.True(t, metadata.IsComplete())
				assert.Equal(t, `"Hello, Dapr!"`, metadata.SerializedOutput)
			}()
		}

		wg.Wait()
	})
}
