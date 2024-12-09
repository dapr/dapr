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
	"io"
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
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(data))
}

type data struct {
	db        *sqlite.SQLite
	app       *app.App
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	called atomic.Bool
	got    atomic.Value
}

func (d *data) Setup(t *testing.T) []framework.Option {
	d.db = sqlite.New(t, sqlite.WithActorStateStore(true))
	d.app = app.New(t,
		app.WithConfig(`{"entities": ["myactortype"]}`),
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(_ http.ResponseWriter, r *http.Request) {
		}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/remind/myreminder", func(_ http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			d.got.Store(b)
			assert.True(t, d.called.CompareAndSwap(false, true))
		}),
	)
	d.scheduler = scheduler.New(t)
	d.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(d.db, d.scheduler, d.place, d.app),
	}
}

func (d *data) Run(t *testing.T, ctx context.Context) {
	opts := []daprd.Option{
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
		daprd.WithAppPort(d.app.Port()),
	}

	daprd1 := daprd.New(t, append(opts,
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: false
`))...)
	daprd2 := daprd.New(t, opts...)

	t.Cleanup(func() { daprd1.Cleanup(t) })
	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)

	_, err := daprd1.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "myreminder",
		DueTime:   "3s",
		Data:      []byte("mydata"),
	})
	require.NoError(t, err)
	assert.Len(t, d.db.ActorReminders(t, ctx, "myactortype").Reminders, 1)
	assert.Empty(t, d.scheduler.EtcdJobs(t, ctx))
	daprd1.Cleanup(t)

	assert.False(t, d.called.Load())

	daprd2.Run(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)
	assert.Len(t, d.db.ActorReminders(t, ctx, "myactortype").Reminders, 1)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, d.called.Load())
	}, time.Second*10, time.Millisecond*10)

	got, ok := d.got.Load().([]byte)
	assert.True(t, ok)
	assert.JSONEq(t, `{"data":"bXlkYXRh","dueTime":"","period":""}`, string(got))

	daprd2.Cleanup(t)
}
