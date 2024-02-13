/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package timers

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
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(apprestart))
}

// apprestart tests that timers will continue to be triggered, even when the
// app restarts.
type apprestart struct {
	daprd *daprd.Daprd
	place *placement.Placement

	methodcalled atomic.Int64
	app          *prochttp.HTTP
	healthz      atomic.Bool
}

func (a *apprestart) Setup(t *testing.T) []framework.Option {
	a.healthz.Store(true)
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if a.healthz.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/timer/timermethod", func(w http.ResponseWriter, r *http.Request) {
		a.methodcalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})

	a.app = prochttp.New(t, prochttp.WithHandler(handler))
	a.place = placement.New(t)
	a.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithAppPort(a.app.Port()),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_EXPERIMENTAL_ACTOR_HEALTH_FAILURE_THRESHOLD", "1",
			"DAPR_EXPERIMENTAL_ACTOR_HEALTH_UNHEALTHY_INTERVAL", "100ms",
			"DAPR_EXPERIMENTAL_ACTOR_HEALTH_HEALTHY_INTERVAL", "100ms",
			"DAPR_EXPERIMENTAL_ACTOR_HEALTH_REQUEST_TIMEOUT", "100ms",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(a.place, a.app, a.daprd),
	}
}

func (a *apprestart) Run(t *testing.T, ctx context.Context) {
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	daprdURL := "http://" + a.daprd.HTTPAddress() + "/v1.0/actors/myactortype/myactorid/method/foo"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL, nil)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rErr := client.Do(req)
		//nolint:testifylint
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*10, time.Millisecond*10, "actor not ready in time")

	gclient := a.daprd.GRPCClient(t, ctx)
	_, err = gclient.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "timermethod",
		Period:    "1ms",
	})
	require.NoError(t, err)

	resp, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	require.Len(t, resp.GetActorRuntime().GetActiveActors(), 1)
	assert.Equal(t, int32(1), resp.GetActorRuntime().GetActiveActors()[0].GetCount())

	assert.Eventually(t, func() bool {
		return a.methodcalled.Load() > 2
	}, time.Second*3, time.Millisecond*10)

	a.healthz.Store(false)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(c, err)
		if assert.Len(c, resp.GetActorRuntime().GetActiveActors(), 1) {
			assert.Equal(c, int32(0), resp.GetActorRuntime().GetActiveActors()[0].GetCount())
		}
	}, time.Second*5, time.Millisecond*10)
	methodCalled := a.methodcalled.Load()
	time.Sleep(time.Millisecond * 200)
	assert.Equal(t, methodCalled, a.methodcalled.Load())
}
