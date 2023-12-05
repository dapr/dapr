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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(timeout))
}

// timeout tests Daprd's --dapr-block-shutdown-seconds, ensuring shutdown
// procedure will begin when seconds is reached when app still reports healthy.
type timeout struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	routeCh chan struct{}
}

func (i *timeout) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	i.routeCh = make(chan struct{}, 1)
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/subscribe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `[{"pubsubname":"foo","topic":"topic","route":"route"}]`)
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/route", func(w http.ResponseWriter, r *http.Request) {
		i.routeCh <- struct{}{}
	})
	app := prochttp.New(t,
		prochttp.WithHandler(handler),
	)

	i.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Blocking graceful shutdown for 2s or until app reports unhealthy...",
			"Block shutdown period expired, entering shutdown...",
			"Daprd shutdown gracefully",
		),
	)

	i.daprd = daprd.New(t,
		daprd.WithDaprBlockShutdownDuration("2s"),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthCheckPath("/healthz"),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithExecOptions(exec.WithStdout(i.logline.Stdout())),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: foo
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, i.logline),
	}
}

func (i *timeout) Run(t *testing.T, ctx context.Context) {
	i.daprd.Run(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, i.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
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
	case <-i.routeCh:
	case <-ctx.Done():
		assert.Fail(t, "pubsub message should have been sent to subscriber")
	}

	daprdStopped := make(chan struct{})
	go func() {
		i.daprd.Cleanup(t)
		close(daprdStopped)
	}()

	t.Run("daprd APIs should still be available during blocked shutdown", func(t *testing.T) {
		time.Sleep(time.Second)
		_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "foo",
			Topic:      "topic",
			Data:       []byte(`{"status":"completed"}`),
		})
		require.NoError(t, err)
		select {
		case <-i.routeCh:
		case <-ctx.Done():
			assert.Fail(t, "pubsub message should have been sent to subscriber")
		}
	})

	t.Run("daprd APIs are no longer available when past blocked shutdown", func(t *testing.T) {
		time.Sleep(time.Second * 3 / 2)
		_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "foo",
			Topic:      "topic",
			Data:       []byte(`{"status":"completed"}`),
		})
		require.Error(t, err)
	})

	select {
	case <-daprdStopped:
	case <-time.After(time.Second * 5):
		assert.Fail(t, "daprd did not exit in time")
	}
}
