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
	suite.Register(new(bulk))
}

// bulk ensures that in-flight bulk messages will continue to be processed when
// a SIGTERM is received by daprd.
type bulk struct {
	daprd         *daprd.Daprd
	appHealth     atomic.Bool
	inPublish     chan struct{}
	returnPublish chan struct{}
	recvRoute2    chan struct{}
}

func (b *bulk) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	b.appHealth.Store(true)
	b.inPublish = make(chan struct{})
	b.returnPublish = make(chan struct{})
	b.recvRoute2 = make(chan struct{})

	bulkResp := []byte(`{"statuses":[{"entryId":"1","status":"SUCCESS"}]}`)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/subscribe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `[
		{"pubsubname":"foo","topic":"abc","route":"route1"},
		{"pubsubname":"foo","topic":"def","route":"route2"}
]`)
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if b.appHealth.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	handler.HandleFunc("/route1", func(w http.ResponseWriter, r *http.Request) {
		close(b.inPublish)
		<-b.returnPublish
		w.Write(bulkResp)
	})
	handler.HandleFunc("/route2", func(w http.ResponseWriter, r *http.Request) {
		select {
		case b.recvRoute2 <- struct{}{}:
		case <-r.Context().Done():
		}
		w.Write(bulkResp)
	})
	app := prochttp.New(t, prochttp.WithHandler(handler))

	b.daprd = daprd.New(t,
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

func (b *bulk) Run(t *testing.T, ctx context.Context) {
	b.daprd.Run(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	assert.Len(t, b.daprd.RegistedComponents(t, ctx), 1)

	_, err := client.BulkPublishEventAlpha1(ctx, &rtv1.BulkPublishRequest{
		PubsubName: "foo",
		Topic:      "abc",
		Entries: []*rtv1.BulkPublishRequestEntry{
			{EntryId: "1", Event: []byte(`{"status":"completed"}`), ContentType: "application/json"},
		},
	})
	require.NoError(t, err)

	select {
	case <-b.inPublish:
	case <-time.After(time.Second * 5):
		assert.Fail(t, "did not receive publish event")
	}

	daprdStopped := make(chan struct{})
	go func() {
		b.daprd.Cleanup(t)
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
		_, err := client.BulkPublishEventAlpha1(ctx, &rtv1.BulkPublishRequest{
			PubsubName: "foo",
			Topic:      "def",
			Entries: []*rtv1.BulkPublishRequestEntry{
				{EntryId: "1", Event: []byte(`{"status":"completed"}`), ContentType: "application/json"},
			},
		})
		require.NoError(t, err)
		select {
		case <-b.recvRoute2:
		case <-time.After(time.Second / 2):
			break LOOP
		}
	}

	close(b.returnPublish)

	egressMetric := fmt.Sprintf("dapr_component_pubsub_egress_bulk_count|app_id:%s|component:foo|namespace:|success:true|topic:abc", b.daprd.AppID())
	ingressMetric := fmt.Sprintf("dapr_component_pubsub_ingress_count|app_id:%s|component:foo|namespace:|process_status:success|topic:abc", b.daprd.AppID())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := b.daprd.Metrics(t, ctx)
		assert.Equal(c, 1, int(metrics[egressMetric]))
		assert.Equal(c, 1, int(metrics[ingressMetric]))
	}, time.Second*5, time.Millisecond*10)

	b.appHealth.Store(false)
}
