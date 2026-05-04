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
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

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
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(peroperation))
}

type peroperation struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd

	resDir string

	// Distinct names per write let the metadata-API poll deterministically
	// detect that hot-reload took effect.
	policySerial atomic.Uint64
}

const (
	peropTargetAppID = "perop-target"
	peropCallerAppID = "perop-caller"
)

func (p *peroperation) Setup(t *testing.T) []framework.Option {
	p.sentry = sentry.New(t)
	p.place = placement.New(t, placement.WithSentry(t, p.sentry))
	p.sched = scheduler.New(t, scheduler.WithSentry(p.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	p.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: peropconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true
  - name: HotReload
    enabled: true`), 0o600))

	p.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(p.resDir, "perop-init.yaml"), p.policyAllowAll("perop-init"), 0o600))

	p.caller = daprd.New(t,
		daprd.WithAppID(peropCallerAppID),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithSchedulerAddresses(p.sched.Address()),
		daprd.WithSentry(t, p.sentry),
	)
	p.target = daprd.New(t,
		daprd.WithAppID(peropTargetAppID),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(p.resDir),
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithSchedulerAddresses(p.sched.Address()),
		daprd.WithSentry(t, p.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(p.sentry, p.place, p.sched, p.db, p.caller, p.target),
	}
}

func (p *peroperation) policyAllowAll(name string) []byte {
	return []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: ` + name + `
scopes:
- ` + peropTargetAppID + `
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: ` + peropTargetAppID + `
    workflows:
    - name: "*"
      operations: [schedule, terminate, raise, pause, resume, purge, get, rerun]
      action: allow
    activities:
    - name: "*"
      action: allow
  - callers:
    - appID: ` + peropCallerAppID + `
    workflows:
    - name: "*"
      operations: [schedule, terminate, raise, pause, resume, purge, get, rerun]
      action: allow
`)
}

func (p *peroperation) policyOnlyAllowOp(name, callerAppID, op string) []byte {
	return []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: ` + name + `
scopes:
- ` + peropTargetAppID + `
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: ` + peropTargetAppID + `
    workflows:
    - name: "*"
      operations: [schedule, terminate, raise, pause, resume, purge, get, rerun]
      action: allow
    activities:
    - name: "*"
      action: allow
  - callers:
    - appID: ` + callerAppID + `
    workflows:
    - name: "PerOpWF"
      operations: [` + op + `]
      action: allow
`)
}

func (p *peroperation) applyPolicyAllowAll(t *testing.T, ctx context.Context) {
	t.Helper()
	name := p.nextPolicyName()
	p.writePolicy(t, ctx, name, p.policyAllowAll(name))
}

func (p *peroperation) applyPolicyOnlyAllowOp(t *testing.T, ctx context.Context, callerAppID, op string) {
	t.Helper()
	name := p.nextPolicyName()
	p.writePolicy(t, ctx, name, p.policyOnlyAllowOp(name, callerAppID, op))
}

func (p *peroperation) nextPolicyName() string {
	return fmt.Sprintf("perop-%d", p.policySerial.Add(1))
}

func (p *peroperation) writePolicy(t *testing.T, ctx context.Context, name string, body []byte) {
	t.Helper()

	entries, err := os.ReadDir(p.resDir)
	require.NoError(t, err)
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".yaml" {
			require.NoError(t, os.Remove(filepath.Join(p.resDir, e.Name())))
		}
	}

	require.NoError(t, os.WriteFile(filepath.Join(p.resDir, name+".yaml"), body, 0o600))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		policies := p.target.GetMetadata(t, ctx).WorkflowAccessPolicies
		if !assert.Len(c, policies, 1, "exactly one policy should be loaded") {
			return
		}
		assert.Equal(c, name, policies[0].GetName(),
			"metadata should reflect the just-written policy name once hot-reload completes")
	}, time.Second*20, time.Millisecond*10)
}

func (p *peroperation) Run(t *testing.T, ctx context.Context) {
	p.place.WaitUntilRunning(t, ctx)
	p.sched.WaitUntilRunning(t, ctx)
	p.caller.WaitUntilRunning(t, ctx)
	p.target.WaitUntilRunning(t, ctx)

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("PerOpWF", func(ctx *task.WorkflowContext) (any, error) {
		var payload string
		_ = ctx.WaitForSingleEvent("FinishEvent", time.Hour).Await(&payload)
		return payload, nil
	}))
	targetClient := client.NewTaskHubGrpcClient(p.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	callerReg := task.NewTaskRegistry()
	callerClient := client.NewTaskHubGrpcClient(p.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(p.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	scheduleInstance := func(t *testing.T) api.InstanceID {
		t.Helper()
		p.applyPolicyAllowAll(t, ctx)
		id, err := targetClient.ScheduleNewWorkflow(ctx, "PerOpWF")
		require.NoError(t, err)
		_, err = targetClient.WaitForWorkflowStart(ctx, id)
		require.NoError(t, err)
		return id
	}

	t.Run("local", func(t *testing.T) {
		t.Run("schedule", func(t *testing.T) {
			p.applyPolicyOnlyAllowOp(t, ctx, peropTargetAppID, "schedule")
			id, err := targetClient.ScheduleNewWorkflow(ctx, "PerOpWF")
			require.NoError(t, err)
			_, _ = targetClient.WaitForWorkflowStart(ctx, id)
		})

		t.Run("terminate", func(t *testing.T) {
			id := scheduleInstance(t)
			p.applyPolicyOnlyAllowOp(t, ctx, peropTargetAppID, "terminate")
			require.NoError(t, targetClient.TerminateWorkflow(ctx, id))
		})

		t.Run("raise event", func(t *testing.T) {
			id := scheduleInstance(t)
			p.applyPolicyOnlyAllowOp(t, ctx, peropTargetAppID, "raise")
			require.NoError(t, targetClient.RaiseEvent(ctx, id, "FinishEvent", api.WithEventPayload("done")))
		})

		t.Run("pause and resume", func(t *testing.T) {
			id := scheduleInstance(t)
			p.applyPolicyOnlyAllowOp(t, ctx, peropTargetAppID, "pause")
			require.NoError(t, targetClient.SuspendWorkflow(ctx, id, ""))

			p.applyPolicyOnlyAllowOp(t, ctx, peropTargetAppID, "resume")
			require.NoError(t, targetClient.ResumeWorkflow(ctx, id, ""))
		})

		t.Run("purge after termination", func(t *testing.T) {
			id := scheduleInstance(t)
			require.NoError(t, targetClient.TerminateWorkflow(ctx, id))
			_, err := targetClient.WaitForWorkflowCompletion(ctx, id)
			require.NoError(t, err)

			p.applyPolicyOnlyAllowOp(t, ctx, peropTargetAppID, "purge")
			require.NoError(t, targetClient.PurgeWorkflowState(ctx, id))
		})

		t.Run("get", func(t *testing.T) {
			id := scheduleInstance(t)
			p.applyPolicyOnlyAllowOp(t, ctx, peropTargetAppID, "get")
			meta, err := targetClient.FetchWorkflowMetadata(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, "PerOpWF", meta.GetName())
		})

		t.Run("rerun", func(t *testing.T) {
			id := scheduleInstance(t)
			require.NoError(t, targetClient.TerminateWorkflow(ctx, id))
			_, err := targetClient.WaitForWorkflowCompletion(ctx, id)
			require.NoError(t, err)

			p.applyPolicyOnlyAllowOp(t, ctx, peropTargetAppID, "rerun")
			newID, err := targetClient.RerunWorkflowFromEvent(ctx, id, 0)
			require.NoError(t, err)
			assert.NotEmpty(t, newID)
		})
	})

	// Cross-sidecar terminate/raise/etc aren't exposed by the durabletask client
	// (it targets the local daprd), so we bypass the SDK and craft raw
	// InvokeActor calls.
	callerActorClient := runtimev1pb.NewDaprClient(p.caller.GRPCConn(t, ctx))
	targetWorkflowActorType := "dapr.internal.default." + peropTargetAppID + ".workflow"

	t.Run("remote", func(t *testing.T) {
		t.Run("schedule allowed", func(t *testing.T) {
			p.applyPolicyOnlyAllowOp(t, ctx, peropCallerAppID, "schedule")
			payload := mustMarshalCreate(t, "PerOpWF", "remote-schedule-1")
			_, err := callerActorClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
				ActorType: targetWorkflowActorType,
				ActorId:   "remote-schedule-1",
				Method:    "CreateWorkflowInstance",
				Data:      payload,
			})
			require.NoError(t, err, "remote schedule must succeed when policy allows it")
		})

		t.Run("schedule denied when only terminate is allowed", func(t *testing.T) {
			p.applyPolicyOnlyAllowOp(t, ctx, peropCallerAppID, "terminate")
			payload := mustMarshalCreate(t, "PerOpWF", "remote-schedule-2")
			_, err := callerActorClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
				ActorType: targetWorkflowActorType,
				ActorId:   "remote-schedule-2",
				Method:    "CreateWorkflowInstance",
				Data:      payload,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "access denied by workflow access policy")
		})

		t.Run("terminate denied when only schedule is allowed", func(t *testing.T) {
			id := scheduleInstance(t)
			p.applyPolicyOnlyAllowOp(t, ctx, peropCallerAppID, "schedule")
			payload := mustMarshalHistoryEvent(t, &protos.HistoryEvent{
				EventType: &protos.HistoryEvent_ExecutionTerminated{
					ExecutionTerminated: &protos.ExecutionTerminatedEvent{},
				},
			})
			_, err := callerActorClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
				ActorType: targetWorkflowActorType,
				ActorId:   string(id),
				Method:    "AddWorkflowEvent",
				Data:      payload,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "access denied by workflow access policy")
		})

		t.Run("purge denied when only get is allowed", func(t *testing.T) {
			id := scheduleInstance(t)
			require.NoError(t, targetClient.TerminateWorkflow(ctx, id))
			_, err := targetClient.WaitForWorkflowCompletion(ctx, id)
			require.NoError(t, err)

			p.applyPolicyOnlyAllowOp(t, ctx, peropCallerAppID, "get")
			_, err = callerActorClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
				ActorType: targetWorkflowActorType,
				ActorId:   string(id),
				Method:    "PurgeWorkflowState",
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "access denied by workflow access policy")
		})
	})

	p.applyPolicyAllowAll(t, ctx)
}

func mustMarshalCreate(t *testing.T, workflowName, instanceID string) []byte {
	t.Helper()
	data, err := proto.Marshal(&protos.CreateWorkflowInstanceRequest{
		StartEvent: &protos.HistoryEvent{
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: workflowName,
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId: instanceID,
					},
				},
			},
		},
	})
	require.NoError(t, err)
	return data
}

func mustMarshalHistoryEvent(t *testing.T, ev *protos.HistoryEvent) []byte {
	t.Helper()
	data, err := proto.Marshal(ev)
	require.NoError(t, err)
	return data
}
