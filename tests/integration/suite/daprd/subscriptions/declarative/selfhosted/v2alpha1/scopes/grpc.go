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
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	sub    *subscriber.Subscriber
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.sub = subscriber.New(t)

	resDir := t.TempDir()

	g.daprd1 = daprd.New(t,
		daprd.WithAppPort(g.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)
	g.daprd2 = daprd.New(t,
		daprd.WithAppPort(g.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
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
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: sub1
spec:
  pubsubname: mypub
  topic: all
  routes:
    default: /all
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: sub2
spec:
  pubsubname: mypub
  topic: allempty
  routes:
    default: /allempty
scopes: []
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: sub3
spec:
  pubsubname: mypub
  topic: only1
  routes:
    default: /only1
scopes:
- %[1]s
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: sub4
spec:
  pubsubname: mypub
  topic: only2
  routes:
    default: /only2
scopes:
- %[2]s
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: sub5
spec:
  pubsubname: mypub
  topic: both
  routes:
    default: /both
scopes:
- %[1]s
- %[2]s
`, g.daprd1.AppID(), g.daprd2.AppID())), 0o600))

	return []framework.Option{
		framework.WithProcesses(g.sub, g.daprd1, g.daprd2),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd1.WaitUntilRunning(t, ctx)
	g.daprd2.WaitUntilRunning(t, ctx)

	client1 := g.daprd1.GRPCClient(t, ctx)
	client2 := g.daprd2.GRPCClient(t, ctx)

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

	newReq := func(topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: "mypub", Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}

	reqAll := newReq("all")
	reqEmpty := newReq("allempty")
	reqOnly1 := newReq("only1")
	reqOnly2 := newReq("only2")
	reqBoth := newReq("both")

	g.sub.ExpectPublishReceive(t, ctx, g.daprd1, reqAll)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd1, reqEmpty)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd1, reqOnly1)
	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd1, reqOnly2)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd1, reqBoth)

	g.sub.ExpectPublishReceive(t, ctx, g.daprd2, reqAll)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd2, reqEmpty)
	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd2, reqOnly1)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd2, reqOnly2)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd2, reqBoth)

	g.sub.AssertEventChanLen(t, 0)
}
