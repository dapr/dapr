/* Copyright 2024 The Dapr Authors
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

package noset

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(failthird))
}

type failthird struct {
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd
	respErr   atomic.Bool
	triggered slice.Slice[string]
}

func (f *failthird) Setup(t *testing.T) []framework.Option {
	f.triggered = slice.String()
	f.respErr.Store(true)

	app := app.New(t,
		app.WithOnJobEventFn(func(_ context.Context, req *rtv1.JobEventRequest) (*rtv1.JobEventResponse, error) {
			defer f.triggered.Append(req.GetName())
			if f.respErr.Load() {
				return nil, errors.New("an error")
			}
			return new(rtv1.JobEventResponse), nil
		}),
	)

	f.scheduler = scheduler.New(t)

	f.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithScheduler(f.scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(app, f.scheduler, f.daprd),
	}
}

func (f *failthird) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	_, err := f.daprd.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:    "test",
			DueTime: ptr.Of("0s"),
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test", "test", "test"}, f.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	f.respErr.Store(false)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test", "test", "test", "test"}, f.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.ElementsMatch(t, []string{"test", "test", "test", "test"}, f.triggered.Slice())
}
