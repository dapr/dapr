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

package selfhosted

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
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(actorstatesecret))
}

type actorstatesecret struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	resDir  string
}

func (a *actorstatesecret) Setup(t *testing.T) []framework.Option {
	sched := scheduler.New(t)
	place := placement.New(t)

	a.logline = logline.New(t, logline.WithStdoutLineContains(
		"Component updated: mystore (state.in-memory/v1)",
	))

	a.resDir = t.TempDir()

	a.daprd = daprd.New(t,
		daprd.WithResourcesDir(a.resDir),
		daprd.WithScheduler(sched),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithExecOptions(
			exec.WithStdout(a.logline.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sched, place, a.logline, a.daprd),
	}
}

func (a *actorstatesecret) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

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

	_, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "SingleActivity",
	})
	require.Error(t, err)
	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, s.Code())
	require.Contains(t, err.Error(), "the state store is not configured to use the actor runtime")

	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"), []byte(`
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
 - name: mysecretvalue
   secretKeyRef:
     name: mysecret
     key: mysecret
auth:
 secretStore: mysecretstore
`), 0o600))

	a.logline.EventuallyFoundAll(t)
	assert.Empty(t, a.daprd.GetMetaRegisteredComponents(t, ctx))

	secretsFile := filepath.Join(t.TempDir(), "secrets.json")
	require.NoError(t, os.WriteFile(secretsFile, []byte(`{"mysecret": "myvalue"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "secret.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mysecretstore
spec:
 type: secretstores.local.file
 version: v1
 metadata:
 - name: secretsFile
   value: '%s'
`, secretsFile)), 0o600))

	gclient := a.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, serr := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "SingleActivity",
			InstanceId:        "parkedsecret",
			Input:             []byte(`"Dapr"`),
		})
		assert.NoError(c, serr)
	}, time.Second*30, time.Millisecond*100)

	meta, err := wfClient.WaitForWorkflowCompletion(ctx, api.InstanceID("parkedsecret"), api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
}
