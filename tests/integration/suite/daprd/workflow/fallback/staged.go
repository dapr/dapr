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

package fallback

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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(staged))
	suite.Register(new(stagedcrossapp))
}

// staged checks that each workflow operation tells its caller whether it was
// staged, through the durabletask API, Dapr's gRPC API and Dapr's HTTP API.
// With a worker connected nothing is staged. Once the last worker leaves,
// the daprd is a fallback host, and every start, raise event, terminate,
// suspend, resume and rerun it serves is staged.
type staged struct {
	workflow *workflow.Workflow
}

func (s *staged) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		workflow.WithFallbackSettleN(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *staged) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	reg := s.workflow.Registry()
	registerStagedWorkflow(t, reg)
	require.NoError(t, reg.AddWorkflowN("act-wf", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("act", func(task.ActivityContext) (any, error) {
		return workflow.DoneOutput, nil
	}))

	worker := s.workflow.ConnectWorker(t, ctx, reg)
	s.workflow.WaitForConnectedWorkers(t, ctx, 1)

	ops := newStagedOps(t, ctx, s.workflow, 0, "")

	// The sources of the reruns below: completed while a worker is connected.
	for _, id := range []api.InstanceID{"staged-rerun-src-worker", "staged-rerun-src-fallback"} {
		_, err := ops.mgmt.ScheduleNewWorkflow(ctx, "act-wf", api.WithInstanceID(id))
		require.NoError(t, err)
		_, err = ops.mgmt.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
	}

	t.Run("a daprd with a worker connected stages nothing", func(t *testing.T) {
		ops.run(t, ctx, "staged-worker", false)

		rctx, cancel := context.WithTimeout(ctx, time.Second*10)
		defer cancel()
		res, err := ops.mgmt.RerunWorkflowFromEventWithResult(rctx, "staged-rerun-src-worker", 0,
			api.WithRerunNewInstanceID("staged-rerun-dst-worker"))
		require.NoError(t, err)
		assert.False(t, res.Staged)
	})

	worker.Disconnect(t)
	s.workflow.WaitForNoConnectedWorkers(t, ctx)

	// The daprd turns into a fallback host in place: it gives up the executor
	// actor type and keeps hosting the workflow actor type.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, s.workflow.HostsActorType(c, ctx, 0, s.workflow.ExecutorActorType(0)))
		assert.True(c, s.workflow.HostsActorType(c, ctx, 0, s.workflow.WorkflowActorType(0)))
	}, time.Second*10, time.Millisecond*50)

	t.Run("a fallback host stages every operation", func(t *testing.T) {
		// The retry rides out the disconnect callback, as in rerun.
		var res *api.RerunResult
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			rctx, cancel := context.WithTimeout(ctx, time.Second*5)
			defer cancel()
			var err error
			res, err = ops.mgmt.RerunWorkflowFromEventWithResult(rctx, "staged-rerun-src-fallback", 0,
				api.WithRerunNewInstanceID("staged-rerun-dst-fallback"))
			assert.NoError(c, err)
		}, time.Second*10, time.Millisecond*50)
		assert.True(t, res.Staged)

		ops.run(t, ctx, "staged-fallback", true)
	})
}

// stagedcrossapp checks that a caller on one app learns whether an operation
// on a workflow of another app was staged. App X has a worker and app Y has
// none, so Y's daprd is a fallback host: every operation X makes on Y is
// staged, and none Y makes on X is.
type stagedcrossapp struct {
	workflow *workflow.Workflow
}

