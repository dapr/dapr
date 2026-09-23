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

package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/meilisearch"
	"github.com/dapr/dapr/tests/integration/suite"
)

const storeName = "mysearch"

func init() {
	suite.Register(new(search))
}

type search struct {
	meili *meilisearch.Meilisearch
	daprd *procdaprd.Daprd
}

func (s *search) Setup(t *testing.T) []framework.Option {
	s.meili = meilisearch.New(t)
	s.daprd = procdaprd.New(t,
		procdaprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: %s
spec:
  type: search.meilisearch
  version: v1
  metadata:
    - name: host
      value: %s
    - name: apiKey
      value: %s
`, storeName, s.meili.Address(), s.meili.APIKey())),
	)

	return []framework.Option{
		framework.WithProcesses(s.meili, s.daprd),
	}
}

func (s *search) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(s.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := runtimev1pb.NewDaprClient(conn)

	t.Run("index lifecycle", func(t *testing.T) {
		index := indexName("lifecycle")
		requireCreateIndex(t, ctx, client, index)
		t.Cleanup(func() { dropIndex(client, index) })

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.ListIndexesAlpha1(ctx, &runtimev1pb.ListIndexesRequestAlpha1{StoreName: storeName})
			assert.NoError(c, err)
			assert.Contains(c, resp.GetIndexes(), index)
		}, 10*time.Second, 100*time.Millisecond)

		got, err := client.GetIndexAlpha1(ctx, &runtimev1pb.GetIndexRequestAlpha1{StoreName: storeName, Index: index})
		require.NoError(t, err)
		assert.Equal(t, index, got.GetIndex())

		// DeleteIndexAlpha1 is unary and returns google.protobuf.Empty.
		_, err = client.DeleteIndexAlpha1(ctx, &runtimev1pb.DeleteIndexRequestAlpha1{StoreName: storeName, Index: index})
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.ListIndexesAlpha1(ctx, &runtimev1pb.ListIndexesRequestAlpha1{StoreName: storeName})
			assert.NoError(c, err)
			assert.NotContains(c, resp.GetIndexes(), index)
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("documents", func(t *testing.T) {
		index := indexName("documents")
		requireCreateIndex(t, ctx, client, index)
		t.Cleanup(func() { dropIndex(client, index) })

		idxResp, err := client.IndexDocumentsAlpha1(ctx, &runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: storeName,
			Index:     index,
			Documents: documents(t),
			Options: &runtimev1pb.IndexingOptionsAlpha1{
				Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
			},
		})
		require.NoError(t, err)
		assert.Empty(t, idxResp.GetFailedItems())
		// A successful write always reports a concrete acknowledgement boundary.
		assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, idxResp.GetAck())

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			searchResp, searchErr := client.SearchAlpha1(ctx, searchRequest(index, "alpha"))
			if !assert.NoError(c, searchErr) {
				return
			}
			assert.Len(c, searchResp.GetHits(), 2)
			assert.ElementsMatch(c, []string{"doc-1", "doc-3"}, hitIDs(searchResp.GetHits()))
		}, 10*time.Second, 100*time.Millisecond)

		resp, err := client.SearchAlpha1(ctx, searchRequest(index, "alpha"))
		require.NoError(t, err)
		require.Len(t, resp.GetHits(), 2)
		for _, hit := range resp.GetHits() {
			// include_content was requested, so the round-tripped JSON object
			// bytes must be readable back.
			var content map[string]any
			require.NoError(t, json.Unmarshal(hit.GetDocument().GetContent(), &content))
			assert.Contains(t, content, "title")
		}

		getResp, err := client.GetDocumentsAlpha1(ctx, &runtimev1pb.GetDocumentsRequestAlpha1{
			StoreName:      storeName,
			Index:          index,
			Ids:            []string{"doc-1", "doc-2"},
			IncludeContent: true,
		})
		require.NoError(t, err)
		require.Len(t, getResp.GetDocuments(), 2)
		assert.ElementsMatch(t, []string{"doc-1", "doc-2"}, documentIDs(getResp.GetDocuments()))
		for _, doc := range getResp.GetDocuments() {
			var content map[string]any
			require.NoError(t, json.Unmarshal(doc.GetContent(), &content))
			assert.Contains(t, content, "title")
		}

		// DeleteDocumentsAlpha1 is a write and shares the acknowledgement
		// model of IndexDocumentsAlpha1.
		delResp, err := client.DeleteDocumentsAlpha1(ctx, &runtimev1pb.DeleteDocumentsRequestAlpha1{
			StoreName: storeName,
			Index:     index,
			Ids:       []string{"doc-1", "doc-2", "doc-3"},
			Options: &runtimev1pb.IndexingOptionsAlpha1{
				Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
			},
		})
		require.NoError(t, err)
		assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, delResp.GetAck())

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			searchResp, searchErr := client.SearchAlpha1(ctx, searchRequest(index, "alpha"))
			if !assert.NoError(c, searchErr) {
				return
			}
			assert.Empty(c, searchResp.GetHits())
		}, 10*time.Second, 100*time.Millisecond)

		_, err = client.DeleteIndexAlpha1(ctx, &runtimev1pb.DeleteIndexRequestAlpha1{StoreName: storeName, Index: index})
		require.NoError(t, err)
	})

	t.Run("non-object content is reported per item", func(t *testing.T) {
		index := indexName("non-object")
		requireCreateIndex(t, ctx, client, index)
		t.Cleanup(func() { dropIndex(client, index) })

		// content must be a JSON object. A document that is not one is
		// rejected in failed_items while the rest of the batch still indexes.
		idxResp, err := client.IndexDocumentsAlpha1(ctx, &runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: storeName,
			Index:     index,
			Documents: []*runtimev1pb.SearchDocument{
				newDocument(t, "doc-1", map[string]any{"title": "alpha guide"}),
				{Id: "doc-2", Content: []byte(`["not", "an", "object"]`)},
			},
		})
		require.NoError(t, err)
		require.Len(t, idxResp.GetFailedItems(), 1)
		assert.Equal(t, "doc-2", idxResp.GetFailedItems()[0].GetId())
		assert.Equal(t, int32(codes.InvalidArgument), idxResp.GetFailedItems()[0].GetError().GetCode())
		assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, idxResp.GetAck())

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetDocumentsAlpha1(ctx, &runtimev1pb.GetDocumentsRequestAlpha1{
				StoreName: storeName,
				Index:     index,
				Ids:       []string{"doc-1", "doc-2"},
			})
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, []string{"doc-1"}, documentIDs(resp.GetDocuments()))
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("error missing store", func(t *testing.T) {
		_, err := client.ListIndexesAlpha1(ctx, &runtimev1pb.ListIndexesRequestAlpha1{StoreName: "does-not-exist"})
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("error index already exists", func(t *testing.T) {
		index := indexName("exists")
		requireCreateIndex(t, ctx, client, index)
		t.Cleanup(func() { dropIndex(client, index) })

		// Creating an existing index does not reconcile settings.
		_, err := client.CreateIndexAlpha1(ctx, &runtimev1pb.CreateIndexRequestAlpha1{StoreName: storeName, Index: index})
		require.Error(t, err)
		assert.Equal(t, codes.AlreadyExists, status.Code(err))
	})

	t.Run("error empty document id", func(t *testing.T) {
		_, err := client.IndexDocumentsAlpha1(ctx, &runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: storeName,
			Index:     indexName("empty-id"),
			Documents: []*runtimev1pb.SearchDocument{newDocument(t, "", map[string]any{"title": "alpha"})},
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("error duplicate document id", func(t *testing.T) {
		_, err := client.IndexDocumentsAlpha1(ctx, &runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: storeName,
			Index:     indexName("duplicate-id"),
			Documents: []*runtimev1pb.SearchDocument{
				newDocument(t, "doc-1", map[string]any{"title": "alpha"}),
				newDocument(t, "doc-1", map[string]any{"title": "beta"}),
			},
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("error wait options without wait mode", func(t *testing.T) {
		// wait_timeout and on_wait_timeout are only valid with
		// INDEXING_MODE_WAIT_FOR_COMPLETION.
		_, err := client.IndexDocumentsAlpha1(ctx, &runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: storeName,
			Index:     indexName("bad-options"),
			Documents: []*runtimev1pb.SearchDocument{newDocument(t, "doc-1", map[string]any{"title": "alpha"})},
			Options: &runtimev1pb.IndexingOptionsAlpha1{
				Mode:          runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
				WaitTimeout:   durationpb.New(time.Second),
				OnWaitTimeout: runtimev1pb.IndexingWaitTimeoutAction_INDEXING_WAIT_TIMEOUT_ACTION_FAIL_REQUEST,
			},
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("error search missing store", func(t *testing.T) {
		_, err := client.SearchAlpha1(ctx, &runtimev1pb.SearchRequestAlpha1{})
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func requireCreateIndex(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient, index string) {
	t.Helper()
	// Index settings are component specific and travel in metadata; the
	// Meilisearch defaults are enough for a full text query.
	_, err := client.CreateIndexAlpha1(ctx, &runtimev1pb.CreateIndexRequestAlpha1{StoreName: storeName, Index: index})
	require.NoError(t, err)
}

func dropIndex(client runtimev1pb.DaprClient, index string) {
	_, _ = client.DeleteIndexAlpha1(context.Background(), &runtimev1pb.DeleteIndexRequestAlpha1{StoreName: storeName, Index: index})
}

func searchRequest(index string, text string) *runtimev1pb.SearchRequestAlpha1 {
	return &runtimev1pb.SearchRequestAlpha1{
		StoreName:      storeName,
		Index:          index,
		Query:          &runtimev1pb.SearchRequestAlpha1_Text{Text: text},
		TopK:           10,
		IncludeContent: true,
	}
}

func documents(t *testing.T) []*runtimev1pb.SearchDocument {
	t.Helper()
	return []*runtimev1pb.SearchDocument{
		newDocument(t, "doc-1", map[string]any{"title": "alpha guide", "body": "first alpha document", "category": "guide", "price": 10.0}),
		newDocument(t, "doc-2", map[string]any{"title": "beta guide", "body": "second document", "category": "guide", "price": 25.0}),
		newDocument(t, "doc-3", map[string]any{"title": "alpha reference", "body": "reference material", "category": "reference", "price": 30.0}),
	}
}

func newDocument(t *testing.T, id string, content map[string]any) *runtimev1pb.SearchDocument {
	t.Helper()
	// SearchDocument.content is JSON object bytes.
	b, err := json.Marshal(content)
	require.NoError(t, err)
	return &runtimev1pb.SearchDocument{Id: id, Content: b}
}

func hitIDs(hits []*runtimev1pb.SearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.GetDocument().GetId())
	}
	return ids
}

func documentIDs(docs []*runtimev1pb.SearchDocument) []string {
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.GetId())
	}
	return ids
}

func indexName(suffix string) string {
	return fmt.Sprintf("it-search-grpc-%s-%d", suffix, time.Now().UnixNano())
}
