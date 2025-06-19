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

package drop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(failed))
}

type failed struct {
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd
	triggered chan string
}

func (f *failed) Setup(t *testing.T) []framework.Option {
	f.triggered = make(chan string, 10)
	f.scheduler = scheduler.New(t)

	app := app.New(t,
		app.WithOnJobEventFn(func(_ context.Context, req *rtv1.JobEventRequest) (*rtv1.JobEventResponse, error) {
			f.triggered <- req.GetName()
			return nil, errors.New("an error")
		}),
	)

	f.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithScheduler(f.scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(app, f.scheduler, f.daprd),
	}
}

func (f *failed) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	_, err := f.daprd.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:    "test",
			DueTime: ptr.Of("0s"),
			FailurePolicy: &corev1.JobFailurePolicy{
				Policy: &corev1.JobFailurePolicy_Drop{
					Drop: &corev1.JobFailurePolicyDrop{},
				},
			},
		},
	})
	require.NoError(t, err)

	// Should trigger once immediately
	select {
	case name := <-f.triggered:
		assert.Equal(t, "test", name)
	case <-time.After(time.Second * 1):
		require.Fail(t, "timed out waiting for job")
	}

	// Should not trigger any more
	select {
	case <-f.triggered:
		assert.Fail(t, "unexpected trigger")
	case <-time.After(time.Second * 3):
	}
}
