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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(upsert))
}

type upsert struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	daprd        *daprd.Daprd
	methodcalled atomic.Uint32
}

func (u *upsert) Setup(t *testing.T) []framework.Option {
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

func (u *upsert) Run(t *testing.T, ctx context.Context) {
	u.scheduler.WaitUntilRunning(t, ctx)
	u.place.WaitUntilRunning(t, ctx)
	u.daprd.WaitUntilRunning(t, ctx)

	gclient := u.daprd.GRPCClient(t, ctx)
	_, err := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   "1h",
	})
	require.NoError(t, err)

	time.Sleep(time.Second * 2)
	require.Equal(t, 0, int(u.methodcalled.Load()))

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   "1s",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return u.methodcalled.Load() == 1
	}, time.Second*5, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	require.Equal(t, 1, int(u.methodcalled.Load()))

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   time.Now().Format(time.RFC3339),
		Period:    "PT1S",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return u.methodcalled.Load() == 4
	}, time.Second*5, time.Millisecond*10)
}
