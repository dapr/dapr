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
	suite.Register(new(http))
}

type http struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber

	resDir1 string
	resDir2 string
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t,
		subscriber.WithRoutes("/a", "/b", "/c", "/d", "/e", "/f"),
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

	h.resDir1, h.resDir2 = t.TempDir(), t.TempDir()

	for i, dir := range []string{h.resDir1, h.resDir2} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pubsub.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'pubsub%d'
spec:
 type: pubsub.in-memory
 version: v1
`, i)), 0o600))
	}

	h.daprd = daprd.New(t,
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(h.resDir1, h.resDir2),
	)

	return []framework.Option{
		framework.WithProcesses(h.sub, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, h.daprd.GetMetaRegistedComponents(t, ctx), 2)
	assert.Empty(t, h.daprd.GetMetaSubscriptions(t, ctx))

	newReq := func(daprd *daprd.Daprd, pubsubName, topic string) subscriber.PublishRequest {
		return subscriber.PublishRequest{
			Daprd:      daprd,
			PubSubName: pubsubName,
			Topic:      topic,
			Data:       `{"status": "completed"}`,
		}
	}
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub0", "a"))

	require.NoError(t, os.WriteFile(filepath.Join(h.resDir1, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub1'
spec:
 pubsubname: 'pubsub0'
 topic: 'a'
 routes:
  default: '/a'
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "a"))

	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))
	require.NoError(t, os.WriteFile(filepath.Join(h.resDir2, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub2'
spec:
 pubsubname: 'pubsub0'
 topic: 'b'
 routes:
  default: '/b'
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 2)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))

	require.NoError(t, os.Remove(filepath.Join(h.resDir1, "sub.yaml")))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub0", "a"))

	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub1", "c"))
	require.NoError(t, os.WriteFile(filepath.Join(h.resDir2, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub2'
spec:
 pubsubname: 'pubsub0'
 topic: 'b'
 routes:
  default: '/b'
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub3'
spec:
 pubsubname: 'pubsub1'
 topic: c
 routes:
  default: '/c'
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 2)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "c"))

	require.NoError(t, os.WriteFile(filepath.Join(h.resDir2, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub2'
spec:
 pubsubname: 'pubsub0'
 topic: 'd'
 routes:
  default: '/d'
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub3'
spec:
 pubsubname: 'pubsub1'
 topic: c
 routes:
  default: '/c'
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := h.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		if assert.Len(c, resp.GetSubscriptions(), 2) {
			assert.Equal(c, "c", resp.GetSubscriptions()[0].GetTopic())
			assert.Equal(c, "d", resp.GetSubscriptions()[1].GetTopic())
		}
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "d"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "c"))

	require.NoError(t, os.WriteFile(filepath.Join(h.resDir2, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub2'
spec:
 pubsubname: 'pubsub0'
 topic: 'd'
 routes:
  default: '/d'
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub3'
spec:
 pubsubname: 'pubsub1'
 topic: c
 routes:
  default: '/c'
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: 'sub4'
spec:
 pubsubname: 'pubsub1'
 topic: e
 route: '/e'
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 3)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "d"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "c"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "e"))

	require.NoError(t, os.WriteFile(filepath.Join(h.resDir2, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub2'
spec:
 pubsubname: 'pubsub0'
 topic: 'd'
 routes:
  default: '/d'
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub3'
spec:
 pubsubname: 'pubsub1'
 topic: c
 routes:
  default: '/c'
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: 'sub4'
spec:
 pubsubname: 'pubsub1'
 topic: f
 route: '/f'
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := h.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		if assert.Len(c, resp.GetSubscriptions(), 3) {
			assert.Equal(c, "f", resp.GetSubscriptions()[2].GetTopic())
		}
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "d"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "c"))
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub1", "e"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "f"))

	require.NoError(t, os.Remove(filepath.Join(h.resDir2, "pubsub.yaml")))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaRegistedComponents(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "d"))
	h.sub.ExpectPublishError(t, ctx, newReq(h.daprd, "pubsub1", "c"))
	h.sub.ExpectPublishError(t, ctx, newReq(h.daprd, "pubsub1", "f"))
}
