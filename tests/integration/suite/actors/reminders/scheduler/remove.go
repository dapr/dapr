/*
Copyright 2024 The Dapr Authors
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

package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(remove))
}

type remove struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	triggered atomic.Int64

	daprd    *daprd.Daprd
	etcdPort int
}

func (r *remove) Setup(t *testing.T) []framework.Option {
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

	fp := ports.Reserve(t, 2)
	port1 := fp.Port(t)
	port2 := fp.Port(t)
	r.etcdPort = port2
	clientPorts := []string{
		"scheduler-0=" + strconv.Itoa(r.etcdPort),
	}
	r.scheduler = scheduler.New(t,
		scheduler.WithID("scheduler-0"),
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler-0=http://localhost:%d", port1)),
		scheduler.WithInitialClusterPorts(port1),
		scheduler.WithEtcdClientPorts(clientPorts),
	)

	app := app.New(t,
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(http.ResponseWriter, *http.Request) {
			r.triggered.Add(1)
		}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {}),
		app.WithConfig(`{"entities": ["myactortype"]}`),
	)

	r.place = placement.New(t)

	r.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
	)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(app, r.scheduler, r.place, r.daprd),
	}
}

func (r *remove) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	client := r.daprd.GRPCClient(t, ctx)

	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("localhost:%d", r.etcdPort)},
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, etcdClient.Close()) })

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rerr := etcdClient.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
		require.NoError(c, rerr)
		assert.Equal(c, int64(0), resp.Count)
	}, 10*time.Second, 10*time.Millisecond)

	_, err = client.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = client.RegisterActorReminder(ctx, &runtimev1pb.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0s",
		Period:    "1s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rerr := etcdClient.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
		require.NoError(c, rerr)
		assert.Equal(c, int64(1), resp.Count)
	}, 10*time.Second, 10*time.Millisecond)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), r.triggered.Load())
	}, 30*time.Second, 10*time.Millisecond)

	_, err = client.UnregisterActorReminder(ctx, &runtimev1pb.UnregisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := etcdClient.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
		require.NoError(c, err)
		assert.Equal(c, int64(0), resp.Count)
	}, 10*time.Second, 10*time.Millisecond)
}
