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

package disstimeout

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

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(workflowexec))
}

// workflowexec tests that a workflow started BEFORE a dissemination timeout
// can complete its activity execution AFTER recovery. This reproduces the
// production scenario where workflows were stuck in RUNNING with no activities
// executing.
type workflowexec struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	httpClient *http.Client
}

func (w *workflowexec) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	})
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": []}`))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	sched := scheduler.New(t)
	w.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*7),
	)
	w.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithScheduler(sched),
	)

	return []framework.Option{
		framework.WithProcesses(w.place, sched, srv, w.daprd),
	}
}

func (w *workflowexec) Run(t *testing.T, ctx context.Context) {
	w.place.WaitUntilRunning(t, ctx)
	w.daprd.WaitUntilRunning(t, ctx)

	w.httpClient = fclient.HTTP(t)

	conn, err := grpc.DialContext(ctx, //nolint:staticcheck
		w.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	backendClient := dtclient.NewTaskHubGrpcClient(conn, backend.DefaultLogger())

	r := task.NewTaskRegistry()
	r.AddOrchestratorN("TimeoutWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if ierr := ctx.GetInput(&input); ierr != nil {
			return nil, ierr
		}
		var output string
		aerr := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, aerr
	})
	r.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		var name string
		if ierr := ctx.GetInput(&name); ierr != nil {
			return nil, ierr
		}
		return fmt.Sprintf("Hello, %s!", name), nil
	})

	taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
	t.Cleanup(cancelTaskhub)
	require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := w.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		if !assert.NotNil(c, table.Tables["default"].Hosts) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	connectBlocker := func(t *testing.T, id string) {
		t.Helper()
		client := w.place.Client(t, ctx)
		blocker, berr := client.ReportDaprStatus(ctx)
		require.NoError(t, berr)
		require.NoError(t, blocker.Send(&v1pb.Host{
			Name: id, Port: 9999,
			Entities: []string{"myactor"}, Id: id, Namespace: "default",
		}))
		go func() {
			for {
				if _, recvErr := blocker.Recv(); recvErr != nil {
					return
				}
			}
		}()
	}

	for i := range 2 {
		connectBlocker(t, fmt.Sprintf("blocker-%d", i))
		time.Sleep(9 * time.Second)
	}

	instanceID := w.startWorkflow(ctx, t, "TimeoutWorkflow", "World")

	metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
	require.NoError(t, err, "workflow should complete after timeout recovery")
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, `"Hello, World!"`, metadata.GetOutput().GetValue())
}

func (w *workflowexec) startWorkflow(ctx context.Context, t *testing.T, name, input string) string {
	t.Helper()

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/start", w.daprd.HTTPPort(), name)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, reqURL, strings.NewReader(string(data)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.httpClient.Do(req)
	require.NoError(t, err, "workflow start should not hang after timeout recovery")
	defer resp.Body.Close()
	if !assert.Equal(t, http.StatusAccepted, resp.StatusCode) {
		b, _ := io.ReadAll(resp.Body)
		require.Fail(t, string(b))
	}
	var response struct {
		InstanceID string `json:"instanceID"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	return response.InstanceID
}
