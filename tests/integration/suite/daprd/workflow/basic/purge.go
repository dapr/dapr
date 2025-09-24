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
	"fmt"
	"io"
	"net/http"
	"strings"
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
	suite.Register(new(purge))
}

type purge struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
	grpcClient runtimev1pb.DaprClient
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	p.sched = scheduler.New(t)
	p.place = placement.New(t)
	p.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithSchedulerAddresses(p.sched.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(p.place, p.sched, srv, p.daprd),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.sched.WaitUntilRunning(t, ctx)
	p.place.WaitUntilRunning(t, ctx)
	p.daprd.WaitUntilRunning(t, ctx)

	p.httpClient = fclient.HTTP(t)

	conn, err := grpc.DialContext(ctx, //nolint:staticcheck
		p.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	p.grpcClient = runtimev1pb.NewDaprClient(conn)

	backendClient := client.NewTaskHubGrpcClient(conn, backend.DefaultLogger())

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
		id := api.InstanceID(p.startWorkflow(ctx, t, "Root", ""))

		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
		require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), metadata.GetRuntimeStatus().String())

		// Purge the root orchestration
		p.purgeWorkflow(t, ctx, string(id))

		// Verify that root Orchestration has been purged
		_, err = backendClient.FetchOrchestrationMetadata(ctx, id)
		assert.Contains(t, status.Convert(err).Message(), api.ErrInstanceNotFound.Error())

		// Verify that L1 and L2 orchestrations have been purged
		_, err = backendClient.FetchOrchestrationMetadata(ctx, id+"_L1")
		require.Contains(t, status.Convert(err).Message(), api.ErrInstanceNotFound.Error())

		_, err = backendClient.FetchOrchestrationMetadata(ctx, id+"_L1_L2")
		require.Contains(t, status.Convert(err).Message(), api.ErrInstanceNotFound.Error())
	})
}

func (p *purge) startWorkflow(ctx context.Context, t *testing.T, name string, input string) string {
	t.Helper()

	// use http client to start the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/start", p.daprd.HTTPPort(), name)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	require.NoError(t, err)
	resp, err := p.httpClient.Do(req)
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

// purge workflow
func (p *purge) purgeWorkflow(t *testing.T, ctx context.Context, instanceID string) {
	t.Helper()

	// use http client to purge the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/purge", p.daprd.HTTPPort(), instanceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := p.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if !assert.Equal(t, http.StatusAccepted, resp.StatusCode) {
		bresp, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Empty(t, string(bresp))
	}
}
