/*
Copyright 2024 The Dapr Authors
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

package selfhosted

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(actorstate))
}

const actorStateStoreComp = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorStateStore
   value: "true"
`

// actorstate ensures that removing the actor state store via hot reload shuts
// down actor hosting in-process - hosted actors are deactivated and the actor
// and workflow APIs error - and that hosting and the workflow APIs recover
// when the actor state store is hot reloaded back.
type actorstate struct {
	daprd         *daprd.Daprd
	resDir        string
	deactivatedCh chan string
}

func (a *actorstate) Setup(t *testing.T) []framework.Option {
	a.deactivatedCh = make(chan string, 10)

	handler := chi.NewRouter()
	handler.Get("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.Delete("/actors/{actorType}/{actorId}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		a.deactivatedCh <- chi.URLParam(r, "actorType") + "/" + chi.URLParam(r, "actorId")
	})
	handler.Put("/actors/{actorType}/{actorId}/method/foo", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`"bar"`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	sched := scheduler.New(t)
	place := placement.New(t)

	a.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"), []byte(actorStateStoreComp), 0o600))

	a.daprd = daprd.New(t,
		daprd.WithResourcesDir(a.resDir),
		daprd.WithScheduler(sched),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv, sched, place, a.daprd),
	}
}

func (a *actorstate) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", name), nil
	}))
	require.NoError(t, reg.AddWorkflowN("SingleActivity", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, err
	}))

	wfClient := dtclient.NewTaskHubGrpcClient(a.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, wfClient.StartWorkItemListener(ctx, reg))

	invokeActor := func(c *assert.CollectT) (int, string) {
		url := fmt.Sprintf("http://%s/v1.0/actors/myactortype/myactor1/method/foo", a.daprd.HTTPAddress())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		require.NoError(c, err)
		resp, err := httpClient.Do(req)
		require.NoError(c, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(c, err)
		return resp.StatusCode, string(body)
	}

	getActorState := func(c *assert.CollectT) (int, string) {
		url := fmt.Sprintf("http://%s/v1.0/actors/myactortype/myactor1/state/mykey", a.daprd.HTTPAddress())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(c, err)
		resp, err := httpClient.Do(req)
		require.NoError(c, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(c, err)
		return resp.StatusCode, string(body)
	}

	t.Run("actors and workflows work with the actor state store", func(t *testing.T) {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := invokeActor(c)
			assert.Equalf(c, http.StatusOK, code, "body: %s", body)
		}, time.Second*20, time.Millisecond*10)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, _ := getActorState(c)
			assert.Equal(c, http.StatusNoContent, code)
		}, time.Second*10, time.Millisecond*10)

		id, err := wfClient.ScheduleNewWorkflow(ctx, "SingleActivity", api.WithInput("Dapr"), api.WithInstanceID("beforeremove"))
		require.NoError(t, err)
		meta, err := wfClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
		assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
	})

	t.Run("removing the actor state store shuts down actor hosting", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(a.resDir, "state.yaml")))

		// The hosted actor is deactivated.
		select {
		case act := <-a.deactivatedCh:
			assert.Equal(t, "myactortype/myactor1", act)
		case <-time.After(time.Second * 20):
			assert.Fail(t, "did not receive actor deactivation in time")
		}

		// All actor instances are torn down.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			meta := a.daprd.GetMetadata(c, ctx)
			if !assert.NotNil(c, meta) || !assert.NotNil(c, meta.ActorRuntime) {
				return
			}
			for _, active := range meta.ActorRuntime.ActiveActors {
				assert.Equalf(c, 0, active.Count, "actor type %s still active", active.Type)
			}
		}, time.Second*20, time.Millisecond*10)

		// Actor state operations error with the actor runtime not configured
		// error.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := getActorState(c)
			assert.Equal(c, http.StatusInternalServerError, code)
			assert.Contains(c, body, "the state store is not configured to use the actor runtime")
		}, time.Second*20, time.Millisecond*10)

		// Workflow APIs error with the actor runtime not configured error.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
				WorkflowComponent: "dapr",
				WorkflowName:      "SingleActivity",
			})
			if !assert.Error(c, err) {
				return
			}
			s, ok := status.FromError(err)
			require.True(c, ok)
			assert.Equal(c, codes.Internal, s.Code())
			assert.Contains(c, err.Error(), "the state store is not configured to use the actor runtime")
		}, time.Second*20, time.Millisecond*10)

		// The actor types are no longer advertised so invocation fails to
		// resolve a host.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := invokeActor(c)
			assert.NotEqual(c, http.StatusOK, code)
			assert.Contains(c, strings.ToLower(body), "did not find address for actor")
		}, time.Second*20, time.Millisecond*10)
	})

	t.Run("re-adding the actor state store recovers actors and workflows", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"), []byte(actorStateStoreComp), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := invokeActor(c)
			assert.Equalf(c, http.StatusOK, code, "body: %s", body)
		}, time.Second*20, time.Millisecond*10)

		// The workflow worker stream has stayed connected throughout; a new
		// workflow completes without reconnecting.
		id, err := wfClient.ScheduleNewWorkflow(ctx, "SingleActivity", api.WithInput("Dapr"), api.WithInstanceID("afterreadd"))
		require.NoError(t, err)
		meta, err := wfClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
		assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
	})
}
