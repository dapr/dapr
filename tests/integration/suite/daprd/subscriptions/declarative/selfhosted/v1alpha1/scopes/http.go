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

package scopes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	sub    *subscriber.Subscriber
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t, subscriber.WithRoutes(
		"/all", "/allempty", "/only1", "/only2", "/both",
	))

	resDir := t.TempDir()

	h.daprd1 = daprd.New(t,
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourcesDir(resDir),
	)
	h.daprd2 = daprd.New(t,
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourcesDir(resDir),
	)

	require.NoError(t, os.WriteFile(filepath.Join(resDir, "sub.yaml"),
		[]byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub1
spec:
  pubsubname: mypub
  topic: all
  route: /all
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub2
spec:
  pubsubname: mypub
  topic: allempty
  route: /allempty
scopes: []
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub3
spec:
  pubsubname: mypub
  topic: only1
  route: /only1
scopes:
- %[1]s
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub4
spec:
  pubsubname: mypub
  topic: only2
  route: /only2
scopes:
- %[2]s
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub5
spec:
  pubsubname: mypub
  topic: both
  route: /both
scopes:
- %[1]s
- %[2]s
`, h.daprd1.AppID(), h.daprd2.AppID())), 0o600))

	return []framework.Option{
		framework.WithProcesses(h.sub, h.daprd1, h.daprd2),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd1.WaitUntilRunning(t, ctx)
	h.daprd2.WaitUntilRunning(t, ctx)

	client1 := h.daprd1.GRPCClient(t, ctx)
	client2 := h.daprd2.GRPCClient(t, ctx)

	meta, err := client1.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, []*rtv1.PubsubSubscription{
		{PubsubName: "mypub", Topic: "all", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/all"}},
		}},
		{PubsubName: "mypub", Topic: "allempty", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/allempty"}},
		}},
		{PubsubName: "mypub", Topic: "only1", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/only1"}},
		}},
		{PubsubName: "mypub", Topic: "both", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/both"}},
		}},
	}, meta.GetSubscriptions())

	meta, err = client2.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, []*rtv1.PubsubSubscription{
		{PubsubName: "mypub", Topic: "all", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/all"}},
		}},
		{PubsubName: "mypub", Topic: "allempty", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/allempty"}},
		}},
		{PubsubName: "mypub", Topic: "only2", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/only2"}},
		}},
		{PubsubName: "mypub", Topic: "both", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/both"}},
		}},
	}, meta.GetSubscriptions())

	newReq := func(daprd *daprd.Daprd, topic string) subscriber.PublishRequest {
		return subscriber.PublishRequest{
			Daprd:      daprd,
			PubSubName: "mypub",
			Topic:      topic,
			Data:       `{"status": "completed"}`,
		}
	}

	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd1, "all"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd1, "allempty"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd1, "only1"))
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd1, "only2"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd1, "both"))

	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd2, "all"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd2, "allempty"))
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd2, "only1"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd2, "only2"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd2, "both"))

	h.sub.AssertEventChanLen(t, 0)
}
