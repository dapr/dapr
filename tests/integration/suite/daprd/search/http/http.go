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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	client := &http.Client{Timeout: 10 * time.Second}
	baseURL := "http://" + s.daprd.HTTPAddress() + "/v1.0-alpha1/search"

	t.Run("lifecycle", func(t *testing.T) {
		index := indexName("http-lifecycle")
		createIndex(t, ctx, client, baseURL, storeName, index)
		t.Cleanup(func() {
			_ = doNoBody(context.Background(), client, http.MethodDelete, indexURL(baseURL, storeName, index), http.StatusOK)
		})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := listIndexes(t, ctx, client, baseURL, storeName)
			assert.Contains(c, resp.Indexes, index)
		}, 10*time.Second, 100*time.Millisecond)

		desc := describeIndex(t, ctx, client, baseURL, storeName, index)
		assert.Equal(t, index, desc.Index)

		require.NoError(t, doNoBody(ctx, client, http.MethodDelete, indexURL(baseURL, storeName, index), http.StatusOK))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := listIndexes(t, ctx, client, baseURL, storeName)
			assert.NotContains(c, resp.Indexes, index)
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("documents", func(t *testing.T) {
		index := indexName("http-documents")
		createIndex(t, ctx, client, baseURL, storeName, index)
		t.Cleanup(func() {
			_ = doNoBody(context.Background(), client, http.MethodDelete, indexURL(baseURL, storeName, index), http.StatusOK)
		})

		idxResp := indexDocuments(t, ctx, client, baseURL, storeName, index)
		require.Len(t, idxResp.Results, 3)
		for _, result := range idxResp.Results {
			assert.True(t, result.Success, result.ErrorMessage)
		}

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := searchText(t, ctx, client, baseURL, storeName, index, "alpha")
			assert.GreaterOrEqual(c, len(resp.Hits), 2)
			assert.Contains(c, hitIDs(resp.Hits), "doc-1")
			assert.Contains(c, hitIDs(resp.Hits), "doc-3")
		}, 10*time.Second, 100*time.Millisecond)

		delResp := deleteDocuments(t, ctx, client, baseURL, storeName, index)
		require.Len(t, delResp.Results, 3)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := searchText(t, ctx, client, baseURL, storeName, index, "alpha")
			assert.Empty(c, resp.Hits)
		}, 10*time.Second, 100*time.Millisecond)

		require.NoError(t, doNoBody(ctx, client, http.MethodDelete, indexURL(baseURL, storeName, index), http.StatusOK))
	})

	t.Run("error missing store", func(t *testing.T) {
		resp, body := do(t, ctx, client, http.MethodGet, baseURL+"/does-not-exist/indexes", nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Contains(t, string(body), "ERR_SEARCH_STORE_NOT_FOUND")
	})

	t.Run("error malformed request", func(t *testing.T) {
		resp, body := do(t, ctx, client, http.MethodPost, indexURL(baseURL, storeName, indexName("bad-json")), bytes.NewBufferString("{"))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, string(body), "ERR_MALFORMED_REQUEST")
	})
}

func createIndex(t *testing.T, ctx context.Context, client *http.Client, baseURL, store, index string) {
	t.Helper()
	body := map[string]any{"fields": fields()}
	resp, respBody := doJSON(t, ctx, client, http.MethodPost, indexURL(baseURL, store, index), body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(respBody))
}

func listIndexes(t *testing.T, ctx context.Context, client *http.Client, baseURL, store string) listIndexesResponse {
	t.Helper()
	resp, body := do(t, ctx, client, http.MethodGet, baseURL+"/"+store+"/indexes", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var out listIndexesResponse
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

func describeIndex(t *testing.T, ctx context.Context, client *http.Client, baseURL, store, index string) describeIndexResponse {
	t.Helper()
	resp, body := do(t, ctx, client, http.MethodGet, indexURL(baseURL, store, index), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var out describeIndexResponse
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

func indexDocuments(t *testing.T, ctx context.Context, client *http.Client, baseURL, store, index string) operationResponse {
	t.Helper()
	body := map[string]any{"ack": "INDEX_ACK_DURABLE", "documents": documents()}
	resp, respBody := doJSON(t, ctx, client, http.MethodPost, indexURL(baseURL, store, index)+"/documents", body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(respBody))
	var out operationResponse
	require.NoError(t, json.Unmarshal(respBody, &out))
	return out
}

func deleteDocuments(t *testing.T, ctx context.Context, client *http.Client, baseURL, store, index string) operationResponse {
	t.Helper()
	body := map[string]any{"ack": "INDEX_ACK_DURABLE", "ids": []string{"doc-1", "doc-2", "doc-3"}}
	resp, respBody := doJSON(t, ctx, client, http.MethodDelete, indexURL(baseURL, store, index)+"/documents", body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(respBody))
	var out operationResponse
	require.NoError(t, json.Unmarshal(respBody, &out))
	return out
}

func searchText(t *testing.T, ctx context.Context, client *http.Client, baseURL, store, index, text string) searchResponse {
	t.Helper()
	body := map[string]any{"text": text, "topK": 10, "includeContent": true}
	resp, respBody := doJSON(t, ctx, client, http.MethodPost, indexURL(baseURL, store, index)+"/query", body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(respBody))
	var out searchResponse
	require.NoError(t, json.Unmarshal(respBody, &out))
	return out
}

func doNoBody(ctx context.Context, client *http.Client, method, url string, statusCode int) error {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != statusCode {
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}
	return nil
}

func doJSON(t *testing.T, ctx context.Context, client *http.Client, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	return do(t, ctx, client, method, url, bytes.NewReader(data))
}

func do(t *testing.T, ctx context.Context, client *http.Client, method, url string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, respBody
}

func indexURL(baseURL, store, index string) string {
	return baseURL + "/" + store + "/indexes/" + index
}

func fields() []map[string]any {
	return []map[string]any{
		{"name": "title", "type": "INDEX_FIELD_TYPE_TEXT", "searchable": true, "sortable": true},
		{"name": "body", "type": "INDEX_FIELD_TYPE_TEXT", "searchable": true},
		{"name": "category", "type": "INDEX_FIELD_TYPE_KEYWORD", "filterable": true, "searchable": true},
		{"name": "price", "type": "INDEX_FIELD_TYPE_DOUBLE", "filterable": true, "sortable": true},
	}
}

func documents() []map[string]any {
	return []map[string]any{
		{"id": "doc-1", "content": map[string]any{"title": "alpha guide", "body": "first alpha document", "category": "guide", "price": 10.0}},
		{"id": "doc-2", "content": map[string]any{"title": "beta guide", "body": "second document", "category": "guide", "price": 25.0}},
		{"id": "doc-3", "content": map[string]any{"title": "alpha reference", "body": "reference material", "category": "reference", "price": 30.0}},
	}
}

func hitIDs(hits []searchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Document.ID)
	}
	return ids
}

func indexName(prefix string) string {
	return fmt.Sprintf("it-search-%s-%d", prefix, time.Now().UnixNano())
}

type listIndexesResponse struct {
	Indexes []string `json:"indexes"`
}

type describeIndexResponse struct {
	Index string `json:"index"`
}

type operationResponse struct {
	Results []operationResult `json:"results"`
}

type operationResult struct {
	ID           string `json:"id"`
	Success      bool   `json:"success"`
	ErrorMessage string `json:"errorMessage"`
}

type searchResponse struct {
	Hits []searchHit `json:"hits"`
}

type searchHit struct {
	Document searchDocument `json:"document"`
}

type searchDocument struct {
	ID string `json:"id"`
}
