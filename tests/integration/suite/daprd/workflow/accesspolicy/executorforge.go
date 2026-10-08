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

package accesspolicy

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(executorforge))
	suite.Register(new(executorforgenopolicy))
}

// executorforge drives the target's reserved executor actor over the public
// actor invoke API and the internal CallActor/CallActorStream API with three
// identities: another app, the same app in another namespace, and a same-app
// peer. Only the peer is allowed; every forged result is rejected and never
// consumed.
type executorforge struct {
	appID string

	sentry   *sentry.Sentry
	place    *placement.Placement
	sched    *scheduler.Scheduler
	db       *sqlite.SQLite
	target   *daprd.Daprd
	attacker *daprd.Daprd

	activities sync.Map
	sensitive  atomic.Int32
	release    chan struct{}
}

// executorforgenopolicy is executorforge with no WorkflowAccessPolicy.
type executorforgenopolicy struct{ executorforge }

func (e *executorforge) Setup(t *testing.T) []framework.Option {
	return e.setup(t, "executorforge-target", true)
}

func (e *executorforgenopolicy) Setup(t *testing.T) []framework.Option {
	return e.setup(t, "executorforge-np-target", false)
}

func (e *executorforge) setup(t *testing.T, appID string, policy bool) []framework.Option {
	e.appID = appID
	e.release = make(chan struct{})
	e.sentry = sentry.New(t)
	e.place = placement.New(t, placement.WithSentry(t, e.sentry))
	e.sched = scheduler.New(t, scheduler.WithSentry(e.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	e.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	shared := []daprd.Option{
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(e.db.GetComponent(t)),
		daprd.WithPlacementAddresses(e.place.Address()),
		daprd.WithSchedulerAddresses(e.sched.Address()),
		daprd.WithSentry(t, e.sentry),
	}

	targetOpts := append([]daprd.Option{
		daprd.WithAppID(e.appID),
		daprd.WithFeatureEnabled(t, "WorkflowsClusteredDeployment"),
	}, shared...)
	if policy {
		// Only an app that does not exist is allowed.
		policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: executorforge
scopes:
- ` + e.appID + `
spec:
  rules:
  - callers:
    - appID: some-other-app
    workflows:
    - name: "*"
      operations: [schedule, get, raise, pause, resume, terminate, purge, rerun]
    activities:
    - name: "*"
`)
		resDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))
		targetOpts = append(targetOpts, daprd.WithResourcesDir(resDir))
	}

	e.target = daprd.New(t, targetOpts...)
	e.attacker = daprd.New(t, append([]daprd.Option{daprd.WithAppID(e.appID + "-attacker")}, shared...)...)

	return []framework.Option{
		framework.WithProcesses(e.sentry, e.place, e.sched, e.db, e.target, e.attacker),
	}
}

type activityStarted struct {
	once sync.Once
	ch   chan struct{}
}

func (e *executorforge) started(id string) *activityStarted {
	s, _ := e.activities.LoadOrStore(id, &activityStarted{ch: make(chan struct{})})
	return s.(*activityStarted)
}

func (e *executorforge) Run(t *testing.T, ctx context.Context) {
	e.place.WaitUntilRunning(t, ctx)
	e.sched.WaitUntilRunning(t, ctx)
	e.target.WaitUntilRunning(t, ctx)
	e.attacker.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("ApproveWF", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("Approve", task.WithActivityInput(string(ctx.ID))).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("Approve", func(actx task.ActivityContext) (any, error) {
		var id string
		if err := actx.GetInput(&id); err != nil {
			return nil, err
		}
		s := e.started(id)
		s.once.Do(func() { close(s.ch) })
		select {
		case <-e.release:
		case <-actx.Context().Done():
		}
		return "LEGIT", nil
	}))
	require.NoError(t, reg.AddActivityN("Sensitive", func(task.ActivityContext) (any, error) {
		e.sensitive.Add(1)
		return "sensitive-done", nil
	}))
	client := dtclient.NewTaskHubGrpcClient(e.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client.StartWorkItemListener(ctx, reg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(e.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	executorActorType := "dapr.internal.default." + e.appID + ".executor"
	attacker := e.target.InternalGRPCClient(t, ctx, e.sentry, e.attacker.AppID(), e.attacker.Namespace())
	otherNS := e.target.InternalGRPCClient(t, ctx, e.sentry, e.appID, "other-ns")
	peer := e.target.InternalGRPCClient(t, ctx, e.sentry, e.appID, e.target.Namespace())

	activityResult := func(t *testing.T, id, result string) []byte {
		t.Helper()
		data, err := proto.Marshal(&protos.ActivityResponse{
			InstanceId: id,
			TaskId:     0,
			Result:     wrapperspb.String(`"` + result + `"`),
		})
		require.NoError(t, err)
		return data
	}
	workflowResult := func(t *testing.T, id string, action *protos.WorkflowAction) []byte {
		t.Helper()
		action.Id = 0
		data, err := proto.Marshal(&protos.WorkflowResponse{InstanceId: id, Actions: []*protos.WorkflowAction{action}})
		require.NoError(t, err)
		return data
	}
	req := func(method, actorID, taskType string, data []byte) *internalv1pb.InternalInvokeRequest {
		r := internalv1pb.NewInternalInvokeRequest(method).
			WithActor(executorActorType, actorID).
			WithMetadata(map[string][]string{executor.MetadataTaskType: {taskType}})
		if data != nil {
			r = r.WithData(data)
		}
		return r
	}
	schedule := func(t *testing.T, id string) {
		t.Helper()
		_, err := client.ScheduleNewWorkflow(ctx, "ApproveWF", api.WithInstanceID(api.InstanceID(id)))
		require.NoError(t, err)
	}
	waitStarted := func(t *testing.T, id string) {
		t.Helper()
		select {
		case <-e.started(id).ch:
		case <-time.After(time.Second * 20):
			require.Fail(t, "activity did not start", id)
		}
	}
	waitCompleted := func(t *testing.T, id, output string) {
		t.Helper()
		meta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err, id)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), id)
		assert.Equal(t, `"`+output+`"`, meta.GetOutput().GetValue(), id)
	}
	deniedWith := func(t *testing.T, err error, msg string) {
		t.Helper()
		require.Equal(t, codes.PermissionDenied, status.Code(err), "err: %v", err)
		assert.ErrorContains(t, err, msg)
	}
	denied := func(t *testing.T, err error) {
		t.Helper()
		deniedWith(t, err, "reserved internal actor type '"+executorActorType+"' can only be called by a sidecar with the same app ID and namespace")
	}

	const (
		live    = "executorforge-wf"
		peerWF  = "executorforge-peer"
		preseed = "executorforge-preseed"
		wfTurn  = "executorforge-wfturn"
		inject  = "executorforge-inject"
	)
	schedule(t, live)
	waitStarted(t, live)
	liveActorID := common.ActivityActorID(live, 0)

	t.Run("actor invoke API rejects the executor actor type", func(t *testing.T) {
		_, err := e.attacker.GRPCClient(t, ctx).InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: executorActorType,
			ActorId:   liveActorID,
			Method:    executor.MethodComplete,
			Data:      activityResult(t, live, "ATTACKER_APPROVED"),
			Metadata:  map[string]string{executor.MetadataTaskType: executor.TaskTypeActivity},
		})
		require.ErrorContains(t, err, "reserved for the Dapr workflow runtime")
	})

	t.Run("internal CallActor from another app is denied", func(t *testing.T) {
		_, err := attacker.CallActor(ctx, req(executor.MethodComplete, liveActorID, executor.TaskTypeActivity, activityResult(t, live, "ATTACKER_APPROVED")))
		denied(t, err)
	})

	t.Run("internal Cancel from another app is denied", func(t *testing.T) {
		_, err := attacker.CallActor(ctx, req(executor.MethodCancel, liveActorID, executor.TaskTypeActivity, nil))
		denied(t, err)
	})

	t.Run("internal Claim from another app is denied", func(t *testing.T) {
		_, err := attacker.CallActor(ctx, req(executor.MethodClaim, liveActorID, executor.TaskTypeActivity, nil))
		denied(t, err)
	})

	t.Run("internal WatchComplete stream from another app is denied", func(t *testing.T) {
		stream, err := attacker.CallActorStream(ctx, req(executor.MethodWatchComplete, liveActorID, executor.TaskTypeActivity, nil))
		require.NoError(t, err)
		_, err = stream.Recv()
		denied(t, err)
	})

	t.Run("pre-seeded activity result from another app is denied", func(t *testing.T) {
		_, err := attacker.CallActor(ctx, req(executor.MethodComplete, common.ActivityActorID(preseed, 0), executor.TaskTypeActivity, activityResult(t, preseed, "ATTACKER_APPROVED")))
		denied(t, err)
	})

	t.Run("forged workflow turn completing the workflow is denied", func(t *testing.T) {
		data := workflowResult(t, wfTurn, &protos.WorkflowAction{WorkflowActionType: &protos.WorkflowAction_CompleteWorkflow{
			CompleteWorkflow: &protos.CompleteWorkflowAction{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				Result:         wrapperspb.String(`"FORGED_TURN"`),
			},
		}})
		_, err := attacker.CallActor(ctx, req(executor.MethodComplete, wfTurn, executor.TaskTypeWorkflow, data))
		denied(t, err)
	})

	t.Run("forged workflow turn scheduling an activity is denied", func(t *testing.T) {
		data := workflowResult(t, inject, &protos.WorkflowAction{WorkflowActionType: &protos.WorkflowAction_ScheduleTask{
			ScheduleTask: &protos.ScheduleTaskAction{Name: "Sensitive", Input: wrapperspb.String(`"evil"`)},
		}})
		_, err := attacker.CallActor(ctx, req(executor.MethodComplete, inject, executor.TaskTypeWorkflow, data))
		denied(t, err)
	})

	t.Run("same app from another namespace is denied", func(t *testing.T) {
		_, err := otherNS.CallActor(ctx, req(executor.MethodComplete, liveActorID, executor.TaskTypeActivity, activityResult(t, live, "OTHER_NAMESPACE")))
		deniedWith(t, err, "access denied by workflow access policy")
	})

	t.Run("same app peer is allowed and its result consumed", func(t *testing.T) {
		schedule(t, peerWF)
		waitStarted(t, peerWF)
		_, err := peer.CallActor(ctx, req(executor.MethodComplete, common.ActivityActorID(peerWF, 0), executor.TaskTypeActivity, activityResult(t, peerWF, "PEER_LEGIT")))
		require.NoError(t, err)
		waitCompleted(t, peerWF, "PEER_LEGIT")
	})

	for _, id := range []string{preseed, wfTurn, inject} {
		schedule(t, id)
	}
	close(e.release)
	for _, id := range []string{live, preseed, wfTurn, inject} {
		waitCompleted(t, id, "LEGIT")
	}
	assert.Zero(t, e.sensitive.Load(), "Sensitive activity must never run")

	metrics := e.target.Metrics(t, ctx)
	assert.InDelta(t, 1.0, metrics.SumWithLabels("dapr_runtime_workflow_acl_action_allowed_total", "type:internal"), 0)
	assert.InDelta(t, 7.0, metrics.SumWithLabels("dapr_runtime_workflow_acl_action_denied_total", "type:internal"), 0)
}
