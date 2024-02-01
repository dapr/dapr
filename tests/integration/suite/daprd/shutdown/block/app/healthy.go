/*
Copyright 2023 The Dapr Authors
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

package app

import (
	"context"
	"io"
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(healthy))
}

// health tests Daprd's --dapr-block-shutdown-seconds, ensuring shutdown will
// occur when the app becomes unhealthy.
type healthy struct {
	daprd         *daprd.Daprd
	logline       *logline.LogLine
	appHealth     atomic.Bool
	healthzCalled atomic.Int64
	routeCh       chan struct{}
}

func (h *healthy) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	h.appHealth.Store(true)
	h.routeCh = make(chan struct{}, 1)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/subscribe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `[{"pubsubname":"foo","topic":"topic","route":"route"}]`)
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		defer h.healthzCalled.Add(1)
		if h.appHealth.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	handler.HandleFunc("/route", func(w http.ResponseWriter, r *http.Request) {
		h.routeCh <- struct{}{}
	})
	app := prochttp.New(t,
		prochttp.WithHandler(handler),
	)

	h.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Blocking graceful shutdown for 3m0s or until app reports unhealthy...",
			"App reported unhealthy, entering shutdown...",
			"Daprd shutdown gracefully",
		),
	)

	h.daprd = daprd.New(t,
		daprd.WithDaprBlockShutdownDuration("180s"),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthCheckPath("/healthz"),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithExecOptions(exec.WithStdout(h.logline.Stdout())),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: foo
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, h.logline),
	}
}

func (h *healthy) Run(t *testing.T, ctx context.Context) {
	h.daprd.Run(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, h.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "foo",
		Topic:      "topic",
		Data:       []byte(`{"status":"completed"}`),
	})
	require.NoError(t, err)
	select {
	case <-h.routeCh:
	case <-ctx.Done():
		assert.Fail(t, "pubsub did not send message to subscriber")
	}

	daprdStopped := make(chan struct{})
	go func() {
		h.daprd.Cleanup(t)
		close(daprdStopped)
	}()

	healthzCalled := h.healthzCalled.Load()
	assert.Eventually(t, func() bool {
		return h.healthzCalled.Load() > healthzCalled
	}, time.Second*5, time.Millisecond*100)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "foo",
		Topic:      "topic",
		Data:       []byte(`{"status":"completed"}`),
	})
	require.NoError(t, err)
	select {
	case <-h.routeCh:
		assert.Fail(t, "pubsub should not have sent message to subscriber")
	case <-time.After(time.Second):
	}
	_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "mystore",
		States: []*commonv1.StateItem{
			{Key: "key", Value: []byte("value")},
		},
	})
	require.NoError(t, err)

	healthzCalled = h.healthzCalled.Load()
	h.appHealth.Store(false)
	require.Equal(t, healthzCalled, h.healthzCalled.Load())

	assert.Eventually(t, func() bool {
		return h.healthzCalled.Load() > healthzCalled
	}, time.Second*5, time.Millisecond*100)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "foo",
			Topic:      "topic",
			Data:       []byte(`{"status":"completed"}`),
		})
		//nolint:testifylint
		assert.Error(c, err)
	}, time.Second*5, time.Millisecond*100)
	_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "mystore",
		States: []*commonv1.StateItem{
			{Key: "key", Value: []byte("value2")},
		},
	})
	require.Error(t, err)

	select {
	case <-daprdStopped:
	case <-time.After(time.Second * 5):
		assert.Fail(t, "daprd did not exit in time")
	}
}
