/*
Copyright 2024 The Dapr Authors
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

package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/client"
	"github.com/microsoft/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(deletereminder))
}

type deletereminder struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (d *deletereminder) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true`), 0o600))

	app := app.New(t)
	d.place = placement.New(t)
	d.scheduler = procscheduler.New(t)
	d.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
		daprd.WithConfigs(configFile),
	)

	return []framework.Option{
		framework.WithProcesses(d.scheduler, d.place, app, d.daprd),
	}
}

func (d *deletereminder) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)
	d.place.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"localhost:" + d.scheduler.EtcdClientPort()},
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	kvs, err := etcdClient.KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	require.Empty(t, kvs.Count)

	var runSingleActivity atomic.Uint32

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddOrchestratorN("SingleActivity", func(c *task.OrchestrationContext) (any, error) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			kvs, err = etcdClient.KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
			//nolint:testifylint
			if assert.NoError(c, err) {
				assert.Len(c, kvs.Kvs, 1)
			}
		}, 15*time.Second, 10*time.Millisecond)

		name := string(kvs.Kvs[0].Key)
		name = name[strings.LastIndex(name, "|")+1:]
		var expectedName string
		if runSingleActivity.Add(1) == 1 {
			expectedName = "start-"
		} else {
			expectedName = "new-event-"
		}
		assert.True(t, strings.HasPrefix(name, expectedName), "expected job name to start with %q but got %q", expectedName, name)

		var input string
		if err = c.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err = c.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)

		kvs, err = etcdClient.KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
		require.NoError(t, err)
		require.Len(t, kvs.Kvs, 1)
		name = string(kvs.Kvs[0].Key)
		name = name[strings.LastIndex(name, "|")+1:]
		assert.True(t, strings.HasPrefix(name, "new-event-"), "expected job name to start with  'new-event-' but got %q", name)

		return output, err
	}))
	require.NoError(t, r.AddActivityN("SayHello", func(c task.ActivityContext) (any, error) {
		kvs, err = etcdClient.KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
		require.NoError(t, err)
		require.Len(t, kvs.Kvs, 1)
		name := string(kvs.Kvs[0].Key)
		name = name[strings.LastIndex(name, "|")+1:]
		assert.Equal(t, "run-activity", name)

		var inp string
		if err = c.GetInput(&inp); err != nil {
			return nil, err
		}

		kvs, err = etcdClient.KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
		require.NoError(t, err)
		require.Len(t, kvs.Kvs, 1)
		name = string(kvs.Kvs[0].Key)
		name = name[strings.LastIndex(name, "|")+1:]
		assert.Equal(t, "run-activity", name)

		return fmt.Sprintf("Hello, %s!", inp), nil
	}))

	backendClient := client.NewTaskHubGrpcClient(d.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := d.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "SingleActivity",
		Input:             []byte(`"Dapr"`),
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, metadata.IsComplete())
	assert.Equal(t, `"Hello, Dapr!"`, metadata.SerializedOutput)

	kvs, err = etcdClient.KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	require.Empty(t, kvs.Count)
}
