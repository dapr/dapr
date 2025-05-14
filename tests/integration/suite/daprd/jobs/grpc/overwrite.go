package grpc

import (
	"context"
	"testing"

	"github.com/diagridio/go-etcd-cron/api/errors"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(overwrite))
}

type overwrite struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.scheduler = scheduler.New(t)

	o.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(o.scheduler, o.daprd),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)
	o.daprd.WaitUntilRunning(t, ctx)

	client := o.daprd.GRPCClient(t, ctx)

	t.Run("overwrite if exists", func(t *testing.T) {
		for _, req := range []*rtv1.ScheduleJobRequest{
			{Job: &rtv1.Job{
				Name:      "overwrite1",
				Schedule:  ptr.Of("@daily"),
				Repeats:   ptr.Of(uint32(1)),
				Overwrite: true,
			}}, {Job: &rtv1.Job{
				Name:      "overwrite1",
				Schedule:  ptr.Of("@daily"),
				Repeats:   ptr.Of(uint32(1)),
				Overwrite: true,
			}},
		} {
			_, err := client.ScheduleJobAlpha1(ctx, req)
			require.NoError(t, err)
		}
	})

	t.Run("do not overwrite if exists", func(t *testing.T) {
		r := &rtv1.ScheduleJobRequest{Job: &rtv1.Job{
			Name:     "overwrite2",
			Schedule: ptr.Of("@daily"),
			Repeats:  ptr.Of(uint32(1)),
		}}
		_, err := client.ScheduleJobAlpha1(ctx, r)
		require.NoError(t, err)

		for _, req := range []*rtv1.ScheduleJobRequest{
			{Job: &rtv1.Job{
				Name:      "overwrite2",
				Schedule:  ptr.Of("@daily"),
				Repeats:   ptr.Of(uint32(1)),
				Overwrite: false,
			}},
		} {
			_, err := client.ScheduleJobAlpha1(ctx, req)
			require.Error(t, err)
			require.True(t, errors.IsJobAlreadyExists(err))
		}
	})
}
