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

package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
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
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(remove))
}

type remove struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	triggered atomic.Int64

	daprd *daprd.Daprd
}

func (r *remove) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 2)
	port1 := fp.Port(t)
	port2 := fp.Port(t)
	r.scheduler = scheduler.New(t,
		scheduler.WithID("scheduler-0"),
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler-0=http://localhost:%d", port1)),
		scheduler.WithEtcdClientPort(port2),
	)

	app := app.New(t,
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {
		}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(http.ResponseWriter, *http.Request) {
			r.triggered.Add(1)
		}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {}),
		app.WithConfig(`{"entities": ["myactortype"]}`),
	)

	r.place = placement.New(t)

	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
	)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(app, r.scheduler, r.place, r.daprd),
	}
}

func (r *remove) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	client := r.daprd.GRPCClient(t, ctx)

	// should have the same path separator across OS
	etcdKeysPrefix := "dapr/jobs"

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, r.scheduler.ListAllKeys(t, ctx, etcdKeysPrefix))
	}, time.Second*10, 10*time.Millisecond)

	_, err := client.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = client.RegisterActorReminder(ctx, &runtimev1pb.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0s",
		Period:    "1s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.scheduler.ListAllKeys(t, ctx, etcdKeysPrefix), 1)
	}, time.Second*10, 10*time.Millisecond)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, r.triggered.Load(), int64(1))
	}, 30*time.Second, 10*time.Millisecond, "failed to wait for 'triggered' to be greater or equal 1, actual value "+strconv.FormatInt(r.triggered.Load(), 10))

	_, err = client.UnregisterActorReminder(ctx, &runtimev1pb.UnregisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, r.scheduler.ListAllKeys(t, ctx, etcdKeysPrefix))
	}, time.Second*10, 10*time.Millisecond)
}
