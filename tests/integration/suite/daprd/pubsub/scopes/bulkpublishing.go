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
	suite.Register(new(bulkpublishing))
}

type bulkpublishing struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (b *bulkpublishing) Setup(t *testing.T) []framework.Option {
	b.sub = subscriber.New(t)

	resDir := t.TempDir()

	b.daprd = daprd.New(t,
		daprd.WithAppID("app1"),
		daprd.WithNamespace("ns1"),
		daprd.WithAppPort(b.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)

	require.NoError(t, os.WriteFile(filepath.Join(resDir, "pubsub.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: namespacedbulk
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: consumerID
   value: "{namespace}"
`), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(resDir, "subscription.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: namespacedbulk-sub
spec:
 pubsubname: namespacedbulk
 topic: topic0
 route: /a
 bulkSubscribe:
  enabled: true
`), 0o600))

	return []framework.Option{framework.WithProcesses(b.sub, b.daprd)}
}

func (b *bulkpublishing) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	meta, err := b.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Len(t, meta.GetRegisteredComponents(), 1)
	assert.Len(t, meta.GetSubscriptions(), 1)

	resp, err := b.daprd.GRPCClient(t, ctx).BulkPublishEvent(ctx, &rtv1.BulkPublishRequest{
		PubsubName: "namespacedbulk",
		Topic:      "topic0",
		Entries: []*rtv1.BulkPublishRequestEntry{{
			EntryId:     "1",
			Event:       []byte(`{"id":1}`),
			ContentType: "application/json",
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetFailedEntries())

	in := b.sub.ReceiveBulk(t, ctx)
	require.NotNil(t, in)
	assert.Len(t, in.GetEntries(), 1)
	b.sub.AssertBulkEventChanLen(t, 0)
}
