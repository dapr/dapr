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

package standby

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(stale))
}

// staleReportDelay is how long the forwarder reports its actor type changes
// to placement late, holding every placement table stale for its worker.
const staleReportDelay = time.Second * 15

// stale checks the standby host's refusal of a reminder, the backstop for a
// reminder forwarded through a stale placement table. The standby host
// (daprd 0) holds a workflow which was started and sent an event while no
// worker was connected. A worker connects to the forwarder, another daprd of
// the app without standby hosts, which reports the change to placement late
// (DAPR_ACTORS_TEST_DELAY_TYPES_REPORT). A worker connecting to another app
// (daprd 1) completes a round in the meantime, after which the forwarder
// advertises the workflow actor type to the scheduler while its table still
// routes the workflow to the standby host. The scheduler delivers the staged
// reminders to the forwarder, which forwards them to the standby host, which
// refuses them with FailedPrecondition, which fails their triggers. Once the
// forwarder's change is placed, the retried reminders run on the forwarder:
// the workflow completes with its event, its first turn and its activity
// running once.
type stale struct {
	workflow  *workflow.Workflow
	forwarder *daprd.Daprd
	logline   *logline.LogLine
}

func (s *stale) Setup(t *testing.T) []framework.Option {
	// The forwarder's built-in actor retries log each refusal it receives.
	s.logline = logline.New(t, logline.WithStdoutLineContains(
		"code = FailedPrecondition desc = workflow actor 'standby-stale' is on a standby host",
	))

	s.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithStandbyHosts(),
	)

	dopts := []daprd.Option{
		daprd.WithAppID(s.workflow.Dapr().AppID()),
		daprd.WithResourceFiles(s.workflow.DB().GetComponent(t)),
		daprd.WithSchedulerAddresses(s.workflow.Scheduler().Address()),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_ACTORS_TEST_DELAY_TYPES_REPORT", staleReportDelay.String(),
		)),
	}
	if s.workflow.HasPlacement() {
		dopts = append(dopts, daprd.WithPlacementAddresses(s.workflow.Placement().Address()))
	}
	if s.workflow.Sentry() != nil {
		dopts = append(dopts, daprd.WithSentry(t, s.workflow.Sentry()))
	}
	// The harness's features without standby hosts: the forwarder hosts no
	// workflow actor type until its worker connects, so the stale table
	// routes the workflow to the standby host alone.
	var features []string
	if s.workflow.ClusteredDeployment() {
		features = append(features, "WorkflowsClusteredDeployment")
	}
	if s.workflow.FastPath() {
		features = append(features, "WorkflowsFastPath")
	}
	if s.workflow.Signing() {
		features = append(features, "WorkflowHistorySigning")
	}
	if len(features) > 0 {
		dopts = append(dopts, daprd.WithFeatureEnabled(t, features...))
	}
	dopts = append(dopts, daprd.WithLogLevel("debug"), daprd.WithLogLineStdout(s.logline))
	s.forwarder = daprd.New(t, dopts...)

	return []framework.Option{
		framework.WithProcesses(s.logline, s.workflow, s.forwarder),
	}
}

func (s *stale) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	s.forwarder.WaitUntilRunning(t, ctx)

	var starts, acts atomic.Int64
	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		if !ctx.IsReplaying {
			starts.Add(1)
		}
		var ev string
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(&ev); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("act").Await(nil); err != nil {
			return nil, err
		}
		return ev, nil
	}))
	require.NoError(t, reg.AddActivityN("act", func(task.ActivityContext) (any, error) {
		acts.Add(1)
		return nil, nil
	}))

	mgmt := s.workflow.ManagementClient(t, ctx)

	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	// The standby host serves the start and the event once it is placed.
	var id api.InstanceID
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		id, err = mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("standby-stale"))
		assert.NoError(c, err)
	}, time.Second*5, time.Millisecond*50)
	require.NoError(t, mgmt.RaiseEvent(octx, id, "go", api.WithEventPayload("raised")))

	// The worker's types are hosted on the forwarder at once, but reported to
	// placement only after staleReportDelay.
	cl := client.NewTaskHubGrpcClient(s.forwarder.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, reg))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		md := s.forwarder.GetMetadata(c, ctx)
		if assert.NotNil(c, md) && assert.NotNil(c, md.ActorRuntime) {
			assert.GreaterOrEqual(c, len(md.ActorRuntime.ActiveActors), 3)
		}
	}, time.Second*10, time.Millisecond*10)

	// A round for another app's change makes the forwarder advertise its
	// types to the scheduler, with the workflow still routed to the standby
	// host.
	s.workflow.ConnectWorkerN(t, ctx, 1, s.workflow.RegistryN(1))

	s.logline.EventuallyFoundAll(t)

	meta, err := mgmt.FetchWorkflowMetadata(octx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())

	// The refused trigger failed back to the scheduler.
	failed := fmt.Sprintf("dapr_runtime_actor_reminders_fired_total|actor_type:%s|app_id:%s|success:false",
		s.workflow.WorkflowActorType(0), s.forwarder.AppID())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, s.forwarder.Metrics(c, ctx).All()[failed])
	}, time.Second*10, time.Millisecond*50)

	meta, err = mgmt.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"raised"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(1), starts.Load())
	assert.Equal(t, int64(1), acts.Load())
}
