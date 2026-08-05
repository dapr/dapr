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

package hotreload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(statestore))
}

// statestore ensures that a daprd which starts with a state store that is not
// an actor state store has no workflow API available, then hot reloading the
// component to be marked as the actor state store makes the workflow API
// available.
type statestore struct {
	daprd  *daprd.Daprd
	resDir string
}

func (s *statestore) Setup(t *testing.T) []framework.Option {
	sched := scheduler.New(t)
	place := placement.New(t)

	s.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "state.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
`), 0o600))

	s.daprd = daprd.New(t,
		daprd.WithResourcesDir(s.resDir),
		daprd.WithScheduler(sched),
		daprd.WithPlacementAddresses(place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sched, place, s.daprd),
	}
}

func (s *statestore) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	require.Len(t, s.daprd.GetMetaRegisteredComponents(t, ctx), 1)

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

	cl := client.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, reg))

	// The state store is not an actor state store, so the workflow API is not
	// available.
	_, err := s.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowName: "SingleActivity",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	require.Contains(t, err.Error(), "the state store is not configured to use the actor runtime")

	// Update the component to be marked as the actor state store. It should be
	// hot reloaded and the workflow API become available.
	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "state.yaml"), []byte(`
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
`), 0o600))

	// The component is hot reloaded asynchronously, so poll until the workflow
	// API becomes available. The fixed instance ID means only the first
	// successful start creates the workflow instance.
	gclient := s.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, serr := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "SingleActivity",
			InstanceId:        "statestore-hotreload",
			Input:             []byte(`"Dapr"`),
		})
		assert.NoError(c, serr)
	}, time.Second*30, time.Millisecond*100)

	meta, err := cl.WaitForWorkflowCompletion(ctx, api.InstanceID("statestore-hotreload"), api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
}
