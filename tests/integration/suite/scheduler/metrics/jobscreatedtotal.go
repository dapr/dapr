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
	suite.Register(new(jobscreatedtotal))
}

type jobscreatedtotal struct {
	actors *actors.Actors
}

func (j *jobscreatedtotal) Setup(t *testing.T) []framework.Option {
	j.actors = actors.New(t,
		actors.WithActorTypes("my-type"),
		actors.WithActorTypeHandler("my-type", func(http.ResponseWriter, *http.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(j.actors),
	}
}

//nolint:testifylint
func (j *jobscreatedtotal) Run(t *testing.T, ctx context.Context) {
	j.actors.WaitUntilRunning(t, ctx)

	client := j.actors.GRPCClient(t, ctx)

	for i := range 10 {
		_, err := client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
			Job: &rtv1.Job{
				Name:    strconv.Itoa(i),
				DueTime: ptr.Of("100s"),
			},
		})
		require.NoError(t, err)
	}

	for i := range 5 {
		_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "my-type",
			ActorId:   strconv.Itoa(i),
			Name:      strconv.Itoa(i),
			DueTime:   "100s",
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := j.actors.Scheduler().MetricsWithLabels(t, ctx)
		total, ok := metrics.Metrics["dapr_scheduler_jobs_created_total"]
		if !assert.True(c, ok) {
			return
		}
		assert.Equal(c, 10.0, total["type=job"])
		assert.Equal(c, 5.0, total["type=actor"])
	}, time.Second*15, time.Millisecond*10)
}
