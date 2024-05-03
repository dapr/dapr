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

package subscriptions

import (
	"context"
	"fmt"
	nethttp "net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(inflight))
}

// inflight ensures in-flight messages are not lost when subscriptions
// are hot-reloaded.
type inflight struct {
	daprd         *daprd.Daprd
	sub           *subscriber.Subscriber
	dir           string
	inPublish     chan struct{}
	returnPublish chan struct{}
}

func (i *inflight) Setup(t *testing.T) []framework.Option {
	i.inPublish = make(chan struct{})
	i.returnPublish = make(chan struct{})
	i.sub = subscriber.New(t,
		subscriber.WithHandlerFunc("/a", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			close(i.inPublish)
			<-i.returnPublish
		}),
	)

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	i.dir = t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(i.dir, "pubsub.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: pubsub
spec:
 type: pubsub.in-memory
 version: v1
`), 0o600))

	i.daprd = daprd.New(t,
		daprd.WithAppPort(i.sub.Port()),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(i.dir),
	)

	return []framework.Option{
		framework.WithProcesses(i.sub, i.daprd),
	}
}

func (i *inflight) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, i.daprd.GetMetaRegistedComponents(t, ctx), 1)
	assert.Empty(t, i.daprd.GetMetaSubscriptions(t, ctx))

	require.NoError(t, os.WriteFile(filepath.Join(i.dir, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: sub
spec:
 pubsubname: pubsub
 topic: a
 routes:
  default: /a
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, i.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)

	client := i.daprd.GRPCClient(t, ctx)
	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "pubsub",
		Topic:      "a",
		Data:       []byte(`{"status":"completed"}`),
	})
	require.NoError(t, err)

	select {
	case <-i.inPublish:
	case <-time.After(time.Second * 5):
		assert.Fail(t, "did not receive publish event")
	}

	require.NoError(t, os.Remove(filepath.Join(i.dir, "sub.yaml")))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, i.daprd.GetMetaSubscriptions(t, ctx))
	}, time.Second*5, time.Millisecond*10)

	egressMetric := fmt.Sprintf("dapr_component_pubsub_egress_count|app_id:%s|component:pubsub|namespace:|success:true|topic:a", i.daprd.AppID())
	ingressMetric := fmt.Sprintf("dapr_component_pubsub_ingress_count|app_id:%s|component:pubsub|namespace:|process_status:success|status:success|topic:a", i.daprd.AppID())
	metrics := i.daprd.Metrics(t, ctx)
	assert.Equal(t, 1, int(metrics[egressMetric]))
	assert.Equal(t, 0, int(metrics[ingressMetric]))

	close(i.returnPublish)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := i.daprd.Metrics(t, ctx)
		assert.Equal(c, 1, int(metrics[egressMetric]))
		assert.Equal(c, 1, int(metrics[ingressMetric]))
	}, time.Second*5, time.Millisecond*10)
}
