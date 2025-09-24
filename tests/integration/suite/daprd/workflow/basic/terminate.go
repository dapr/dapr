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

package basic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(terminate))
}

type terminate struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
	grpcClient runtimev1pb.DaprClient
}

func (tt *terminate) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	tt.sched = scheduler.New(t)
	tt.place = placement.New(t)
	tt.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(tt.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithSchedulerAddresses(tt.sched.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(tt.place, tt.sched, srv, tt.daprd),
	}
}

func (tt *terminate) Run(t *testing.T, ctx context.Context) {
	tt.sched.WaitUntilRunning(t, ctx)
	tt.place.WaitUntilRunning(t, ctx)
	tt.daprd.WaitUntilRunning(t, ctx)

	tt.httpClient = fclient.HTTP(t)

	conn, err := grpc.DialContext(ctx, //nolint:staticcheck
		tt.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	tt.grpcClient = runtimev1pb.NewDaprClient(conn)

	backendClient := client.NewTaskHubGrpcClient(conn, backend.DefaultLogger())

	t.Run("terminate", func(t *testing.T) {
		delayTime := 30 * time.Second
		var executedActivity atomic.Bool
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("Root", func(ctx *task.OrchestrationContext) (any, error) {
			tasks := []task.Task{}
			for i := range 3 {
				task := ctx.CallSubOrchestrator("L1", task.WithSubOrchestrationInstanceID(string(ctx.ID)+"_L1_"+strconv.Itoa(i)))
				tasks = append(tasks, task)
			}
			for _, task := range tasks {
				task.Await(nil)
			}
			return nil, nil
		})
		r.AddOrchestratorN("L1", func(ctx *task.OrchestrationContext) (any, error) {
			ctx.CallSubOrchestrator("L2", task.WithSubOrchestrationInstanceID(string(ctx.ID)+"_L2")).Await(nil)
			return nil, nil
		})
		r.AddOrchestratorN("L2", func(ctx *task.OrchestrationContext) (any, error) {
			ctx.CreateTimer(delayTime).Await(nil)
			ctx.CallActivity("Fail").Await(nil)
			return nil, nil
		})
		r.AddActivityN("Fail", func(ctx task.ActivityContext) (any, error) {
			executedActivity.Store(true)
			return nil, errors.New("failed: Should not have executed the activity")
		})

		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id := api.InstanceID(tt.startWorkflow(ctx, t, "Root", ""))

		// Wait long enough to ensure all orchestrations have started (but not longer than the timer delay)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			// List of all orchestrations created
			orchestrationIDs := []string{string(id)}
			for i := range 3 {
				orchestrationIDs = append(orchestrationIDs, string(id)+"_L1_"+strconv.Itoa(i), string(id)+"_L1_"+strconv.Itoa(i)+"_L2")
			}
			for _, orchID := range orchestrationIDs {
				meta, err := backendClient.FetchOrchestrationMetadata(ctx, api.InstanceID(orchID))
				assert.NoError(c, err)
				// All orchestrations should be running
				assert.Equal(c, api.RUNTIME_STATUS_RUNNING.String(), meta.GetRuntimeStatus().String())
			}
		}, 10*time.Second, 10*time.Millisecond)

		// Terminate the root orchestration
		tt.terminateWorkflow(t, ctx, string(id))

		// Wait for the root orchestration to complete and verify its terminated status
		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
		require.Equal(t, api.RUNTIME_STATUS_TERMINATED, metadata.GetRuntimeStatus())

		// Wait for all L2 suborchestrations to complete
		orchIDs := []string{}
		for i := range 3 {
			orchIDs = append(orchIDs, string(id)+"_L1_"+strconv.Itoa(i)+"_L2")
		}
		for _, orchID := range orchIDs {
			_, err := backendClient.WaitForOrchestrationCompletion(ctx, api.InstanceID(orchID))
			require.NoError(t, err)
		}

		// Verify that none of the L2 suborchestrations executed the activity
		assert.False(t, executedActivity.Load())
	})
}

func (tt *terminate) startWorkflow(ctx context.Context, t *testing.T, name string, input string) string {
	t.Helper()

	// use http client to start the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/start", tt.daprd.HTTPPort(), name)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	require.NoError(t, err)
	resp, err := tt.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if !assert.Equal(t, http.StatusAccepted, resp.StatusCode) {
		bresp, berr := io.ReadAll(resp.Body)
		require.NoError(t, berr)
		require.Fail(t, string(bresp))
	}
	var response struct {
		InstanceID string `json:"instanceID"`
	}
	err = json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)

	return response.InstanceID
}

// terminate workflow
func (tt *terminate) terminateWorkflow(t *testing.T, ctx context.Context, instanceID string) {
	t.Helper()

	// use http client to terminate the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/terminate", tt.daprd.HTTPPort(), instanceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := tt.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	if !assert.Equal(t, http.StatusAccepted, resp.StatusCode) {
		bresp, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Fail(t, string(bresp))
	}
}
