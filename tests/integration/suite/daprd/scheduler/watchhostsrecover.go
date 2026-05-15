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

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(watchhostsrecover))
}

type watchhostsrecover struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (w *watchhostsrecover) Setup(t *testing.T) []framework.Option {
	w.scheduler = scheduler.New(t)
	w.proxy = proxy.New(t, w.scheduler)

	w.proxy.ArmFailures(proxy.MethodWatchHosts, 1, codes.Unavailable, nil)

	w.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(w.proxy.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(w.scheduler, w.proxy, w.daprd),
	}
}

func (w *watchhostsrecover) Run(t *testing.T, ctx context.Context) {
	w.daprd.WaitUntilRunning(t, ctx)

	require.GreaterOrEqual(t, w.proxy.FailedCount(), 1,
		"expected the injected WatchHosts failure to have fired at least once")

	gclient := w.daprd.GRPCClient(t, ctx)
	_, err := gclient.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:     "watchhostsrecover-job",
			Schedule: ptr.Of("@every 1s"),
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, gerr := gclient.GetJobAlpha1(ctx, &rtv1.GetJobRequest{
			Name: "watchhostsrecover-job",
		})
		assert.NoError(c, gerr)
	}, 10*time.Second, 10*time.Millisecond)
}
