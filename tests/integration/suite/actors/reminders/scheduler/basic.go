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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler

	methodcalled atomic.Int64
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(w http.ResponseWriter, r *http.Request) {
		b.methodcalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})

	b.scheduler = procscheduler.New(t)
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	b.place = placement.New(t)
	b.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithSchedulerAddresses(b.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(b.scheduler, b.place, srv, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)
	b.place.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	aurl := b.daprd.ActorInvokeURL("myactortype", "myactorid", "foo")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aurl, nil)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rErr := client.Do(req)
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*10, time.Millisecond*10, "actor not ready in time")

	body := `{"dueTime": "1s", "data": "reminderdata"}`
	aurl = b.daprd.ActorReminderURL("myactortype", "myactorid", "remindermethod")
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, aurl, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	assert.Eventually(t, func() bool {
		return b.methodcalled.Load() == 1
	}, time.Second*3, time.Millisecond*10)

	gclient := b.daprd.GRPCClient(t, ctx)
	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "1s",
		Data:      []byte("reminderdata"),
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return b.methodcalled.Load() == 2
	}, time.Second*10, time.Millisecond*10)
}
