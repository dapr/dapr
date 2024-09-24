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

package metrics

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	frameworkclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	scheduler *scheduler.Scheduler

	idPrefix string
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	uuid, err := uuid.NewUUID()
	require.NoError(t, err)
	b.idPrefix = uuid.String()

	b.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(b.scheduler),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)

	frameworkClient := frameworkclient.HTTP(t)
	client := b.scheduler.Client(t, ctx)

	t.Run("create 10 jobs, ensure metrics", func(t *testing.T) {
		for i := 1; i <= 10; i++ {
			name := b.idPrefix + "_" + strconv.Itoa(i)

			req := &schedulerv1.ScheduleJobRequest{
				Name: name,
				Job: &schedulerv1.Job{
					Schedule: ptr.Of("@every 20s"),
					Repeats:  ptr.Of(uint32(1)),
					Data: &anypb.Any{
						Value: []byte(b.idPrefix),
					},
					Ttl: ptr.Of("30s"),
				},
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: new(schedulerv1.JobTargetMetadata_Job),
					},
				},
			}

			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)

			assert.True(t, b.etcdHasJob(t, ctx, name))

			b.assertMetricExists(t, ctx, frameworkClient, "dapr_scheduler_jobs_created_total", i)
		}
	})
}

func (b *basic) etcdHasJob(t *testing.T, ctx context.Context, key string) bool {
	t.Helper()

	// Get keys with prefix
	keys, err := b.scheduler.ETCDClient(t).Get(ctx, "", clientv3.WithPrefix())
	require.NoError(t, err)

	for _, k := range keys {
		if strings.HasSuffix(k, "||"+key) {
			return true
		}
	}

	return false
}

// assert the metric exists and the count is correct
func (b *basic) assertMetricExists(t *testing.T, ctx context.Context, client *http.Client, expectedMetric string, expectedCount int) {
	t.Helper()

	metricReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", b.scheduler.MetricsPort()), nil)
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
	assert.True(t, foundMetric, "Expected metric %s not found", expectedMetric)
}
