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

package selfhosted

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
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(subscriptions))
}

type subscriptions struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber

	resDir1 string
	resDir2 string
}

func (s *subscriptions) Setup(t *testing.T) []framework.Option {
	s.sub = subscriber.New(t)

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

	s.resDir1, s.resDir2 = t.TempDir(), t.TempDir()

	for i, dir := range []string{s.resDir1, s.resDir2} {
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

	s.daprd = daprd.New(t,
		daprd.WithAppPort(s.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(s.resDir1, s.resDir2),
	)

	return []framework.Option{
		framework.WithProcesses(s.sub, s.daprd),
	}
}

func (s *subscriptions) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)
	resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Len(t, resp.GetRegisteredComponents(), 2)
	assert.Empty(t, resp.GetSubscriptions())

	newReq := func(pubsub, topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: pubsub, Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "a"))

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "sub.yaml"), []byte(`
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
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		assert.Len(c, resp.GetSubscriptions(), 1)
	}, time.Second*5, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "a"))

	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))
	require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "sub.yaml"), []byte(`
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
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		assert.Len(c, resp.GetSubscriptions(), 2)
	}, time.Second*5, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))

	require.NoError(t, os.Remove(filepath.Join(s.resDir1, "sub.yaml")))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		assert.Len(c, resp.GetSubscriptions(), 1)
	}, time.Second*5, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "a"))

	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub1", "c"))
	require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "sub.yaml"), []byte(`
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
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		assert.Len(c, resp.GetSubscriptions(), 2)
	}, time.Second*5, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub1", "c"))

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "sub.yaml"), []byte(`
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
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		if assert.Len(c, resp.GetSubscriptions(), 2) {
			assert.Equal(c, "c", resp.GetSubscriptions()[0].GetTopic())
			assert.Equal(c, "d", resp.GetSubscriptions()[1].GetTopic())
		}
	}, time.Second*5, time.Millisecond*10)
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "d"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub1", "c"))

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "sub.yaml"), []byte(`
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
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		assert.Len(c, resp.GetSubscriptions(), 3)
	}, time.Second*5, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "d"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub1", "c"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub1", "e"))

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "sub.yaml"), []byte(`
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
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		if assert.Len(c, resp.GetSubscriptions(), 3) {
			assert.Equal(c, "f", resp.GetSubscriptions()[2].GetTopic())
		}
	}, time.Second*5, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "d"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub1", "c"))
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub1", "e"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub1", "f"))

	require.NoError(t, os.Remove(filepath.Join(s.resDir2, "pubsub.yaml")))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		assert.Len(c, resp.GetRegisteredComponents(), 1)
	}, time.Second*5, time.Millisecond*10)
	require.NoError(t, err)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "d"))
	s.sub.ExpectPublishError(t, ctx, s.daprd, newReq("pubsub1", "c"))
	s.sub.ExpectPublishError(t, ctx, s.daprd, newReq("pubsub1", "f"))
}
