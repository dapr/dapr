/*
Copyright 2025 The Dapr Authors
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
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(triggerlatency))
}

type triggerlatency struct {
	actors *actors.Actors
}

func (r *triggerlatency) Setup(t *testing.T) []framework.Option {
	r.actors = actors.New(t,
		actors.WithActorTypes("my-type"),
		actors.WithActorTypeHandler("my-type", func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(time.Second)
		}),
		actors.WithHandler("/job/", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(time.Second / 2)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(r.actors),
	}
}

//nolint:testifylint
func (r *triggerlatency) Run(t *testing.T, ctx context.Context) {
	r.actors.WaitUntilRunning(t, ctx)

	client := r.actors.GRPCClient(t, ctx)

	for i := range 3 {
		_, err := client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
			Job: &rtv1.Job{
				Name:    strconv.Itoa(i),
				DueTime: ptr.Of("0s"),
			},
		})
		require.NoError(t, err)
	}

	for i := range 5 {
		_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "my-type",
			ActorId:   strconv.Itoa(i),
			Name:      strconv.Itoa(i),
			DueTime:   "0s",
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := r.actors.Scheduler().MetricsWithLabels(t, ctx)
		total, ok := metrics.Metrics["dapr_scheduler_jobs_triggered_total"]
		if !assert.True(c, ok) {
			return
		}
		assert.Equal(c, 3.0, total["type=job"])
		assert.Equal(c, 5.0, total["type=actor"])
	}, time.Second*15, time.Millisecond*10)

	metrics := r.actors.Scheduler().MetricsWithLabels(t, ctx)
	total, ok := metrics.Metrics["dapr_scheduler_trigger_latency_bucket"]
	require.True(t, ok)

	assert.Equal(t, 0.0, total["type=actor,le=100.000000"])
	assert.Equal(t, 0.0, total["type=actor,le=500.000000"])
	assert.Equal(t, 0.0, total["type=actor,le=1000.000000"])
	assert.Equal(t, 5.0, total["type=actor,le=5000.000000"])
	assert.Equal(t, 5.0, total["type=actor,le=10000.000000"])
	assert.Equal(t, 5.0, total["type=actor,le=+Inf"])

	assert.Equal(t, 0.0, total["type=job,le=100.000000"])
	assert.Equal(t, 0.0, total["type=job,le=500.000000"])
	assert.Equal(t, 3.0, total["type=job,le=1000.000000"])
	assert.Equal(t, 3.0, total["type=job,le=5000.000000"])
	assert.Equal(t, 3.0, total["type=job,le=10000.000000"])
	assert.Equal(t, 3.0, total["type=job,le=+Inf"])
}
