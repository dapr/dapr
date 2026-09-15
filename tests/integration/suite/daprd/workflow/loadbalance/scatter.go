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

package loadbalance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/tests/integration/framework"
	fgrpc "github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(scatter))
}

// scatter runs workers behind a per-call round-robin load balancer over a
// clustered deployment, so completion calls land on any daprd.
type scatter struct {
	workflow *workflow.Workflow
}

func (s *scatter) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.NewClustered(t, 3)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *scatter) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const (
		daprds    = 3
		perShape  = 20
		fanWidth  = 5
		seqLength = 5
	)

	for i := range daprds {
		reg := s.workflow.RegistryN(i)
		require.NoError(t, reg.AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, fanWidth)
			for j := range fanWidth {
				tasks[j] = ctx.CallActivity("noop", task.WithActivityInput(j))
			}
			for _, tk := range tasks {
				if err := tk.Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
		require.NoError(t, reg.AddWorkflowN("sequential", func(ctx *task.WorkflowContext) (any, error) {
			for j := range seqLength {
				if err := ctx.CallActivity("slow", task.WithActivityInput(j)).Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
		require.NoError(t, reg.AddWorkflowN("can", func(ctx *task.WorkflowContext) (any, error) {
			var gen int
			if err := ctx.GetInput(&gen); err != nil {
				return nil, err
			}
			if err := ctx.CallActivity("noop", task.WithActivityInput(gen)).Await(nil); err != nil {
				return nil, err
			}
			if gen < 3 {
				ctx.ContinueAsNew(gen + 1)
			}
			return nil, nil
		}))
		require.NoError(t, reg.AddActivityN("noop", func(task.ActivityContext) (any, error) {
			return nil, nil
		}))
		require.NoError(t, reg.AddActivityN("slow", func(actx task.ActivityContext) (any, error) {
			select {
			case <-actx.Context().Done():
				return nil, actx.Context().Err()
			case <-time.After(20 * time.Millisecond):
			}
			return nil, nil
		}))
	}

	for i := range daprds {
		s.workflow.BackendClientN(t, ctx, i)
	}

	conns := make([]grpc.ClientConnInterface, daprds)
	for i := range daprds {
		conns[i] = s.workflow.DaprN(i).GRPCConn(t, ctx)
	}

	for i := range daprds {
		lb := client.NewTaskHubGrpcClient(fgrpc.LoadBalance(t, conns...), logger.New(t))
		require.NoError(t, lb.StartWorkItemListener(ctx, s.workflow.RegistryN(i)))
	}

	lbClient := client.NewTaskHubGrpcClient(fgrpc.LoadBalance(t, conns...), logger.New(t))

	ids := make([]api.InstanceID, 0, perShape*3)
	for _, shape := range []string{"fanout", "sequential", "can"} {
		for i := range perShape {
			id := api.InstanceID(fmt.Sprintf("scatter-%s-%d", shape, i))
			_, err := lbClient.ScheduleNewWorkflow(ctx, shape, api.WithInstanceID(id), api.WithInput(0))
			require.NoError(t, err)
			ids = append(ids, id)
		}
	}

	fworkflow.WaitForAllCompleted(t, ctx, lbClient, ids...)
}
