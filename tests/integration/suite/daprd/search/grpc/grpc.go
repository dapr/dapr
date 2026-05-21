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
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

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
		procdaprd.WithExit1(),
		procdaprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: %s
spec:
  type: search.meilisearch
  version: v1
  metadata:
    - name: host
      value: http://%s
    - name: apiKey
      value: %s
`, storeName, s.meili.Host(), s.meili.APIKey())),
	)

	return []framework.Option{
		framework.WithProcesses(s.meili, s.daprd),
	}
}

func (s *search) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, s.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := runtimev1pb.NewDaprClient(conn)

	t.Run("lifecycle", func(t *testing.T) {
		index := indexName(t, "lifecycle")
		requireCreateIndex(t, ctx, client, index)
		t.Cleanup(func() {
			_, _ = client.DropIndexAlpha1(context.Background(), &runtimev1pb.DropIndexRequestAlpha1{StoreName: storeName, Index: index})
		})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.ListIndexesAlpha1(ctx, &runtimev1pb.ListIndexesRequestAlpha1{StoreName: storeName})
			assert.NoError(c, err)
			assert.Contains(c, resp.GetIndexes(), index)
		}, 10*time.Second, 100*time.Millisecond)

		desc, err := client.DescribeIndexAlpha1(ctx, &runtimev1pb.DescribeIndexRequestAlpha1{StoreName: storeName, Index: index})
		require.NoError(t, err)
		assert.Equal(t, index, desc.GetIndex())

		_, err = client.DropIndexAlpha1(ctx, &runtimev1pb.DropIndexRequestAlpha1{StoreName: storeName, Index: index})
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.ListIndexesAlpha1(ctx, &runtimev1pb.ListIndexesRequestAlpha1{StoreName: storeName})
			assert.NoError(c, err)
			assert.NotContains(c, resp.GetIndexes(), index)
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("documents", func(t *testing.T) {
		index := indexName(t, "documents")
		requireCreateIndex(t, ctx, client, index)
		t.Cleanup(func() {
			_, _ = client.DropIndexAlpha1(context.Background(), &runtimev1pb.DropIndexRequestAlpha1{StoreName: storeName, Index: index})
		})

		stream, err := client.IndexDocumentsAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: storeName,
			Index:     index,
			Ack:       runtimev1pb.IndexAck_INDEX_ACK_DURABLE,
			Documents: documents(t),
		}))
		idxResp, err := stream.CloseAndRecv()
		require.NoError(t, err)
		require.Len(t, idxResp.GetResults(), 3)
		for _, result := range idxResp.GetResults() {
			assert.True(t, result.GetSuccess(), result.GetErrorMessage())
		}

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := searchText(t, ctx, client, index, "alpha")
			assert.GreaterOrEqual(c, len(resp.GetHits()), 2)
			assert.Contains(c, hitIDs(resp.GetHits()), "doc-1")
			assert.Contains(c, hitIDs(resp.GetHits()), "doc-3")
		}, 10*time.Second, 100*time.Millisecond)

		delResp, err := client.DeleteDocumentsAlpha1(ctx, &runtimev1pb.DeleteDocumentsRequestAlpha1{
			StoreName: storeName,
			Index:     index,
			Ids:       []string{"doc-1", "doc-2", "doc-3"},
			Ack:       runtimev1pb.IndexAck_INDEX_ACK_DURABLE,
		})
		require.NoError(t, err)
		require.Len(t, delResp.GetResults(), 3)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := searchText(t, ctx, client, index, "alpha")
			assert.Empty(c, resp.GetHits())
		}, 10*time.Second, 100*time.Millisecond)

		_, err = client.DropIndexAlpha1(ctx, &runtimev1pb.DropIndexRequestAlpha1{StoreName: storeName, Index: index})
		require.NoError(t, err)
	})

	t.Run("error missing store", func(t *testing.T) {
		_, err := client.ListIndexesAlpha1(ctx, &runtimev1pb.ListIndexesRequestAlpha1{StoreName: "does-not-exist"})
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("error empty first stream message", func(t *testing.T) {
		stream, err := client.IndexDocumentsAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&runtimev1pb.IndexDocumentsRequestAlpha1{}))
		_, err = stream.CloseAndRecv()
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, err.Error(), "store_name")

		searchStream, err := client.SearchAlpha1(ctx, &runtimev1pb.SearchRequestAlpha1{})
		require.NoError(t, err)
		_, err = searchStream.Recv()
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func requireCreateIndex(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient, index string) {
	t.Helper()
	_, err := client.CreateIndexAlpha1(ctx, &runtimev1pb.CreateIndexRequestAlpha1{StoreName: storeName, Index: index, Fields: fields()})
	require.NoError(t, err)
}

func searchText(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient, index string, text string) *runtimev1pb.SearchResponseAlpha1 {
	t.Helper()
	stream, err := client.SearchAlpha1(ctx, &runtimev1pb.SearchRequestAlpha1{
		StoreName:      storeName,
		Index:          index,
		Query:          &runtimev1pb.SearchRequestAlpha1_Text{Text: text},
		TopK:           10,
		IncludeContent: true,
	})
	require.NoError(t, err)
	resp, err := stream.Recv()
	require.NoError(t, err)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	return resp
}

func fields() []*runtimev1pb.IndexFieldSchema {
	return []*runtimev1pb.IndexFieldSchema{
		{Name: "title", Type: runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_TEXT, Searchable: true, Sortable: true},
		{Name: "body", Type: runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_TEXT, Searchable: true},
		{Name: "category", Type: runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_KEYWORD, Filterable: true, Searchable: true},
		{Name: "price", Type: runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_DOUBLE, Filterable: true, Sortable: true},
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
	st, err := structpb.NewStruct(content)
	require.NoError(t, err)
	return &runtimev1pb.SearchDocument{Id: id, Content: st}
}

func hitIDs(hits []*runtimev1pb.SearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.GetDocument().GetId())
	}
	return ids
}

func indexName(t *testing.T, suffix string) string {
	return fmt.Sprintf("it-search-grpc-%s-%d", suffix, time.Now().UnixNano())
}
