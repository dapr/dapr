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

	"github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(jobsfailedtotal))
}

type jobsfailedtotal struct {
	actors *actors.Actors
}

func (j *jobsfailedtotal) Setup(t *testing.T) []framework.Option {
	j.actors = actors.New(t,
		actors.WithActorTypes("my-type"),
		actors.WithActorTypeHandler("my-type", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}),
		actors.WithHandler("/job/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(j.actors),
	}
}

//nolint:testifylint
func (j *jobsfailedtotal) Run(t *testing.T, ctx context.Context) {
	j.actors.WaitUntilRunning(t, ctx)

	client := j.actors.GRPCClient(t, ctx)

	for i := range 5 {
		_, err := client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
			Job: &rtv1.Job{
				Name:    strconv.Itoa(i),
				DueTime: ptr.Of("0s"),
				FailurePolicy: &common.JobFailurePolicy{
					Policy: &common.JobFailurePolicy_Drop{
						Drop: new(common.JobFailurePolicyDrop),
					},
				},
			},
		})
		require.NoError(t, err)
	}

	for i := range 3 {
		_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "my-type",
			ActorId:   strconv.Itoa(i),
			Name:      strconv.Itoa(i),
			DueTime:   "0s",
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := j.actors.Scheduler().MetricsWithLabels(t, ctx)
		total, ok := metrics.Metrics["dapr_scheduler_jobs_failed_total"]
		if !assert.True(c, ok) {
			return
		}
		assert.Equal(c, 5.0, total["type=job"])
		assert.Equal(c, 9.0, total["type=actor"])
	}, time.Second*15, time.Millisecond*10)
}
