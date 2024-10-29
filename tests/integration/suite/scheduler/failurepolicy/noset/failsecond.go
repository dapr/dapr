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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(failsecond))
}

type failsecond struct {
	scheduler *scheduler.Scheduler
}

func (f *failsecond) Setup(t *testing.T) []framework.Option {
	f.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(f.scheduler),
	}
}

func (f *failsecond) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)

	client := f.scheduler.Client(t, ctx)

	_, err := client.ScheduleJob(ctx, f.scheduler.JobNowJob("test", "namespace", "appid1"))
	require.NoError(t, err)

	var respStatus atomic.Value
	respStatus.Store(schedulerv1.WatchJobsRequestResultStatus_FAILED)

	triggered := f.scheduler.WatchJobs(t, ctx, &schedulerv1.WatchJobsRequestInitial{
		AppId: "appid1", Namespace: "namespace",
	}, &respStatus)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test", "test"}, triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	respStatus.Store(schedulerv1.WatchJobsRequestResultStatus_SUCCESS)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test", "test", "test"}, triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.ElementsMatch(t, []string{"test", "test", "test"}, triggered.Slice())
}
