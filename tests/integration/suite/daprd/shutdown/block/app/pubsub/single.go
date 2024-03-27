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

package pubsub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(single))
}

// single ensures that in-flight messages will continue to be processed when a
// SIGTERM is received by daprd.
type single struct {
	daprd         *daprd.Daprd
	appHealth     atomic.Bool
	inPublish     chan struct{}
	returnPublish chan struct{}
	recvRoute2    chan struct{}
}

func (s *single) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	s.appHealth.Store(true)
	s.inPublish = make(chan struct{})
	s.returnPublish = make(chan struct{})
	s.recvRoute2 = make(chan struct{})

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/subscribe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `[
		{"pubsubname":"foo","topic":"abc","route":"route1"},
		{"pubsubname":"foo","topic":"def","route":"route2"}
]`)
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if s.appHealth.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	handler.HandleFunc("/route1", func(w http.ResponseWriter, r *http.Request) {
		close(s.inPublish)
		<-s.returnPublish
	})
	handler.HandleFunc("/route2", func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.recvRoute2 <- struct{}{}:
		case <-r.Context().Done():
		}
	})
	app := prochttp.New(t, prochttp.WithHandler(handler))

	s.daprd = daprd.New(t,
		daprd.WithDaprBlockShutdownDuration("180s"),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthCheckPath("/healthz"),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
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
		framework.WithProcesses(app),
	}
}

func (s *single) Run(t *testing.T, ctx context.Context) {
	s.daprd.Run(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	assert.Len(t, s.daprd.RegistedComponents(t, ctx), 1)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "foo",
		Topic:      "abc",
		Data:       []byte(`{"status":"completed"}`),
	})
	require.NoError(t, err)

	select {
	case <-s.inPublish:
	case <-time.After(time.Second * 5):
		assert.Fail(t, "did not receive publish event")
	}

	daprdStopped := make(chan struct{})
	go func() {
		s.daprd.Cleanup(t)
		close(daprdStopped)
	}()
	t.Cleanup(func() {
		select {
		case <-daprdStopped:
		case <-time.After(time.Second * 5):
			assert.Fail(t, "daprd did not exit in time")
		}
	})

LOOP:
	for {
		_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "foo",
			Topic:      "def",
			Data:       []byte(`{"status":"completed"}`),
		})
		require.NoError(t, err)
		select {
		case <-s.recvRoute2:
		case <-time.After(time.Second / 2):
			break LOOP
		}
	}

	close(s.returnPublish)

	egressMetric := fmt.Sprintf("dapr_component_pubsub_egress_count|app_id:%s|component:foo|namespace:|success:true|topic:abc", s.daprd.AppID())
	ingressMetric := fmt.Sprintf("dapr_component_pubsub_ingress_count|app_id:%s|component:foo|namespace:|process_status:success|topic:abc", s.daprd.AppID())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := s.daprd.Metrics(t, ctx)
		assert.Equal(c, 1, int(metrics[egressMetric]))
		assert.Equal(c, 1, int(metrics[ingressMetric]))
	}, time.Second*5, time.Millisecond*10)

	s.appHealth.Store(false)
}
