/*
Copyright 2026 The Dapr Authors
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
	"strings"
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
	suite.Register(new(whitespace))
}

type whitespace struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	sub    *subscriber.Subscriber
}

func (w *whitespace) Setup(t *testing.T) []framework.Option {
	w.sub = subscriber.New(t)

	resDir := t.TempDir()

	w.daprd1 = daprd.New(t,
		daprd.WithAppPort(w.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)
	w.daprd2 = daprd.New(t,
		daprd.WithAppPort(w.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)

	var subYaml strings.Builder
	for i, sub := range []struct {
		pubsub string
		topic  string
	}{
		{"whitespace-allowed", "topic1"},
		{"whitespace-allowed", "topic2"},
		{"empty-allowed", "topic1"},
		{"empty-allowed", "topic2"},
		{"multiline-scopes", "topic1"},
		{"multiline-scopes", "topic2"},
	} {
		fmt.Fprintf(&subYaml, `
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
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "sub.yaml"), []byte(subYaml.String()), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(resDir, "pubsub.yaml"),
		fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: whitespace-allowed
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: allowedTopics
   value: " topic1, topic2, "
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: empty-allowed
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: allowedTopics
   value: "   "
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: multiline-scopes
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: allowedTopics
   value: "topic1,\n\ttopic2"
 - name: subscriptionScopes
   value: "%[1]s=topic1;\n  %[2]s=topic2"
 - name: publishingScopes
   value: "%[1]s=topic1, ;\n  %[2]s=topic2"
`, w.daprd1.AppID(), w.daprd2.AppID()), 0o600))

	return []framework.Option{
		framework.WithProcesses(w.sub, w.daprd1, w.daprd2),
	}
}

func (w *whitespace) Run(t *testing.T, ctx context.Context) {
	w.daprd1.WaitUntilRunning(t, ctx)
	w.daprd2.WaitUntilRunning(t, ctx)

	for _, daprd := range []*daprd.Daprd{w.daprd1, w.daprd2} {
		meta, err := daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Len(t, meta.GetRegisteredComponents(), 3)
		assert.Len(t, meta.GetSubscriptions(), 6)
	}

	newReq := func(pubsub, topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: pubsub, Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}

	// whitespace-allowed: topic1 and topic2 are allowed; topic3 is not. Trailing comma does not create empty topic.
	req := newReq("whitespace-allowed", "topic1")
	w.sub.ExpectPublishReceive(t, ctx, w.daprd1, req)
	w.sub.ExpectPublishReceive(t, ctx, w.daprd2, req)

	req = newReq("whitespace-allowed", "topic2")
	w.sub.ExpectPublishReceive(t, ctx, w.daprd1, req)
	w.sub.ExpectPublishReceive(t, ctx, w.daprd2, req)

	req = newReq("whitespace-allowed", "topic3")
	w.sub.ExpectPublishError(t, ctx, w.daprd1, req)
	w.sub.ExpectPublishError(t, ctx, w.daprd2, req)

	// empty-allowed: allowedTopics is whitespace-only, so no allowlist restriction applies (all topics allowed)
	req = newReq("empty-allowed", "topic1")
	w.sub.ExpectPublishReceive(t, ctx, w.daprd1, req)
	w.sub.ExpectPublishReceive(t, ctx, w.daprd2, req)

	req = newReq("empty-allowed", "topic2")
	w.sub.ExpectPublishReceive(t, ctx, w.daprd1, req)
	w.sub.ExpectPublishReceive(t, ctx, w.daprd2, req)

	// multiline-scopes:
	// daprd1 can publish to topic1 and is subscribed to topic1
	req = newReq("multiline-scopes", "topic1")
	w.sub.ExpectPublishReceive(t, ctx, w.daprd1, req)
	// daprd2 is not allowed to publish to topic1
	w.sub.ExpectPublishError(t, ctx, w.daprd2, req)

	// daprd2 can publish to topic2 and is subscribed to topic2
	req = newReq("multiline-scopes", "topic2")
	// daprd1 is not allowed to publish to topic2
	w.sub.ExpectPublishError(t, ctx, w.daprd1, req)
	w.sub.ExpectPublishReceive(t, ctx, w.daprd2, req)
}
