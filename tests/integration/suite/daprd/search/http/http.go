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

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
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

	httpClient := client.HTTP(t)
	baseURL := fmt.Sprintf("http://%s/v1.0-alpha1/search/%s", s.daprd.HTTPAddress(), storeName)

	t.Run("index lifecycle", func(t *testing.T) {
		index := indexName("lifecycle")
		createIndex(t, ctx, httpClient, baseURL, index)
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, indexURL(baseURL, index), nil, nil)
		})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.ListIndexesResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodGet, baseURL+"/indexes", nil, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.Contains(c, resp.GetIndexes(), index)
		}, 10*time.Second, 100*time.Millisecond)

		var got runtimev1pb.GetIndexResponseAlpha1
		status, body := do(t, ctx, httpClient, nethttp.MethodGet, indexURL(baseURL, index), nil, &got)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		assert.Equal(t, index, got.GetIndex())

		status, body = do(t, ctx, httpClient, nethttp.MethodDelete, indexURL(baseURL, index), nil, nil)
		require.Equal(t, nethttp.StatusOK, status, string(body))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.ListIndexesResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodGet, baseURL+"/indexes", nil, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.NotContains(c, resp.GetIndexes(), index)
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("documents", func(t *testing.T) {
		index := indexName("documents")
		createIndex(t, ctx, httpClient, baseURL, index)
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, indexURL(baseURL, index), nil, nil)
		})

		var idxResp runtimev1pb.IndexDocumentsResponseAlpha1
		status, body := do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index)+"/documents", &runtimev1pb.IndexDocumentsRequestAlpha1{
			Documents: documents(t),
			Options: &runtimev1pb.IndexingOptionsAlpha1{
				Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
			},
		}, &idxResp)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		assert.Empty(t, idxResp.GetFailedItems())
		assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, idxResp.GetAck())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.SearchResponseAlpha1
			searchStatus, _ := do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index)+"/query", searchRequest("alpha"), &resp)
			assert.Equal(c, nethttp.StatusOK, searchStatus)
			assert.ElementsMatch(c, []string{"doc-1", "doc-3"}, hitIDs(resp.GetHits()))
		}, 10*time.Second, 100*time.Millisecond)

		var getResp runtimev1pb.GetDocumentsResponseAlpha1
		status, body = do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index)+"/documents/get", &runtimev1pb.GetDocumentsRequestAlpha1{
			Ids:            []string{"doc-1", "doc-2"},
			IncludeContent: true,
		}, &getResp)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		require.Len(t, getResp.GetDocuments(), 2)
		assert.ElementsMatch(t, []string{"doc-1", "doc-2"}, documentIDs(getResp.GetDocuments()))
		for _, doc := range getResp.GetDocuments() {
			// content is JSON object bytes, base64 encoded over protojson.
			var content map[string]any
			require.NoError(t, json.Unmarshal(doc.GetContent(), &content))
			assert.Contains(t, content, "title")
		}

		// Deletes are writes and share the acknowledgement model of indexing.
		var delResp runtimev1pb.DeleteDocumentsResponseAlpha1
		status, body = do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index)+"/documents/delete", &runtimev1pb.DeleteDocumentsRequestAlpha1{
			Ids: []string{"doc-1", "doc-2", "doc-3"},
			Options: &runtimev1pb.IndexingOptionsAlpha1{
				Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
			},
		}, &delResp)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, delResp.GetAck())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.SearchResponseAlpha1
			searchStatus, _ := do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index)+"/query", searchRequest("alpha"), &resp)
			assert.Equal(c, nethttp.StatusOK, searchStatus)
			assert.Empty(c, resp.GetHits())
		}, 10*time.Second, 100*time.Millisecond)

		status, body = do(t, ctx, httpClient, nethttp.MethodDelete, indexURL(baseURL, index), nil, nil)
		require.Equal(t, nethttp.StatusOK, status, string(body))
	})

	t.Run("non-object content is reported per item", func(t *testing.T) {
		index := indexName("non-object")
		createIndex(t, ctx, httpClient, baseURL, index)
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, indexURL(baseURL, index), nil, nil)
		})

		// content must be a JSON object. A document that is not one is
		// rejected in failed_items while the rest of the batch still indexes.
		var idxResp runtimev1pb.IndexDocumentsResponseAlpha1
		status, body := do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index)+"/documents", &runtimev1pb.IndexDocumentsRequestAlpha1{
			Documents: []*runtimev1pb.SearchDocument{
				newDocument(t, "doc-1", map[string]any{"title": "alpha guide"}),
				{Id: "doc-2", Content: []byte(`["not", "an", "object"]`)},
			},
		}, &idxResp)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		require.Len(t, idxResp.GetFailedItems(), 1)
		assert.Equal(t, "doc-2", idxResp.GetFailedItems()[0].GetId())
		assert.Equal(t, int32(codes.InvalidArgument), idxResp.GetFailedItems()[0].GetError().GetCode())
		assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, idxResp.GetAck())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.GetDocumentsResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index)+"/documents/get", &runtimev1pb.GetDocumentsRequestAlpha1{
				Ids: []string{"doc-1", "doc-2"},
			}, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.Equal(c, []string{"doc-1"}, documentIDs(resp.GetDocuments()))
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("error missing store", func(t *testing.T) {
		url := fmt.Sprintf("http://%s/v1.0-alpha1/search/does-not-exist/indexes", s.daprd.HTTPAddress())
		status, body := do(t, ctx, httpClient, nethttp.MethodGet, url, nil, nil)
		assert.Equal(t, nethttp.StatusNotFound, status)
		assert.Contains(t, string(body), "ERR_SEARCH_STORE_NOT_FOUND")
	})

	t.Run("error index already exists", func(t *testing.T) {
		index := indexName("exists")
		createIndex(t, ctx, httpClient, baseURL, index)
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, indexURL(baseURL, index), nil, nil)
		})

		// Creating an existing index does not reconcile settings.
		status, _ := do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index), &runtimev1pb.CreateIndexRequestAlpha1{}, nil)
		assert.Equal(t, nethttp.StatusConflict, status)
	})

	t.Run("error malformed request", func(t *testing.T) {
		status, body := doRaw(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, indexName("bad-json")), []byte("{"), nil)
		assert.Equal(t, nethttp.StatusBadRequest, status)
		assert.Contains(t, string(body), "ERR_MALFORMED_REQUEST")
	})

	t.Run("error duplicate document id", func(t *testing.T) {
		status, _ := do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, indexName("duplicate-id"))+"/documents", &runtimev1pb.IndexDocumentsRequestAlpha1{
			Documents: []*runtimev1pb.SearchDocument{
				newDocument(t, "doc-1", map[string]any{"title": "alpha"}),
				newDocument(t, "doc-1", map[string]any{"title": "beta"}),
			},
		}, nil)
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})
}