func (s *stagedcrossapp) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithStagedOperations(),
		workflow.WithFallbackSettleN(t, 0),
		workflow.WithFallbackSettleN(t, 1),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *stagedcrossapp) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	registerStagedWorkflow(t, s.workflow.RegistryN(0))
	registerStagedWorkflow(t, s.workflow.RegistryN(1))

	s.workflow.ConnectWorkerN(t, ctx, 0, s.workflow.RegistryN(0))
	s.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)

	// Y's fallback host registers once the placement tables have shown no
	// executor of app Y for the settle period.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, s.workflow.HostsActorType(c, ctx, 1, s.workflow.WorkflowActorType(1)))
	}, time.Second*30, time.Millisecond*50)

	appX := s.workflow.DaprN(0).AppID()
	appY := s.workflow.DaprN(1).AppID()
	fromX := newStagedOps(t, ctx, s.workflow, 0, appY)
	fromY := newStagedOps(t, ctx, s.workflow, 1, appX)

	// Retried until the placement table which shows Y's host reaches X.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		sctx, cancel := context.WithTimeout(ctx, time.Second*5)
		defer cancel()
		_, err := fromX.mgmt.ScheduleNewWorkflow(sctx, "staged-wf",
			api.WithInstanceID("staged-crossapp-probe"),
			api.WithAppID(appY),
		)
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*50)

	t.Run("operations on an app without a worker are staged", func(t *testing.T) {
		fromX.run(t, ctx, "staged-crossapp-x", true)
	})

	t.Run("operations on an app with a worker are not staged", func(t *testing.T) {
		fromY.run(t, ctx, "staged-crossapp-y", false)
	})
}

// registerStagedWorkflow registers the workflow stagedOps drives: it waits
// for an event it is never sent, so it runs until it is terminated.
func registerStagedWorkflow(t *testing.T, reg *task.TaskRegistry) {
	t.Helper()
	require.NoError(t, reg.AddWorkflowN("staged-wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("never", time.Hour).Await(nil)
	}))
}

// stagedOps drives the operations a fallback host may stage through the
// durabletask API, Dapr's gRPC API and Dapr's HTTP API of one daprd, on a
// workflow of the app appID, or of the daprd's own app if appID is empty.
type stagedOps struct {
	mgmt    *client.TaskHubGrpcClient
	grpc    rtv1.DaprClient
	http    *http.Client
	baseURL string
	appID   string
}

func newStagedOps(t *testing.T, ctx context.Context, w *workflow.Workflow, index int, appID string) *stagedOps {
	t.Helper()
	return &stagedOps{
		mgmt:    w.ManagementClientN(t, ctx, index),
		grpc:    w.GRPCClientN(t, ctx, index),
		http:    fclient.HTTP(t),
		baseURL: fmt.Sprintf("http://%s/v1.0-beta1/workflows/dapr", w.DaprN(index).HTTPAddress()),
		appID:   appID,
	}
}

