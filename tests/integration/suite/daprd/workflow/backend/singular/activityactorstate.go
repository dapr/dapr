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

package singular

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/client"
	"github.com/microsoft/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	frameworksql "github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(activityactorstate))
}

type httpServer struct {
	actorsReady   atomic.Bool
	actorsReadyCh chan struct{}
}

func (h *httpServer) newhandler() http.Handler {
	h.actorsReadyCh = make(chan struct{})

	r := chi.NewRouter()
	r.Get("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("cassie in healthz")
		if h.actorsReady.CompareAndSwap(false, true) {
			close(h.actorsReadyCh)
		}
		w.WriteHeader(http.StatusOK)
	})
	r.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})
	return r
}

func (h *httpServer) waitForActorsReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.actorsReadyCh:
		return nil
	}
}

// check for activity state
type activityactorstate struct {
	daprd      *daprd.Daprd
	httpClient *http.Client
	srv        *prochttp.HTTP
	handler    *httpServer
	place      *placement.Placement
	db         *frameworksql.SQLite
}

func (s *activityactorstate) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	// Create a SQLite database
	s.db = frameworksql.New(t, frameworksql.WithActorStateStore(true))

	s.handler = &httpServer{}

	s.srv = prochttp.New(t, prochttp.WithHandler(s.handler.newhandler()))

	s.place = placement.New(t,
		placement.WithLogLevel("debug"), // TODO: rm
	)

	s.daprd = daprd.New(t,
		daprd.WithLogLevel("debug"), // TODO: rm
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithAppPort(s.srv.Port()),
		daprd.WithPlacementAddresses("127.0.0.1:"+strconv.Itoa(s.place.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(s.db, s.daprd, s.place, s.srv),
	}
}

func (s *activityactorstate) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	conn, err := grpc.DialContext(ctx, //nolint:staticcheck
		s.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	// Wait for actors to be ready
	err = s.handler.waitForActorsReady(ctx)
	require.NoError(t, err)

	backendClient := client.NewTaskHubGrpcClient(conn, backend.DefaultLogger())

	t.Run("check-activity-state", func(t *testing.T) {
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
		for i := 0; i < 10; i++ {
			r.AddActivityN("SayHello"+strconv.Itoa(i), func(ctx task.ActivityContext) (any, error) {
				var name string
				if err := ctx.GetInput(&name); err != nil {
					return nil, err
				}
				return fmt.Sprintf("Hello, %s!", name), nil
			})
		}

		// check for any data - what tables
		queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var result any
		err := s.db.GetConnection(t).QueryRowContext(queryCtx, "SELECT name FROM sqlite_master WHERE type='table'").Scan(&result)
		fmt.Printf("Cassie test result1: %+v\n", result)

		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id := api.InstanceID(s.startWorkflow(ctx, t, "SingleActivity", "Dapr"))

		// Purge the root orchestration
		s.purgeWorkflow(t, ctx, string(id)) // TODO: rm if doesnt work

		// check for any data - whats in the table
		queryCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var result1 any
		err = s.db.GetConnection(t).QueryRowContext(queryCtx, "SELECT * FROM 'metadata'").Scan(&result1)
		fmt.Printf("Cassie test result2: %+v\n", result1)

		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)

		// check again for any data - whats in the table
		queryCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = s.db.GetConnection(t).QueryRowContext(queryCtx, "SELECT * from 'metadata'").Scan(&result)
		fmt.Printf("Cassie test result3: %+v\n", result)

		assert.True(t, metadata.IsComplete())
		assert.Equal(t, `"Hello, Dapr!"`, metadata.SerializedOutput)
	})

}

func (s *activityactorstate) startWorkflow(ctx context.Context, t *testing.T, name string, input string) string {
	// use http client to start the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/start", s.daprd.HTTPPort(), name)
	data, err := json.Marshal(input)
	require.NoError(t, err)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	require.NoError(t, err)
	resp, err := s.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var response struct {
		InstanceID string `json:"instanceID"`
	}
	err = json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)

	return response.InstanceID
}

// purge workflow
func (s *activityactorstate) purgeWorkflow(t *testing.T, ctx context.Context, instanceID string) {
	// use http client to purge the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/purge", s.daprd.HTTPPort(), instanceID)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := s.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
}
