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
	"google.golang.org/grpc/status"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
	grpcClient runtimev1pb.DaprClient
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	b.sched = scheduler.New(t)
	b.place = placement.New(t)
	b.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithSchedulerAddresses(b.sched.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(b.place, b.sched, srv, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.sched.WaitUntilRunning(t, ctx)
	b.place.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	b.httpClient = fclient.HTTP(t)

	conn, err := grpc.DialContext(ctx, //nolint:staticcheck
		b.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	b.grpcClient = runtimev1pb.NewDaprClient(conn)

	backendClient := client.NewTaskHubGrpcClient(conn, backend.DefaultLogger())

	t.Run("basic", func(t *testing.T) {
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("SingleActivity", func(ctx *task.OrchestrationContext) (any, error) {
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
		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id := api.InstanceID(b.startWorkflow(ctx, t, "SingleActivity", "Dapr"))
		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
		assert.Equal(t, `"Hello, Dapr!"`, metadata.GetOutput().GetValue())
	})

	t.Run("terminate", func(t *testing.T) {
		delayTime := 4 * time.Second
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

		id := api.InstanceID(b.startWorkflow(ctx, t, "Root", ""))

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
		b.terminateWorkflow(t, ctx, string(id))

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

	t.Run("purge", func(t *testing.T) {
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("Root", func(ctx *task.OrchestrationContext) (any, error) {
			ctx.CallSubOrchestrator("L1", task.WithSubOrchestrationInstanceID(string(ctx.ID)+"_L1")).Await(nil)
			return nil, nil
		})
		r.AddOrchestratorN("L1", func(ctx *task.OrchestrationContext) (any, error) {
			ctx.CallSubOrchestrator("L2", task.WithSubOrchestrationInstanceID(string(ctx.ID)+"_L2")).Await(nil)
			return nil, nil
		})
		r.AddOrchestratorN("L2", func(ctx *task.OrchestrationContext) (any, error) {
			ctx.CreateTimer(2 * time.Second).Await(nil)
			return nil, nil
		})
		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		// Run the orchestration, which will block waiting for external events
		id := api.InstanceID(b.startWorkflow(ctx, t, "Root", ""))

		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
		require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), metadata.GetRuntimeStatus().String())

		// Purge the root orchestration
		b.purgeWorkflow(t, ctx, string(id))

		// Verify that root Orchestration has been purged
		_, err = backendClient.FetchOrchestrationMetadata(ctx, id)
		assert.Contains(t, status.Convert(err).Message(), api.ErrInstanceNotFound.Error())

		// Verify that L1 and L2 orchestrations have been purged
		_, err = backendClient.FetchOrchestrationMetadata(ctx, id+"_L1")
		require.Contains(t, status.Convert(err).Message(), api.ErrInstanceNotFound.Error())

		_, err = backendClient.FetchOrchestrationMetadata(ctx, id+"_L1_L2")
		require.Contains(t, status.Convert(err).Message(), api.ErrInstanceNotFound.Error())
	})

	t.Run("child workflow", func(t *testing.T) {
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("root", func(ctx *task.OrchestrationContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			var output string
			err := ctx.CallSubOrchestrator("child", task.WithSubOrchestratorInput(input)).Await(&output)
			return output, err
		})
		r.AddOrchestratorN("child", func(ctx *task.OrchestrationContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			return fmt.Sprintf("Hello, %s!", input), nil
		})
		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id := api.InstanceID(b.startWorkflow(ctx, t, "root", "Dapr"))
		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
		assert.Equal(t, `"Hello, Dapr!"`, metadata.GetOutput().GetValue())
	})
}

func (b *basic) startWorkflow(ctx context.Context, t *testing.T, name string, input string) string {
	t.Helper()

	// use http client to start the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/start", b.daprd.HTTPPort(), name)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	require.NoError(t, err)
	resp, err := b.httpClient.Do(req)
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
func (b *basic) terminateWorkflow(t *testing.T, ctx context.Context, instanceID string) {
	t.Helper()

	// use http client to terminate the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/terminate", b.daprd.HTTPPort(), instanceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := b.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	if !assert.Equal(t, http.StatusAccepted, resp.StatusCode) {
		bresp, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Fail(t, string(bresp))
	}
}

// purge workflow
func (b *basic) purgeWorkflow(t *testing.T, ctx context.Context, instanceID string) {
	t.Helper()

	// use http client to purge the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/purge", b.daprd.HTTPPort(), instanceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := b.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if !assert.Equal(t, http.StatusAccepted, resp.StatusCode) {
		bresp, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Empty(t, string(bresp))
	}
}
