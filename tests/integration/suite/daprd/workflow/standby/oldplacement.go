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
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(oldplacement))
}

const (
	// oldPlacementGrace shortens the time placement is given to place the
	// standby types before daprd warns (DAPR_ACTORS_TEST_STANDBY_GRACE). It
	// stays above the control plane's 8s dissemination timeout, which a round
	// stalled on a slow host can take to complete.
	oldPlacementGrace = time.Second * 10

	oldPlacementWarning = "The placement server does not support the WorkflowsStandbyHosts preview feature"
)

// oldplacement checks the fallback with a control plane which predates
// standby hosts. Each daprd, of its own app with standby hosts, reports to
// placement through a proxy, of the placement service or of the scheduler
// serving placement (DAPR_INTEGRATION_SCHEDULER_PLACEMENT). The proxy of
// daprd 0 clears the standby types of every report before forwarding it:
// an old server's decoder keeps them only as an unknown field it ignores. Without a worker, that
// app behaves as with the feature off: a start blocks until its deadline and
// leaves no instance behind, a status read from another app finds no host
// and blocks until its deadline too, and daprd warns once that placement
// lacks the feature. Once a worker connects, workflows run as normal. The
// proxy of daprd 1 forwards reports unchanged: standby works through it and
// nothing is logged, so the proxy itself is transparent.
type oldplacement struct {
	workflow   *workflow.Workflow
	oldLog     *logline.LogLine
	controlLog *logline.LogLine
	proxies    []*placementProxy
}

func (o *oldplacement) Setup(t *testing.T) []framework.Option {
	o.oldLog = logline.New(t, logline.WithCaptureAll())
	o.controlLog = logline.New(t, logline.WithCaptureAll())
	procs := []process.Interface{o.oldLog, o.controlLog}

	dopts := func(l *logline.LogLine) []daprd.Option {
		return []daprd.Option{
			daprd.WithAppID(uuid.NewString()),
			daprd.WithExecOptions(
				exec.WithStdout(l.Stdout()),
				exec.WithStderr(l.Stderr()),
				exec.WithEnvVars(t, "DAPR_ACTORS_TEST_STANDBY_GRACE", oldPlacementGrace.String()),
			),
		}
	}
	oldOpts, controlOpts := dopts(o.oldLog), dopts(o.controlLog)

	// The proxies speak plaintext, so the control plane runs without mTLS.
	wopts := []workflow.Option{
		workflow.WithDaprds(2),
		workflow.WithStandbyHosts(),
		workflow.WithSigning(false),
	}

	if workflow.SchedulerPlacementFromEnv() {
		sched := scheduler.New(t, scheduler.WithPlacementEnabled(true))
		oldProxy := proxy.New(t, sched, proxy.WithoutStandbyActorTypes())
		controlProxy := proxy.New(t, sched)
		procs = append(procs, sched, oldProxy, controlProxy)
		wopts = append(wopts,
			workflow.WithSchedulerPlacement(),
			workflow.WithSchedulerInstance(sched),
			workflow.WithSchedulerAddress(oldProxy.Address()),
		)
		controlOpts = append(controlOpts, daprd.WithSchedulerAddressesReset(controlProxy.Address()))
	} else {
		o.proxies = []*placementProxy{newPlacementProxy(t, true), newPlacementProxy(t, false)}
		procs = append(procs, o.proxies[0], o.proxies[1])
		wopts = append(wopts, workflow.WithPlacementService())
		oldOpts = append(oldOpts, daprd.WithPlacementAddresses(o.proxies[0].address()))
		controlOpts = append(controlOpts, daprd.WithPlacementAddresses(o.proxies[1].address()))
	}

	wopts = append(wopts,
		workflow.WithDaprdOptions(0, oldOpts...),
		workflow.WithDaprdOptions(1, controlOpts...),
	)
	o.workflow = workflow.New(t, wopts...)
	for _, p := range o.proxies {
		p.upstream = o.workflow.Placement().Address()
	}

	procs = append(procs, o.workflow)
	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (o *oldplacement) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	for i := range 2 {
		require.NoError(t, o.workflow.RegistryN(i).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
			return "done", nil
		}))
	}

	oldMgmt := o.workflow.ManagementClientN(t, ctx, 0)
	controlMgmt := o.workflow.ManagementClientN(t, ctx, 1)

	// Through the transparent proxy the standby host is placed: the start
	// returns with the instance pending.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		sctx, cancel := context.WithTimeout(ctx, time.Second*3)
		defer cancel()
		_, err := controlMgmt.ScheduleNewWorkflow(sctx, "wf", api.WithInstanceID("control"))
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*50)
	meta, err := controlMgmt.FetchWorkflowMetadata(ctx, "control")
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())

	// The old placement never places the standby types, which daprd notices
	// once the grace period has passed.
	o.oldLog.EventuallyContains(t, oldPlacementWarning, oldPlacementGrace+time.Second*20, time.Millisecond*50)

	// As with the feature off, no daprd hosts the workflow actor type: the
	// start blocks until its deadline, and leaves no instance behind.
	sctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	_, err = oldMgmt.ScheduleNewWorkflow(sctx, "wf", api.WithInstanceID("old-early"))
	require.True(t, deadlineExceeded(err), err)
	_, err = oldMgmt.FetchWorkflowMetadata(ctx, "old-early")
	require.ErrorIs(t, err, api.ErrInstanceNotFound)

	// A status read from another app finds no address for the instance's
	// workflow actor, so it too fails at its deadline.
	sctx, cancel = context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	_, err = controlMgmt.FetchWorkflowMetadata(sctx, "old-early", api.WithFetchAppID(o.workflow.DaprN(0).AppID()))
	require.True(t, deadlineExceeded(err), err)

	o.workflow.ConnectWorkerN(t, ctx, 0, o.workflow.RegistryN(0))
	o.workflow.ConnectWorkerN(t, ctx, 1, o.workflow.RegistryN(1))

	meta, err = controlMgmt.WaitForWorkflowCompletion(ctx, "control")
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// With a worker, the old placement places the active host as before.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		sctx, cancel := context.WithTimeout(ctx, time.Second*3)
		defer cancel()
		_, err := oldMgmt.ScheduleNewWorkflow(sctx, "wf", api.WithInstanceID("old-late"))
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*50)
	meta, err = oldMgmt.WaitForWorkflowCompletion(ctx, "old-late")
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// The start which timed out left nothing to run.
	_, err = oldMgmt.FetchWorkflowMetadata(ctx, "old-early")
	require.ErrorIs(t, err, api.ErrInstanceNotFound)

	assert.Equal(t, 1, o.oldLog.Count(oldPlacementWarning))
	assert.Equal(t, 0, o.controlLog.Count(oldPlacementWarning))
}