func createIndex(t *testing.T, ctx context.Context, httpClient *nethttp.Client, baseURL, index string) {
	t.Helper()
	// Index settings are component specific and travel in metadata; the
	// Meilisearch defaults are enough for a full text query.
	status, body := do(t, ctx, httpClient, nethttp.MethodPost, indexURL(baseURL, index), &runtimev1pb.CreateIndexRequestAlpha1{}, nil)
	require.Equal(t, nethttp.StatusOK, status, string(body))
}

func searchRequest(text string) *runtimev1pb.SearchRequestAlpha1 {
	return &runtimev1pb.SearchRequestAlpha1{
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

func indexURL(baseURL, index string) string {
	return baseURL + "/indexes/" + index
}

func indexName(suffix string) string {
	return fmt.Sprintf("it-search-http-%s-%d", suffix, time.Now().UnixNano())
}

func do(t *testing.T, ctx context.Context, httpClient *nethttp.Client, method, url string, in proto.Message, out proto.Message) (int, []byte) {
	t.Helper()
	var body []byte
	if in != nil {
		var err error
		body, err = protojson.Marshal(in)
		require.NoError(t, err)
	}
	return doRaw(t, ctx, httpClient, method, url, body, out)
}

func doRaw(t *testing.T, ctx context.Context, httpClient *nethttp.Client, method, url string, body []byte, out proto.Message) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := nethttp.NewRequestWithContext(ctx, method, url, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if out != nil && resp.StatusCode == nethttp.StatusOK && len(respBody) > 0 {
		require.NoError(t, protojson.Unmarshal(respBody, out), string(respBody))
	}
	return resp.StatusCode, respBody
}
