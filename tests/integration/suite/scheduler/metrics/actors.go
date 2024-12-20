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

package metrics

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actors))
}

type actors struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	triggered atomic.Int64

	daprd *daprd.Daprd
}

func (a *actors) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)

	app := app.New(t,
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(http.ResponseWriter, *http.Request) {
			a.triggered.Add(1)
		}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {}),
		app.WithConfig(`{"entities": ["myactortype"]}`),
	)

	a.place = placement.New(t)
	a.daprd = daprd.New(t,
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true
`),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, a.scheduler, a.place, a.daprd),
	}
}

func (a *actors) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	grpcClient := a.daprd.GRPCClient(t, ctx)

	// should have the same path separator across OS
	etcdKeysPrefix := "dapr/jobs"

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, a.scheduler.ListAllKeys(t, ctx, etcdKeysPrefix))
	}, time.Second*10, 10*time.Millisecond)

	metrics := a.scheduler.Metrics(t, ctx).All()
	assert.Equal(t, 0, int(metrics["dapr_scheduler_jobs_created_total"]))

	_, err := grpcClient.RegisterActorReminder(ctx, &runtimev1pb.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0s",
		Period:    "1s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics = a.scheduler.Metrics(t, ctx).All()
		assert.Equal(c, 1, int(metrics["dapr_scheduler_jobs_created_total"]))
	}, time.Second*4, 10*time.Millisecond)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, a.scheduler.ListAllKeys(t, ctx, etcdKeysPrefix), 1)
	}, time.Second*10, 10*time.Millisecond)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, a.triggered.Load(), int64(1))
	}, 30*time.Second, 10*time.Millisecond, "failed to wait for 'triggered' to be greater or equal 1, actual value %d", a.triggered.Load())
}