// run starts a workflow through each API, then suspends, resumes, raises an
// event to and terminates it, and checks that each reports staged as want.
// The instance IDs start with prefix.
func (o *stagedOps) run(t *testing.T, ctx context.Context, prefix string, want bool) {
	t.Helper()

	// A daprd which stages an operation answers at once, and one which does
	// not answers once the worker made the change, so each call gets a short
	// deadline of its own.
	opCtx := func(t *testing.T) context.Context {
		octx, cancel := context.WithTimeout(ctx, time.Second*10)
		t.Cleanup(cancel)
		return octx
	}

	t.Run("durabletask", func(t *testing.T) {
		id := api.InstanceID(prefix + "-durabletask")

		startOpts := []api.NewWorkflowOptions{api.WithInstanceID(id)}
		var (
			raiseOpts     []api.RaiseEventOptions
			suspendOpts   []api.SuspendOptions
			resumeOpts    []api.ResumeOptions
			terminateOpts []api.TerminateOptions
		)
		if o.appID != "" {
			startOpts = append(startOpts, api.WithAppID(o.appID))
			raiseOpts = append(raiseOpts, api.WithRaiseEventAppID(o.appID))
			suspendOpts = append(suspendOpts, api.WithSuspendAppID(o.appID))
			resumeOpts = append(resumeOpts, api.WithResumeAppID(o.appID))
			terminateOpts = append(terminateOpts, api.WithTerminateAppID(o.appID))
		}

		start, err := o.mgmt.ScheduleNewWorkflowWithResult(opCtx(t), "staged-wf", startOpts...)
		require.NoError(t, err)
		assert.Equal(t, id, start.InstanceID)
		assert.Equal(t, want, start.Staged, "start")

		suspend, err := o.mgmt.SuspendWorkflowWithResult(opCtx(t), id, "staged", suspendOpts...)
		require.NoError(t, err)
		assert.Equal(t, want, suspend.Staged, "suspend")

		resume, err := o.mgmt.ResumeWorkflowWithResult(opCtx(t), id, "staged", resumeOpts...)
		require.NoError(t, err)
		assert.Equal(t, want, resume.Staged, "resume")

		raise, err := o.mgmt.RaiseEventWithResult(opCtx(t), id, "ping", raiseOpts...)
		require.NoError(t, err)
		assert.Equal(t, want, raise.Staged, "raise event")

		terminate, err := o.mgmt.TerminateWorkflowWithResult(opCtx(t), id, terminateOpts...)
		require.NoError(t, err)
		assert.Equal(t, want, terminate.Staged, "terminate")
	})

	t.Run("grpc", func(t *testing.T) {
		id := prefix + "-grpc"
		var appID *string
		if o.appID != "" {
			appID = &o.appID
		}

		start, err := o.grpc.StartWorkflowBeta1(opCtx(t), &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "staged-wf",
			InstanceId:        id,
			AppId:             appID,
		})
		require.NoError(t, err)
		assert.Equal(t, id, start.GetInstanceId())
		assert.Equal(t, want, start.GetStaged(), "start")

		pause, err := o.grpc.PauseWorkflowBeta1(opCtx(t), &rtv1.PauseWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        id,
			AppId:             appID,
		})
		require.NoError(t, err)
		assert.Equal(t, want, pause.GetStaged(), "pause")

		resume, err := o.grpc.ResumeWorkflowBeta1(opCtx(t), &rtv1.ResumeWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        id,
			AppId:             appID,
		})
		require.NoError(t, err)
		assert.Equal(t, want, resume.GetStaged(), "resume")

		raise, err := o.grpc.RaiseEventWorkflowBeta1(opCtx(t), &rtv1.RaiseEventWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        id,
			EventName:         "ping",
			AppId:             appID,
		})
		require.NoError(t, err)
		assert.Equal(t, want, raise.GetStaged(), "raise event")

		terminate, err := o.grpc.TerminateWorkflowBeta1(opCtx(t), &rtv1.TerminateWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        id,
			AppId:             appID,
		})
		require.NoError(t, err)
		assert.Equal(t, want, terminate.GetStaged(), "terminate")
	})

	t.Run("http", func(t *testing.T) {
		id := prefix + "-http"
		query := ""
		if o.appID != "" {
			query = "?appID=" + o.appID
		}

		post := func(t *testing.T, path, body string) string {
			t.Helper()
			req, err := http.NewRequestWithContext(opCtx(t), http.MethodPost, o.baseURL+path, strings.NewReader(body))
			require.NoError(t, err)
			resp, err := o.http.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equalf(t, http.StatusAccepted, resp.StatusCode, "%s: %s", path, b)
			return string(b)
		}

		startQuery := "?instanceID=" + id
		if o.appID != "" {
			startQuery += "&appID=" + o.appID
		}
		var start map[string]any
		require.NoError(t, json.Unmarshal([]byte(post(t, "/staged-wf/start"+startQuery, "")), &start))
		assert.Equal(t, id, start["instanceID"])
		if want {
			assert.Equal(t, true, start["staged"], "start")
		} else {
			// Left out of the body unless the start was staged.
			assert.NotContains(t, start, "staged", "start")
		}

		// The body is that of the response before it said whether the
		// operation was staged, unless it was.
		wantBody := `{}`
		if want {
			wantBody = `{"staged":true}`
		}
		assert.JSONEq(t, wantBody, post(t, "/"+id+"/pause"+query, ""), "pause")
		assert.JSONEq(t, wantBody, post(t, "/"+id+"/resume"+query, ""), "resume")
		assert.JSONEq(t, wantBody, post(t, "/"+id+"/raiseEvent/ping"+query, `"payload"`), "raise event")
		assert.JSONEq(t, wantBody, post(t, "/"+id+"/terminate"+query, ""), "terminate")
	})
}
