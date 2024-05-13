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
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(repeats))
}

type repeats struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	daprd           *daprd.Daprd
	methodcalledPT  atomic.Uint32
	methodcalledRPT atomic.Uint32
	methodcalledDur atomic.Uint32
}

func (r *repeats) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/pt", func(http.ResponseWriter, *http.Request) {
		r.methodcalledPT.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/rpt", func(http.ResponseWriter, *http.Request) {
		r.methodcalledRPT.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/dur", func(http.ResponseWriter, *http.Request) {
		r.methodcalledDur.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {})

	r.scheduler = scheduler.New(t)
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	r.place = placement.New(t)
	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, r.place, srv, r.daprd),
	}
}

func (r *repeats) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	daprdURL := "http://" + r.daprd.HTTPAddress() + "/v1.0/actors/myactortype/myactorid"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL+"/method/foo", nil)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rErr := client.Do(req)
		//nolint:testifylint
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*10, time.Millisecond*10, "actor not ready in time")

	gclient := r.daprd.GRPCClient(t, ctx)
	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "pt",
		Data:      []byte("reminderdata"),
		Period:    "PT1S",
		Ttl:       "3s",
	})
	require.NoError(t, err)

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "rpt",
		Data:      []byte("reminderdata"),
		Period:    "R2/PT1S",
	})
	require.NoError(t, err)

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "dur",
		Data:      []byte("reminderdata"),
		Period:    "1s",
		Ttl:       "3s",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return r.methodcalledPT.Load() == 3 &&
			r.methodcalledDur.Load() == 3 &&
			r.methodcalledRPT.Load() == 2
	}, time.Second*5, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, uint32(3), r.methodcalledPT.Load())
	assert.Equal(t, uint32(2), r.methodcalledRPT.Load())
	assert.Equal(t, uint32(3), r.methodcalledDur.Load())
}