// deadlineExceeded reports whether err is a call's deadline passing, as seen
// by either the client or the server, whichever noticed first.
func deadlineExceeded(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded
}

// placementProxy fronts the placement service. With strip set it clears
// Host.standby_entities on every report from daprd, simulating a placement
// server which predates standby hosts.
type placementProxy struct {
	v1pb.UnimplementedPlacementServer

	strip    bool
	lis      net.Listener
	srv      *grpc.Server
	upstream string
}

func newPlacementProxy(t *testing.T, strip bool) *placementProxy {
	return &placementProxy{
		strip: strip,
		lis:   ports.Reserve(t, 1).Listener(t),
	}
}

func (p *placementProxy) address() string { return p.lis.Addr().String() }

func (p *placementProxy) Run(t *testing.T, ctx context.Context) {
	p.srv = grpc.NewServer()
	v1pb.RegisterPlacementServer(p.srv, p)
	go p.srv.Serve(p.lis)
}

func (p *placementProxy) Cleanup(t *testing.T) {
	p.srv.Stop()
}

func (p *placementProxy) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	conn, err := grpc.NewClient(p.upstream, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	upstream, err := v1pb.NewPlacementClient(conn).ReportDaprStatus(ctx)
	if err != nil {
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		for {
			host, rerr := stream.Recv()
			if rerr != nil {
				errCh <- rerr
				return
			}
			if p.strip {
				host.StandbyEntities = nil
			}
			if serr := upstream.Send(host); serr != nil {
				errCh <- serr
				return
			}
		}
	}()
	go func() {
		for {
			order, rerr := upstream.Recv()
			if rerr != nil {
				errCh <- rerr
				return
			}
			if serr := stream.Send(order); serr != nil {
				errCh <- serr
				return
			}
		}
	}()

	if err := <-errCh; !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
