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

package pluggable

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bulkmetadata))
}

// bulkmetadata verifies the metadata a component receives for each entry of
// a bulk publish, over both the gRPC and HTTP APIs. Request-level metadata
// must reach every entry, including entries with no metadata of their own,
// and entry-level keys must take precedence over request-level keys.
type bulkmetadata struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
}

func (b *bulkmetadata) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	b.broker = broker.New(t, broker.WithRecordPublishes())
	b.daprd = daprd.New(t, b.broker.DaprdOptions(t, "mypub")...)

	return []framework.Option{
		framework.WithProcesses(b.broker, b.daprd),
	}
}

func (b *bulkmetadata) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	// Each entry is published to the component on its own, carrying the
	// metadata daprd built for it. Entry "one" has no metadata of its own;
	// entry "two" overrides the request-level "foo".
	expected := map[string]map[string]string{
		"one": {"ttlInSeconds": "300", "foo": "request"},
		"two": {"ttlInSeconds": "300", "foo": "entry"},
	}

	assertPublishes := func(t *testing.T) {
		t.Helper()

		got := make(map[string]map[string]string, len(expected))
		for range len(expected) {
			req := b.broker.NextPublish(t, ctx)
			var ce struct {
				Data string `json:"data"`
			}
			require.NoError(t, json.Unmarshal(req.Data, &ce))
			got[ce.Data] = req.Metadata
		}
		assert.Equal(t, expected, got)
	}

	t.Run("grpc", func(t *testing.T) {
		resp, err := b.daprd.GRPCClient(t, ctx).BulkPublishEvent(ctx, &rtv1.BulkPublishRequest{
			PubsubName: "mypub",
			Topic:      "a",
			Metadata:   map[string]string{"ttlInSeconds": "300", "foo": "request"},
			Entries: []*rtv1.BulkPublishRequestEntry{
				{EntryId: "1", Event: []byte("one"), ContentType: "text/plain"},
				{EntryId: "2", Event: []byte("two"), ContentType: "text/plain", Metadata: map[string]string{"foo": "entry"}},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.GetFailedEntries())

		assertPublishes(t)
	})

	t.Run("http", func(t *testing.T) {
		b.daprd.HTTPPost(t, ctx,
			"v1.0/publish/bulk/mypub/a?metadata.ttlInSeconds=300&metadata.foo=request",
			strings.NewReader(`[
  {"entryId":"1","event":"one","contentType":"text/plain"},
  {"entryId":"2","event":"two","contentType":"text/plain","metadata":{"foo":"entry"}}
]`),
			nethttp.StatusNoContent,
			"Content-Type", "application/json",
		)

		assertPublishes(t)
	})
}
