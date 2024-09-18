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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/client"
	"github.com/microsoft/durabletask-go/task"
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
	dbMutex    sync.Mutex
}

func (s *activityactorstate) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	// Create a SQLite database
	s.db = frameworksql.New(t,
		frameworksql.WithActorStateStore(true),
		frameworksql.WithTableName("workflow"),
	)

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

		r.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
			var name string
			if err := ctx.GetInput(&name); err != nil {
				return nil, err
			}
			var r1, r2, r3, r4, r5, r6 []byte
			queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s.dbMutex.Lock()
			rows, err := s.db.GetConnection(t).QueryContext(queryCtx, fmt.Sprintf("SELECT * FROM %s", s.db.GetTableName(t)))
			s.dbMutex.Unlock()
			require.NoError(t, err)
			for rows.Next() {
				err = rows.Scan(&r1, &r2, &r3, &r4, &r5, &r6)
				if err != nil {
					fmt.Printf("<<%v\n", err)
					break
				}
				fmt.Printf(">>%s \n>>%s \n>>%s \n>>%s \n>>%s \n>>%s<<\n", r1, r2, r3, r4, r5, r6)

				queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				s.dbMutex.Lock()
				_, err := s.db.GetConnection(t).QueryContext(queryCtx, fmt.Sprintf("DELETE FROM %s", s.db.GetTableName(t)))
				s.dbMutex.Unlock()
				require.NoError(t, err)
				fmt.Printf("cassie deelted from table")
				break

				// tried deleting all records
				/*
					dbrecords := []string{string(r1), string(r2), string(r3), string(r4), string(r5), string(r6)}
					for _, field := range dbrecords {
						queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						query := fmt.Sprintf(`
							DELETE FROM %s
							WHERE key = ?
							`)
						s.dbMutex.Lock()
						_, err = s.db.GetConnection(t).ExecContext(queryCtx, query, field)
						s.dbMutex.Unlock()
						fmt.Printf("cassie deleting record: %+v\n", field)

						// tried deleting just the activityState

							//if strings.Contains(field, "activityState") {
							//	fmt.Printf("cassie deleting record: %+v\n", field)
							//	queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							//	defer cancel()
							//	query := fmt.Sprintf(`
							//	DELETE FROM %s
							//	WHERE key = ?
							//`, s.db.GetTableName(t))
							//
							//	_, err = s.db.GetConnection(t).ExecContext(queryCtx, query, field)
							//	if err != nil {
							//		fmt.Printf("Failed to delete record: %v\n", err)
							//	} else {
							//		fmt.Printf("\nRecord with '.activity' deleted. field: %+v\n", field)
							//	}
							//}

					}
				*/
			}

			// ensure db fields are deleted
			fmt.Println("*********************************")
			var f1, f2, f3, f4, f5, f6 []byte
			queryCtx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel1()
			s.dbMutex.Lock()
			rows1, err := s.db.GetConnection(t).QueryContext(queryCtx1, fmt.Sprintf("SELECT * FROM %s", s.db.GetTableName(t)))
			s.dbMutex.Unlock()
			require.NoError(t, err)
			fmt.Printf("cassie err: %s && rows1: %+v\n", err, rows1.Next())
			for rows1.Next() {
				err = rows.Scan(&f1, &f2, &f3, &f4, &f5, &f6)
				if err != nil {
					fmt.Printf("<<%v\n", err)
					break
				}
				fmt.Printf(">>%s \n>>%s \n>>%s \n>>%s \n>>%s \n>>%s<<\n", f1, f2, f3, f4, f5, f6)
			}

			return fmt.Sprintf("Hello, %s!", name), nil
		})

		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id := api.InstanceID(s.startWorkflow(ctx, t, "SingleActivity", "Dapr"))
		//s.getWorkflow(t, ctx, string(id))

		queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.dbMutex.Lock()
		// try again
		_, err := s.db.GetConnection(t).QueryContext(queryCtx, fmt.Sprintf("DELETE FROM %s", s.db.GetTableName(t)))
		s.dbMutex.Unlock()
		require.NoError(t, err)
		fmt.Printf("cassie deelted from table")

		// pause the root orchestration
		s.pauseWorkflow(t, ctx, string(id))
		time.Sleep(3 * time.Second)
		s.resumeWorkflow(t, ctx, string(id))

		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		//require.NoError(t, err)
		fmt.Printf("cassie err: %s && metadata: %+v\n", err, metadata)
		//assert.True(t, metadata.IsComplete())
		//assert.Equal(t, `"Hello, Dapr!"`, metadata.SerializedOutput)
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

// pause workflow
func (s *activityactorstate) pauseWorkflow(t *testing.T, ctx context.Context, instanceID string) {
	// use http client to pause the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/pause", s.daprd.HTTPPort(), instanceID)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := s.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
}

// get workflow
func (s *activityactorstate) getWorkflow(t *testing.T, ctx context.Context, instanceID string) {
	// use http client to get the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s", s.daprd.HTTPPort(), instanceID)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := s.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
}

// resume workflow
func (s *activityactorstate) resumeWorkflow(t *testing.T, ctx context.Context, instanceID string) {
	// use http client to resume the workflow
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/resume", s.daprd.HTTPPort(), instanceID)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := s.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
}
