/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
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
	suite.Register(new(allowedtopics))
}

type allowedtopics struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd
	sub    *subscriber.Subscriber
}

func (a *allowedtopics) Setup(t *testing.T) []framework.Option {
	a.sub = subscriber.New(t)

	resDir := t.TempDir()

	a.daprd1 = daprd.New(t,
		daprd.WithAppPort(a.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)
	a.daprd2 = daprd.New(t,
		daprd.WithAppPort(a.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)
	a.daprd3 = daprd.New(t,
		daprd.WithAppPort(a.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)

	var subYaml string
	for i, sub := range []struct {
		pubsub string
		topic  string
	}{
		{"topic12", "topic0"},
		{"topic12", "topic1"},
		{"topic12", "topic2"},
		{"topic34-publishing", "topic3"},
		{"topic34-publishing", "topic4"},
		{"topic56-subscribing", "topic5"},
		{"topic56-subscribing", "topic6"},
		{"topic789-publishing-subscribing", "topic7"},
		{"topic789-publishing-subscribing", "topic8"},
		{"topic789-publishing-subscribing", "topic9"},
	} {
		subYaml += fmt.Sprintf(`
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: sub%d
spec:
 pubsubname: %s
 topic: %s
 route: /a
`, i+1, sub.pubsub, sub.topic)
	}
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "sub.yaml"), []byte(subYaml), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(resDir, "pubsub.yaml"),
		[]byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: topic12
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: allowedTopics
   value: "topic1,topic2"
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: topic34-publishing
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: allowedTopics
   value: "topic3,topic4"
 - name: publishingScopes
   value: "%[1]s=topic3;%[2]s=topic4"
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: topic56-subscribing
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: allowedTopics
   value: "topic5,topic6"
 - name: subscriptionScopes
   value: "%[1]s=topic5;%[2]s=topic6"
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: topic789-publishing-subscribing
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: allowedTopics
   value: "topic7,topic8,topic9"
 - name: subscriptionScopes
   value: "%[1]s=topic7,topic9;%[2]s=topic8,topic9"
 - name: publishingScopes
   value: "%[1]s=topic8,topic9;%[2]s=topic7,topic9"
`, a.daprd1.AppID(), a.daprd2.AppID())), 0o600))

	return []framework.Option{
		framework.WithProcesses(a.sub, a.daprd1, a.daprd2, a.daprd3),
	}
}

func (a *allowedtopics) Run(t *testing.T, ctx context.Context) {
	a.daprd1.WaitUntilRunning(t, ctx)
	a.daprd2.WaitUntilRunning(t, ctx)
	a.daprd3.WaitUntilRunning(t, ctx)

	for _, daprd := range []*daprd.Daprd{a.daprd1, a.daprd2, a.daprd3} {
		meta, err := daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Len(t, meta.GetRegisteredComponents(), 4)
		assert.Len(t, meta.GetSubscriptions(), 10)
	}

	newReq := func(pubsub, topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: pubsub, Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}

	req := newReq("topic12", "topic0")
	a.sub.ExpectPublishError(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishError(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishError(t, ctx, a.daprd3, req)
	req = newReq("topic12", "topic1")
	a.sub.ExpectPublishReceive(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)
	req = newReq("topic12", "topic2")
	a.sub.ExpectPublishReceive(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)
	req = newReq("topic12", "topic3")
	a.sub.ExpectPublishError(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishError(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishError(t, ctx, a.daprd3, req)

	req = newReq("topic34-publishing", "topic3")
	a.sub.ExpectPublishReceive(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishError(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)
	req = newReq("topic34-publishing", "topic4")
	a.sub.ExpectPublishError(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)

	req = newReq("topic56-subscribing", "topic5")
	a.sub.ExpectPublishReceive(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishNoReceive(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)
	req = newReq("topic56-subscribing", "topic6")
	a.sub.ExpectPublishNoReceive(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)

	req = newReq("topic789-publishing-subscribing", "topic7")
	a.sub.ExpectPublishError(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishNoReceive(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)
	req = newReq("topic789-publishing-subscribing", "topic8")
	a.sub.ExpectPublishNoReceive(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishError(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)
	req = newReq("topic789-publishing-subscribing", "topic9")
	a.sub.ExpectPublishReceive(t, ctx, a.daprd1, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd2, req)
	a.sub.ExpectPublishReceive(t, ctx, a.daprd3, req)
}
