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
	"path"
	"strconv"
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
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(durable))
}

type durable struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	triggered slice.Slice[string]

	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
}

func (d *durable) Setup(t *testing.T) []framework.Option {
	d.triggered = slice.String()

	app := app.New(t,
		app.WithHandlerFunc("/", func(_ http.ResponseWriter, r *http.Request) {}),
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(_ http.ResponseWriter, r *http.Request) {
		}),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/remind/", func(_ http.ResponseWriter, r *http.Request) {
			d.triggered.Append(path.Base(r.URL.Path))
		}),
		app.WithConfig(`{"entities": ["myactortype"]}`),
	)

	d.scheduler = scheduler.New(t)
	d.place = placement.New(t)

	opts := []daprd.Option{
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
	}

	d.daprd1 = daprd.New(t, opts...)
	d.daprd2 = daprd.New(t, opts...)

	return []framework.Option{
		framework.WithProcesses(app, d.scheduler, d.place),
	}
}

func (d *durable) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)
	d.scheduler.WaitUntilRunning(t, ctx)

	d.daprd1.Run(t, ctx)
	t.Cleanup(func() { d.daprd1.Cleanup(t) })
	d.daprd1.WaitUntilRunning(t, ctx)

	sec3 := time.Now().Add(time.Second * 2).Format(time.RFC3339)

	client := d.daprd1.GRPCClient(t, ctx)
	for i := range 20 {
		_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Name:      strconv.Itoa(i),
			DueTime:   sec3,
			Data:      []byte("hello"),
			Period:    "R2/PT3S",
		})
		require.NoError(t, err)
	}

	exp := []string{
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"10", "11", "12", "13", "14", "15", "16", "17", "18", "19",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, exp, d.triggered.Slice())
	}, time.Second*20, time.Millisecond*10)

	d.daprd1.Cleanup(t)

	d.daprd2.Run(t, ctx)
	t.Cleanup(func() { d.daprd2.Cleanup(t) })
	d.daprd2.WaitUntilRunning(t, ctx)

	exp = []string{
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"10", "11", "12", "13", "14", "15", "16", "17", "18", "19",
		"10", "11", "12", "13", "14", "15", "16", "17", "18", "19",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, exp, d.triggered.Slice())
	}, time.Second*20, time.Millisecond*10)
}
