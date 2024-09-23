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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(metrics))
}

type metrics struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	triggered atomic.Int64

	daprd    *daprd.Daprd
	etcdPort int
}

func (m *metrics) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true`), 0o600)) // nolint:mnd

	fp := ports.Reserve(t, 2) // nolint:mnd
	port1 := fp.Port(t)
	port2 := fp.Port(t)
	m.etcdPort = port2
	clientPorts := []string{
		"scheduler-0=" + strconv.Itoa(m.etcdPort),
	}
	m.scheduler = scheduler.New(t,
		scheduler.WithID("scheduler-0"),
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler-0=http://localhost:%d", port1)),
		scheduler.WithInitialClusterPorts(port1),
		scheduler.WithEtcdClientPorts(clientPorts),
	)

	app := app.New(t,
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(http.ResponseWriter, *http.Request) {
			m.triggered.Add(1)
		}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {}),
		app.WithConfig(`{"entities": ["myactortype"]}`),
	)

	m.place = placement.New(t)

	m.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithSchedulerAddresses(m.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
	)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(app, m.scheduler, m.place, m.daprd),
	}
}

func (m *metrics) Run(t *testing.T, ctx context.Context) {
	m.scheduler.WaitUntilRunning(t, ctx)
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	grpcClient := m.daprd.GRPCClient(t, ctx)

	etcdClient := client.Etcd(t, clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("localhost:%d", m.etcdPort)},
		DialTimeout: 5 * time.Second, // nolint:mnd
	})

	// Use "path/filepath" import, it is using OS specific path separator unlike "path"
	etcdKeysPrefix := filepath.Join("dapr", "jobs")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys, rerr := etcdClient.ListAllKeys(ctx, etcdKeysPrefix)
		require.NoError(c, rerr)
		assert.Empty(c, keys)
	}, time.Second*10, 10*time.Millisecond) // nolint:mnd

	// assert false, since count is 0 here
	m.assertMetricExists(t, ctx, httpClient, "dapr_scheduler_jobs_created_total", 0)

	_, err := grpcClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = grpcClient.RegisterActorReminder(ctx, &runtimev1pb.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0s",
		Period:    "1s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys, rerr := etcdClient.ListAllKeys(ctx, etcdKeysPrefix)
		require.NoError(c, rerr)
		assert.Len(c, keys, 1)
	}, time.Second*10, 10*time.Millisecond) // nolint:mnd

	// true, since count is 1
	m.assertMetricExists(t, ctx, httpClient, "dapr_scheduler_jobs_created_total", 1)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, m.triggered.Load(), int64(1))
	}, 30*time.Second, 10*time.Millisecond, "failed to wait for 'triggered' to be greatrer or equal 1, actual value %d", m.triggered.Load()) // nolint:mnd
}

// assert the metric exists and the count is correct
func (m *metrics) assertMetricExists(t *testing.T, ctx context.Context, client *http.Client, expectedMetric string, expectedCount int) {
	t.Helper()

	metricReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", m.scheduler.MetricsPort()), nil)
	require.NoError(t, err)

	resp, err := client.Do(metricReq)
	require.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	foundMetric := false

	for _, line := range bytes.Split(respBody, []byte("\n")) {
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		split := bytes.Split(line, []byte(" "))
		if len(split) != 2 { // nolint:mnd
			continue
		}

		// dapr_scheduler_jobs_created_total{app_id="appid"}
		metricName := string(split[0])
		metricVal := string(split[1])
		if !strings.Contains(metricName, expectedMetric) {
			continue
		}
		if strings.Contains(metricName, expectedMetric) {
			metricCount, err := strconv.Atoi(metricVal)
			require.NoError(t, err)
			assert.Equal(t, expectedCount, metricCount)
			foundMetric = true
			break
		}
	}
	if expectedCount > 0 {
		assert.True(t, foundMetric, "Expected metric %s not found", expectedMetric)
	} else {
		assert.False(t, foundMetric)
	}
}
