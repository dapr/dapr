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
	"os"
	"path/filepath"
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
	suite.Register(new(crud))
}

type crud struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	daprd        *daprd.Daprd
	methodcalled atomic.Uint32
}

func (c *crud) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true`), 0o600))

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/xyz", func(_ http.ResponseWriter, r *http.Request) {
		c.methodcalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {})

	c.scheduler = scheduler.New(t)
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	c.place = placement.New(t)
	c.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithSchedulerAddresses(c.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(c.scheduler, c.place, srv, c.daprd),
	}
}

func (c *crud) Run(t *testing.T, ctx context.Context) {
	c.scheduler.WaitUntilRunning(t, ctx)
	c.place.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	//nolint:goconst
	daprdURL := "http://" + c.daprd.HTTPAddress() + "/v1.0/actors/myactortype/myactorid"
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

	gclient := c.daprd.GRPCClient(t, ctx)
	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   "1h",
	})
	require.NoError(t, err)

	time.Sleep(time.Second * 2)
	require.Equal(t, 0, int(c.methodcalled.Load()))

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   "1s",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return c.methodcalled.Load() == 1
	}, time.Second*5, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	require.Equal(t, 1, int(c.methodcalled.Load()))

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   time.Now().Format(time.RFC3339),
		Period:    "PT1S",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return c.methodcalled.Load() == 4
	}, time.Second*5, time.Millisecond*10)

	_, err = gclient.UnregisterActorReminder(ctx, &rtv1.UnregisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
	})
	require.NoError(t, err)

	time.Sleep(time.Second * 2)

	last := c.methodcalled.Load()
	assert.Eventually(t, func() bool {
		called := c.methodcalled.Load()
		defer func() { last = called }()
		return called == last
	}, time.Second*15, time.Second*2)

	time.Sleep(time.Second)
	assert.Equal(t, last, c.methodcalled.Load())

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "xyz",
		DueTime:   time.Now().Format(time.RFC3339),
	})
	require.NoError(t, err)

	last += 1
	assert.Eventually(t, func() bool {
		return c.methodcalled.Load() == last
	}, time.Second*5, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, last, c.methodcalled.Load())
}
