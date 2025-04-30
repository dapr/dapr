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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(distributed))
}

type distributed struct {
	db        *sqlite.SQLite
	app1      *app.App
	app2      *app.App
	place     *placement.Placement
	scheduler *scheduler.Scheduler
}

func (d *distributed) Setup(t *testing.T) []framework.Option {
	d.db = sqlite.New(t, sqlite.WithActorStateStore(true))
	d.app1 = app.New(t,
		app.WithConfig(`{"entities": ["myactortype","myactortype2"]}`),
		app.WithHandlerFunc("/actors", func(http.ResponseWriter, *http.Request) {}),
	)
	d.app2 = app.New(t,
		app.WithConfig(`{"entities": ["myactortype"]}`),
		app.WithHandlerFunc("/actors", func(http.ResponseWriter, *http.Request) {}),
	)
	d.scheduler = scheduler.New(t)
	d.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(d.db, d.scheduler, d.place, d.app1, d.app2),
	}
}

func (d *distributed) Run(t *testing.T, ctx context.Context) {
	schedOffConfig := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: false
`

	optsApp1 := []daprd.Option{
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
		daprd.WithAppPort(d.app1.Port()),
		daprd.WithConfigManifests(t, schedOffConfig),
	}
	optsApp2 := []daprd.Option{
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
		daprd.WithAppPort(d.app2.Port()),
	}
	optsApp1WithScheduler := []daprd.Option{
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
		daprd.WithAppPort(d.app1.Port()),
	}

	daprd1 := daprd.New(t, optsApp1...)
	daprd2 := daprd.New(t, optsApp2...)
	daprd3 := daprd.New(t, optsApp2...)
	daprd4 := daprd.New(t, optsApp2...)
	daprd5 := daprd.New(t, optsApp2...)
	daprd6 := daprd.New(t, optsApp1WithScheduler...)

	daprd1.Run(t, ctx)
	t.Cleanup(func() { daprd1.Cleanup(t) })
	daprd1.WaitUntilRunning(t, ctx)
	client := daprd1.GRPCClient(t, ctx)

	var wg sync.WaitGroup
	wg.Add(200)
	for i := range 100 {
		go func(i int) {
			defer wg.Done()
			_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
				ActorType: "myactortype",
				ActorId:   strconv.Itoa(i),
				Name:      strconv.Itoa(i),
				DueTime:   "10000s",
			})
			assert.NoError(t, err)
		}(i)
		go func(i int) {
			defer wg.Done()
			_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
				ActorType: "myactortype2",
				ActorId:   strconv.Itoa(i),
				Name:      strconv.Itoa(i),
				DueTime:   "10000s",
			})
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	assert.Len(t, d.db.ActorReminders(t, ctx, "myactortype").Reminders, 100)
	assert.Len(t, d.db.ActorReminders(t, ctx, "myactortype2").Reminders, 100)
	assert.Empty(t, d.scheduler.EtcdJobs(t, ctx))

	daprd1.Cleanup(t)

	daprd2.Run(t, ctx)
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd3.Run(t, ctx)
	t.Cleanup(func() { daprd3.Cleanup(t) })
	daprd4.Run(t, ctx)
	t.Cleanup(func() { daprd4.Cleanup(t) })
	daprd5.Run(t, ctx)
	t.Cleanup(func() { daprd5.Cleanup(t) })

	daprd2.WaitUntilRunning(t, ctx)
	daprd3.WaitUntilRunning(t, ctx)
	daprd4.WaitUntilRunning(t, ctx)
	daprd5.WaitUntilRunning(t, ctx)

	assert.Len(t, d.db.ActorReminders(t, ctx, "myactortype").Reminders, 100)
	assert.Len(t, d.db.ActorReminders(t, ctx, "myactortype2").Reminders, 100)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, d.scheduler.EtcdJobs(t, ctx), 100)
	}, time.Second*5, time.Millisecond*10)

	daprd6.Run(t, ctx)
	t.Cleanup(func() { daprd6.Cleanup(t) })
	daprd6.WaitUntilRunning(t, ctx)

	assert.Len(t, d.db.ActorReminders(t, ctx, "myactortype").Reminders, 100)
	assert.Len(t, d.db.ActorReminders(t, ctx, "myactortype2").Reminders, 100)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, d.scheduler.EtcdJobs(t, ctx), 200)
	}, time.Second*5, time.Millisecond*10)

	daprd2.Cleanup(t)
	daprd3.Cleanup(t)
	daprd4.Cleanup(t)
	daprd5.Cleanup(t)
	daprd6.Cleanup(t)
}
