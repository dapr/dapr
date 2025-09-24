/*
Copyright 2023 The Dapr Authors
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

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	apierrors "github.com/dapr/dapr/pkg/api/errors"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd *procdaprd.Daprd

	queryErr func(*testing.T) error
}

const (
	ErrInfoType      = "type.googleapis.com/google.rpc.ErrorInfo"
	ResourceInfoType = "type.googleapis.com/google.rpc.ResourceInfo"
	BadRequestType   = "type.googleapis.com/google.rpc.BadRequest"
	HelpType         = "type.googleapis.com/google.rpc.Help"
)

func (e *errors) Setup(t *testing.T) []framework.Option {
	socket := socket.New(t)

	e.queryErr = func(t *testing.T) error {
		require.FailNow(t, "query should not be called")
		return nil
	}

	storeWithNoTransactional := statestore.New(t,
		statestore.WithSocket(socket),
		statestore.WithStateStore(inmemory.New(t,
			inmemory.WithFeatures(),
		)),
	)

	storeWithQuerier := statestore.New(t,
		statestore.WithSocket(socket),
		statestore.WithStateStore(inmemory.NewQuerier(t,
			inmemory.WithQueryFn(func(context.Context, *state.QueryRequest) (*state.QueryResponse, error) {
				return nil, e.queryErr(t)
			}),
		)),
	)
	storeWithMultiMaxSize := statestore.New(t,
		statestore.WithSocket(socket),
		statestore.WithStateStore(inmemory.NewTransactionalMultiMaxSize(t,
			inmemory.WithTransactionalStoreMultiMaxSizeFn(func() int {
				return 1
			}),
		)),
	)

	e.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore-non-transactional
spec:
 type: state.%s
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore-pluggable-querier
spec:
  type: state.%s
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore-pluggable-multimaxsize
spec:
  type: state.%s
  version: v1
`, storeWithNoTransactional.SocketName(), storeWithQuerier.SocketName(), storeWithMultiMaxSize.SocketName())),
		procdaprd.WithSocket(t, socket),
		procdaprd.WithErrorCodeMetrics(t),
	)

	return []framework.Option{
		framework.WithProcesses(storeWithNoTransactional, storeWithQuerier, storeWithMultiMaxSize, e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Covers errutils.StateStoreNotFound()
	t.Run("state store doesn't exist", func(t *testing.T) {
		storeName := "mystore-doesnt-exist"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", e.daprd.HTTPPort(), storeName)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(""))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_STATE_STORE_NOT_FOUND", errCode)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			e.daprd.Metrics(c, ctx).MatchMetricAndSum(c, 1, "dapr_error_code_total", "category:state", "error_code:ERR_STATE_STORE_NOT_FOUND")
		}, time.Second*10, time.Millisecond*10)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("state store %s is not found", storeName), errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 1)

		// Confirm that the first element of the 'details' array has the correct ErrorInfo details
		detailsObject, ok := detailsArray[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "dapr.io", detailsObject["domain"])
		require.Equal(t, "DAPR_STATE_NOT_FOUND", detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	// Covers errutils.StateStoreInvalidKeyName()
	t.Run("invalid key name", func(t *testing.T) {
		storeName := "mystore"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", e.daprd.HTTPPort(), storeName)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(`[{"key": "ke||y1", "value": "value1"}]`))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_MALFORMED_REQUEST", errCode)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			e.daprd.Metrics(c, ctx).MatchMetricAndSum(c, 1, "dapr_error_code_total", "category:state", "error_code:ERR_MALFORMED_REQUEST")
		}, time.Second*10, time.Millisecond*10)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Contains(t, errMsg, fmt.Sprintf("input key/keyPrefix '%s' can't contain '%s'", "ke||y1", "||"))

		// Confirm that the 'details' field exists and has three elements
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 3)

		var errInfo map[string]interface{}
		var resInfo map[string]interface{}
		var badRequest map[string]interface{}

		for _, detail := range detailsArray {
			d, innerOK := detail.(map[string]interface{})
			require.True(t, innerOK)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			case BadRequestType:
				badRequest = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, "DAPR_STATE_ILLEGAL_KEY", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.Equal(t, "state", resInfo["resource_type"])
		require.Equal(t, storeName, resInfo["resource_name"])

		// Confirm that the BadRequest details are correct
		fieldViolationsArray, ok := badRequest["field_violations"].([]interface{})
		require.True(t, ok)

		fieldViolations, ok := fieldViolationsArray[0].(map[string]interface{})
		require.True(t, ok)
		require.Len(t, fieldViolationsArray, 1)
		require.Equal(t, "ke||y1", fieldViolations["field"])
		require.Contains(t, fmt.Sprintf("input key/keyPrefix '%s' can't contain '%s'", "ke||y1", "||"), fieldViolations["field"])
	})

	t.Run("state store doesn't support query api", func(t *testing.T) {
		storeName := "mystore"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/state/%s/query", e.daprd.HTTPPort(), storeName)
		payload := `{"filter":{"EQ":{"state":"CA"}},"sort":[{"key":"person.id","order":"DESC"}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_STATE_STORE_NOT_SUPPORTED", errCode)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			e.daprd.Metrics(c, ctx).MatchMetricAndSum(c, 3, "dapr_error_code_total")
		}, 10*time.Second, 100*time.Millisecond)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Contains(t, errMsg, "state store does not support querying")

		// Confirm that the 'details' field exists and has two elements
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 2)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}

		for _, detail := range detailsArray {
			d, ok := detail.(map[string]interface{})
			require.True(t, ok)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.NotEmptyf(t, errInfo, "ErrorInfo not found in %+v", detailsArray)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, "DAPR_STATE_QUERYING_NOT_SUPPORTED", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "state", resInfo["resource_type"])
		require.Equal(t, storeName, resInfo["resource_name"])
	})

	t.Run("state store query failed", func(t *testing.T) {
		storeName := "mystore-pluggable-querier"

		e.queryErr = func(*testing.T) error {
			return apierrors.StateStore(storeName).QueryFailed("this is a custom error string")
		}

		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/state/%s/query", e.daprd.HTTPPort(), storeName)
		payload := `{"filter":{"EQ":{"state":"CA"}},"sort":[{"key":"person.id","order":"DESC"}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_STATE_QUERY", errCode)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			e.daprd.Metrics(t, ctx).MatchMetricAndSum(c, 1, "dapr_error_code_total", "category:state", "error_code:ERR_STATE_QUERY")
		}, time.Second*10, time.Millisecond*10)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Contains(t, errMsg, fmt.Sprintf("state store %s query failed: %s", storeName, "this is a custom error string"))

		// Confirm that the 'details' field exists and has two elements
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 2)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}

		for _, detail := range detailsArray {
			d, ok := detail.(map[string]interface{})
			require.True(t, ok)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.NotEmptyf(t, errInfo, "ErrorInfo not found in %+v", detailsArray)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, "DAPR_STATE_QUERY_FAILED", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "state", resInfo["resource_type"])
		require.Equal(t, storeName, resInfo["resource_name"])
	})

	t.Run("state store too many transactional operations", func(t *testing.T) {
		storeName := "mystore-pluggable-multimaxsize"

		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/transaction", e.daprd.HTTPPort(), storeName)
		payload := `{"operations": [{"operation": "upsert","request": {"key": "key1","value": "val1"}},{"operation": "delete","request": {"key": "key2"}}],"metadata": {"partitionKey": "key2"}}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_STATE_STORE_TOO_MANY_TRANSACTIONS", errCode)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			e.daprd.Metrics(c, ctx).MatchMetricAndSum(c, 1, "dapr_error_code_total", "category:state", "error_code:ERR_STATE_STORE_TOO_MANY_TRANSACTIONS")
		}, time.Second*10, time.Millisecond*10)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Contains(t, errMsg, fmt.Sprintf("the transaction contains %d operations, which is more than what the state store supports: %d", 2, 1))

		// Confirm that the 'details' field exists and has two elements
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 2)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}

		for _, detail := range detailsArray {
			d, ok := detail.(map[string]interface{})
			require.True(t, ok)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.NotEmptyf(t, errInfo, "ErrorInfo not found in %+v", detailsArray)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, "DAPR_STATE_TOO_MANY_TRANSACTIONS", errInfo["reason"])
		require.Equal(t, map[string]interface{}{
			"currentOpsTransaction": "2", "maxOpsPerTransaction": "1",
		}, errInfo["metadata"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "state", resInfo["resource_type"])
		require.Equal(t, storeName, resInfo["resource_name"])
	})

	t.Run("state transactions not supported", func(t *testing.T) {
		storeName := "mystore-non-transactional"

		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/transaction", e.daprd.HTTPPort(), storeName)
		payload := `{"operations": [{"operation": "upsert","request": {"key": "key1","value": "val1"}},{"operation": "delete","request": {"key": "key2"}}],"metadata": {"partitionKey": "key2"}}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_STATE_STORE_NOT_SUPPORTED", errCode)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			e.daprd.Metrics(c, ctx).MatchMetricAndSum(c, 2, "dapr_error_code_total", "category:state", "error_code:ERR_STATE_STORE_NOT_SUPPORTED")
		}, time.Second*10, time.Millisecond*10)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("state store %s doesn't support transactions", "mystore-non-transactional"), errMsg)

		// Confirm that the 'details' field exists and has two elements
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 3)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}
		var help map[string]interface{}

		for _, detail := range detailsArray {
			d, innerOK := detail.(map[string]interface{})
			require.True(t, innerOK)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			case HelpType:
				help = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.NotEmptyf(t, errInfo, "ErrorInfo not found in %+v", detailsArray)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, "DAPR_STATE_TRANSACTIONS_NOT_SUPPORTED", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "state", resInfo["resource_type"])
		require.Equal(t, storeName, resInfo["resource_name"])

		// Confirm that the Help details are correct
		helpLinks, ok := help["links"].([]interface{})
		require.True(t, ok, "Failed to assert Help links as an array")
		require.NotEmpty(t, helpLinks, "Help links array is empty")

		link, ok := helpLinks[0].(map[string]interface{})
		require.True(t, ok, "Failed to assert link as a map")

		linkURL, ok := link["url"].(string)
		require.True(t, ok, "Failed to assert link URL as a string")
		require.Equal(t, "https://docs.dapr.io/reference/components-reference/supported-state-stores/", linkURL)

		linkDescription, ok := link["description"].(string)
		require.True(t, ok, "Failed to assert link description as a string")
		require.Equal(t, "Check the list of state stores and the features they support", linkDescription)
	})
}
