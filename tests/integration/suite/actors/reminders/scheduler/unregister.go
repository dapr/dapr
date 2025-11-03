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
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unregister))
}

type unregister struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	daprd        *daprd.Daprd
	methodcalled atomic.Uint32
}

func (u *unregister) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithConfig(`{"entities": ["myactortype"]}`),
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/remind/xyz",
			func(_ http.ResponseWriter, r *http.Request) { u.methodcalled.Add(1) }),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {}),
	)

	u.scheduler = scheduler.New(t)

	u.place = placement.New(t)
	u.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(u.place.Address()),
		daprd.WithSchedulerAddresses(u.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(u.scheduler, u.place, app, u.daprd),
	}
}

func (u *unregister) Run(t *testing.T, ctx context.Context) {
	u.scheduler.WaitUntilRunning(t, ctx)
	u.place.WaitUntilRunning(t, ctx)
	u.daprd.WaitUntilRunning(t, ctx)

	gclient := u.daprd.GRPCClient(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Name:      "xyz",
			DueTime:   time.Now().Format(time.RFC3339),
			Period:    "PT1S",
		})
		if err != nil {
			st, ok := status.FromError(err)
			assert.True(c, ok, "expected a gRPC status error, got %v", err)
			assert.Equal(c, codes.Unavailable, st.Code(), "the only allowed error is 'Unavailable', but got %v", err)
		}
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.GreaterOrEqual(ct, int(u.methodcalled.Load()), 2)
	}, time.Second*5, time.Millisecond*10)

	_, err := gclient.UnregisterActorReminder(ctx, &rtv1.UnregisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
	})
	require.NoError(t, err)

	time.Sleep(time.Second * 3)
	last := u.methodcalled.Load()

	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		called := u.methodcalled.Load()
		prevLast := last
		last = called
		assert.Equal(ct, int(prevLast), int(called))
	}, time.Second*15, time.Second*2)

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   time.Now().Format(time.RFC3339),
	})
	require.NoError(t, err)

	// Sleep to give time for any ongoing call to take place.
	time.Sleep(time.Second * 2)
	// Last method invoke after unregister.
	last = u.methodcalled.Load()

	// Sleep some time to make sure nothing was called again.
	time.Sleep(time.Second * 5)
	assert.Eventually(t, func() bool {
		return u.methodcalled.Load() == last
	}, time.Second*20, time.Millisecond*10)
}
