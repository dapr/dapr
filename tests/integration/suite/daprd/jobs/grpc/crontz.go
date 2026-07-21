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

package grpc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(crontz))
}

// crontz tests a CRON_TZ= schedule is evaluated in that zone, not the
// scheduler's local one.
type crontz struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobCh     chan *runtimev1pb.JobEventRequest
}

func (c *crontz) Setup(t *testing.T) []framework.Option {
	c.scheduler = scheduler.New(t)

	c.jobCh = make(chan *runtimev1pb.JobEventRequest, 2)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			c.jobCh <- in
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	c.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(c.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(c.scheduler, srv, c.daprd),
	}
}

func (c *crontz) Run(t *testing.T, ctx context.Context) {
	c.scheduler.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	client := c.daprd.GRPCClient(t, ctx)
	loc := offsetZone(t)

	schedule := func(t *testing.T, name, sched string) {
		t.Helper()
		_, err := client.ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{Name: name, Schedule: new(sched)},
		})
		require.NoError(t, err)
	}

	t.Run("with CRON_TZ prefix the job fires at the zone's wall clock", func(t *testing.T) {
		target := time.Now().In(loc).Add(time.Second * 20)
		schedule(t, "crontz-honoured", fmt.Sprintf("CRON_TZ=%s * %d %d * * *",
			loc, target.Minute(), target.Hour()))

		select {
		case job := <-c.jobCh:
			assert.Equal(t, "job/crontz-honoured", job.GetMethod())
		case <-time.After(time.Second * 60):
			assert.Fail(t, "timed out waiting for job to trigger",
				"schedule was due at %s in %s; the CRON_TZ prefix appears to have been ignored",
				target.Format(time.TimeOnly), loc)
		}

		_, err := client.DeleteJob(ctx, &runtimev1pb.DeleteJobRequest{Name: "crontz-honoured"})
		require.NoError(t, err)
	})

	t.Run("an unknown timezone is rejected", func(t *testing.T) {
		_, err := client.ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{Name: "crontz-badzone", Schedule: new("CRON_TZ=Not/AZone 0 0 9 * * *")},
		})
		require.Error(t, err)
	})

	t.Run("without the prefix the same wall clock is a different instant", func(t *testing.T) {
		target := time.Now().In(loc).Add(time.Second * 20)
		schedule(t, "crontz-absent", fmt.Sprintf("* %d %d * * *",
			target.Minute(), target.Hour()))

		select {
		case job := <-c.jobCh:
			assert.Fail(t, "job triggered unexpectedly",
				"%q fired inside the observation window, but without a CRON_TZ prefix "+
					"its wall-clock time resolves to the scheduler's local zone, which is "+
					"at least 30 minutes from %s", job.GetMethod(), loc)
		case <-time.After(time.Second * 20):
		}
	})
}

func offsetZone(t *testing.T) *time.Location {
	t.Helper()

	now := time.Now()
	_, local := now.Zone()

	for _, name := range []string{"Asia/Kolkata", "Pacific/Marquesas", "Asia/Tokyo", "Pacific/Kiritimati"} {
		loc, err := time.LoadLocation(name)
		require.NoError(t, err, "host is missing the tzdata database")

		if _, off := now.In(loc).Zone(); absInt(off-local) >= 3*60*60 {
			return loc
		}
	}

	require.Fail(t, "no candidate timezone is far enough from the host's local zone")

	return nil
}

func absInt(i int) int {
	if i < 0 {
		return -i
	}

	return i
}
