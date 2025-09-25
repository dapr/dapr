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

package apphealth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	durabletask_client "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// luis case: app is unhealthy, worker is connected, app transitions to healthy

func init() {
	suite.Register(new(apphealth))
}

type apphealth struct {
	healthy  atomic.Bool
	app      *app.App
	workflow *workflow.Workflow
}

func (a *apphealth) Setup(t *testing.T) []framework.Option {
	a.healthy.Store(true)
	a.app = app.New(t,
		app.WithHealthCheckFn(func(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
			if a.healthy.Load() {
				return &rtv1.HealthCheckResponse{}, nil
			}
			return nil, errors.New("app not healthy")
		}),
	)

	a.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithAppPort(a.app.Port(t)),
			daprd.WithAppProtocol("grpc"),
			daprd.WithAppHealthCheck(true),
			daprd.WithAppHealthProbeInterval(1),
			daprd.WithAppHealthProbeThreshold(1),
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.app, a.workflow),
	}
}

func (a *apphealth) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.CallActivity("bar").Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("bar").Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	a.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	// app starts unhealthy
	a.healthy.Store(false)
	// we have no way of knowing when the sidecar detected if the app is unhealthy
	time.Sleep(time.Second * 3)

	// connect the worker
	client := durabletask_client.NewTaskHubGrpcClient(a.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client.StartWorkItemListener(ctx, a.workflow.Registry()))
	// this sleep is the key
	// it makes sure you get the placement tables update before the application becomes healthy
	// the root cause of the bug we were following was that the app transitioning to healthy was wiping out the actor types
	time.Sleep(time.Second * 10)

	// app transitions to healthy
	a.healthy.Store(true)
	// active actors is dependant on the app health checks
	// hence we cannot check the active actors while the app is unhealthy
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c,
			len(a.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 2)
	}, time.Second*10, time.Millisecond*10)

	scheduleCtx, scheduleCancel := context.WithTimeout(ctx, time.Second*10)
	t.Cleanup(scheduleCancel)
	id, err := client.ScheduleNewOrchestration(scheduleCtx, "foo")
	require.NoError(t, err, "failed to schedule workflow")
	waitCompletionCtx, waitCompletionCancel := context.WithTimeout(ctx, time.Second*10)
	t.Cleanup(waitCompletionCancel)
	meta, err := client.WaitForOrchestrationCompletion(waitCompletionCtx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())
}
