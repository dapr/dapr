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
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reminder))
}

type reminder struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	daprd        *daprd.Daprd
	methodcalled atomic.Uint32

	loglineSchedulerReminders *logline.LogLine
}

func (r *reminder) Setup(t *testing.T) []framework.Option {
	r.loglineSchedulerReminders = logline.New(t, logline.WithStdoutLineContains(
		"Using Scheduler service for reminders.",
	))

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/xyz", func(http.ResponseWriter, *http.Request) {
		r.methodcalled.Add(1)
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
		daprd.WithAppProtocol("http"),
		daprd.WithLogLevel("debug"),
		daprd.WithExecOptions(
			exec.WithStdout(r.loglineSchedulerReminders.Stdout()),
		))

	return []framework.Option{
		framework.WithProcesses(r.loglineSchedulerReminders, r.scheduler, r.place, srv, r.daprd),
	}
}

func (r *reminder) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	daprdURL := "http://" + r.daprd.HTTPAddress() + "/v1.0/actors/myactortype/myactorid"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL+"/method/foo", nil)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(r *assert.CollectT) {
		resp, rErr := client.Do(req)
		if assert.NoError(r, rErr) {
			assert.NoError(r, resp.Body.Close())
			assert.Equal(r, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*10, time.Millisecond*10, "actor not ready in time")

	gclient := r.daprd.GRPCClient(t, ctx)

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   "1s",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return r.methodcalled.Load() == 1
	}, time.Second*5, time.Millisecond*10)

	// ensure we are using scheduler for reminders if preview feature is set
	r.loglineSchedulerReminders.EventuallyFoundAll(t)
}
