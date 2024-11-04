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

package staging

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(durable))
}

type durable struct {
	scheduler *scheduler.Scheduler
}

func (d *durable) Setup(t *testing.T) []framework.Option {
	d.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(d.scheduler),
	}
}

func (d *durable) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)

	client := d.scheduler.Client(t, ctx)
	for i := range 40 {
		_, err := client.ScheduleJob(ctx,
			d.scheduler.JobNowJob(strconv.Itoa(i), "namespace", "appid"),
		)
		require.NoError(t, err)
	}

	time.Sleep(time.Second * 3)

	triggered := d.scheduler.WatchJobsSuccess(t, ctx,
		&schedulerv1pb.WatchJobsRequestInitial{
			Namespace: "namespace", AppId: "appid",
		},
	)

	exp := []string{
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"10", "11", "12", "13", "14", "15", "16", "17", "18", "19",
		"20", "21", "22", "23", "24", "25", "26", "27", "28", "29",
		"30", "31", "32", "33", "34", "35", "36", "37", "38", "39",
	}

	got := make([]string, 0, 40)
	for range exp {
		select {
		case name := <-triggered:
			got = append(got, name)
		case <-time.After(time.Second * 5):
			require.Fail(t, "timed out waiting for job")
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, got, exp)
	}, time.Second*10, time.Millisecond*10)

	select {
	case <-triggered:
		assert.Fail(t, "unexpected trigger")
	case <-time.After(time.Second * 2):
	}
}
