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
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hop))
}

type hop struct {
	daprd1    *daprd.Daprd
	daprd2    *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler

	daprd1called atomic.Uint64
	daprd2called atomic.Uint64
}

func (h *hop) Setup(t *testing.T) []framework.Option {
	newHTTP := func(called *atomic.Uint64) *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		handler.HandleFunc("/actors/myactortype/foo", func(http.ResponseWriter, *http.Request) {
		})

		for i := range 100 {
			handler.HandleFunc("/actors/myactortype/foo/method/remind/"+strconv.Itoa(i), func(http.ResponseWriter, *http.Request) {
				called.Add(1)
			})
		}

		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	h.scheduler = procscheduler.New(t)
	h.place = placement.New(t)

	srv1 := newHTTP(&h.daprd1called)
	srv2 := newHTTP(&h.daprd2called)
	h.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithSchedulerAddresses(h.scheduler.Address()),
		daprd.WithAppPort(srv1.Port()),
	)
	h.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithSchedulerAddresses(h.scheduler.Address()),
		daprd.WithAppPort(srv2.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, h.scheduler, h.place, h.daprd1, h.daprd2),
	}
}

func (h *hop) Run(t *testing.T, ctx context.Context) {
	h.scheduler.WaitUntilRunning(t, ctx)
	h.place.WaitUntilRunning(t, ctx)
	h.daprd1.WaitUntilRunning(t, ctx)
	h.daprd2.WaitUntilRunning(t, ctx)

	gclient := h.daprd1.GRPCClient(t, ctx)
	for i := range 100 {
		_, err := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "foo",
			Name:      strconv.Itoa(i),
			DueTime:   "0s",
			Data:      []byte("reminderdata"),
		})
		require.NoError(t, err, "failed to register reminder iteration"+strconv.Itoa(i))
	}

	assert.Eventually(t, func() bool {
		return h.daprd1called.Load() == 100 || h.daprd2called.Load() == 100
	}, time.Second*10, time.Millisecond*10)

	assert.True(t,
		(h.daprd1called.Load() == 100 && h.daprd2called.Load() == 0) ||
			(h.daprd2called.Load() == 100 && h.daprd1called.Load() == 0),
	)
}
