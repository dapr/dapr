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

package ops

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(grpcapi))
}

// grpcapi exercises every workflow operation cross-app through the Dapr
// runtime gRPC API: app0's Dapr client targets workflows hosted on app1 via
// the app_id request field.
type grpcapi struct {
	workflow *workflow.Workflow
}

func (g *grpcapi) Setup(t *testing.T) []framework.Option {
	g.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(g.workflow),
	}
}

func (g *grpcapi) Run(t *testing.T, ctx context.Context) {
	g.workflow.WaitUntilRunning(t, ctx)

	g.workflow.RegistryN(1).AddWorkflowN("GRPCOpsWF", func(wctx *task.WorkflowContext) (any, error) {
		var payload string
		if err := wctx.WaitForSingleEvent("Finish", time.Hour).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	caller := g.workflow.GRPCClient(t, ctx)
	target := g.workflow.BackendClientN(t, ctx, 1)
	targetAppID := g.workflow.DaprN(1).AppID()

	waitTargetStatus := func(t *testing.T, id string, status api.OrchestrationStatus) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			meta, err := target.FetchWorkflowMetadata(ctx, api.InstanceID(id))
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, status, meta.GetRuntimeStatus())
		}, time.Second*30, time.Millisecond*10)
	}

	t.Run("start get raise", func(t *testing.T) {
		const id = "ops-grpc-1"
		_, err := caller.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
			WorkflowName:      "GRPCOpsWF",
			AppId:             &targetAppID,
		})
		require.NoError(t, err)
		waitTargetStatus(t, id, api.RUNTIME_STATUS_RUNNING)

		// Cross-app get through the runtime API.
		resp, err := caller.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
			AppId:             &targetAppID,
		})
		require.NoError(t, err)
		assert.Equal(t, "GRPCOpsWF", resp.GetWorkflowName())
		assert.Equal(t, "RUNNING", resp.GetRuntimeStatus())

		_, err = caller.RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
			EventName:         "Finish",
			EventData:         []byte(`"done"`),
			AppId:             &targetAppID,
		})
		require.NoError(t, err)
		waitTargetStatus(t, id, api.RUNTIME_STATUS_COMPLETED)
	})

	t.Run("pause resume terminate purge", func(t *testing.T) {
		const id = "ops-grpc-2"
		_, err := caller.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
			WorkflowName:      "GRPCOpsWF",
			AppId:             &targetAppID,
		})
		require.NoError(t, err)
		waitTargetStatus(t, id, api.RUNTIME_STATUS_RUNNING)

		_, err = caller.PauseWorkflowBeta1(ctx, &rtv1.PauseWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
			AppId:             &targetAppID,
		})
		require.NoError(t, err)
		waitTargetStatus(t, id, api.RUNTIME_STATUS_SUSPENDED)

		_, err = caller.ResumeWorkflowBeta1(ctx, &rtv1.ResumeWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
			AppId:             &targetAppID,
		})
		require.NoError(t, err)
		waitTargetStatus(t, id, api.RUNTIME_STATUS_RUNNING)

		_, err = caller.TerminateWorkflowBeta1(ctx, &rtv1.TerminateWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
			AppId:             &targetAppID,
		})
		require.NoError(t, err)
		waitTargetStatus(t, id, api.RUNTIME_STATUS_TERMINATED)

		_, err = caller.PurgeWorkflowBeta1(ctx, &rtv1.PurgeWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
			AppId:             &targetAppID,
		})
		require.NoError(t, err)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := target.FetchWorkflowMetadata(ctx, api.InstanceID(id))
			assert.ErrorIs(c, err, api.ErrInstanceNotFound)
		}, time.Second*30, time.Millisecond*10)
	})

	t.Run("invalid app id is rejected", func(t *testing.T) {
		const id = "ops-grpc-3"
		badAppID := ptr.Of("bad.app.id")

		// Every operation rejects the app ID with the same InvalidArgument
		// before any routing happens, so none of these can hang.
		for name, op := range map[string]func(context.Context) error{
			"start": func(opctx context.Context) error {
				_, err := caller.StartWorkflowBeta1(opctx, &rtv1.StartWorkflowRequest{
					InstanceId:        id,
					WorkflowComponent: "dapr",
					WorkflowName:      "GRPCOpsWF",
					AppId:             badAppID,
				})
				return err
			},
			"get": func(opctx context.Context) error {
				_, err := caller.GetWorkflowBeta1(opctx, &rtv1.GetWorkflowRequest{
					InstanceId:        id,
					WorkflowComponent: "dapr",
					AppId:             badAppID,
				})
				return err
			},
			"raise event": func(opctx context.Context) error {
				_, err := caller.RaiseEventWorkflowBeta1(opctx, &rtv1.RaiseEventWorkflowRequest{
					InstanceId:        id,
					WorkflowComponent: "dapr",
					EventName:         "Finish",
					EventData:         []byte(`"done"`),
					AppId:             badAppID,
				})
				return err
			},
			"pause": func(opctx context.Context) error {
				_, err := caller.PauseWorkflowBeta1(opctx, &rtv1.PauseWorkflowRequest{
					InstanceId:        id,
					WorkflowComponent: "dapr",
					AppId:             badAppID,
				})
				return err
			},
			"resume": func(opctx context.Context) error {
				_, err := caller.ResumeWorkflowBeta1(opctx, &rtv1.ResumeWorkflowRequest{
					InstanceId:        id,
					WorkflowComponent: "dapr",
					AppId:             badAppID,
				})
				return err
			},
			"terminate": func(opctx context.Context) error {
				_, err := caller.TerminateWorkflowBeta1(opctx, &rtv1.TerminateWorkflowRequest{
					InstanceId:        id,
					WorkflowComponent: "dapr",
					AppId:             badAppID,
				})
				return err
			},
			"purge": func(opctx context.Context) error {
				_, err := caller.PurgeWorkflowBeta1(opctx, &rtv1.PurgeWorkflowRequest{
					InstanceId:        id,
					WorkflowComponent: "dapr",
					AppId:             badAppID,
				})
				return err
			},
		} {
			t.Run(name, func(t *testing.T) {
				opctx, cancel := context.WithTimeout(ctx, time.Second*20)
				defer cancel()

				err := op(opctx)
				require.Error(t, err)
				assert.Equal(t, codes.InvalidArgument, status.Code(err))
				assert.Contains(t, err.Error(), "is invalid")
				require.NoError(t, opctx.Err(), "must be rejected up front, not by exhausting the timeout")
			})
		}
	})
}
