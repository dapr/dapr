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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
	kitErrors "github.com/dapr/kit/errors"
	"github.com/stretchr/testify/require"
)

const (
	ErrInfoType      = "type.googleapis.com/google.rpc.ErrorInfo"
	ResourceInfoType = "type.googleapis.com/google.rpc.ResourceInfo"
	BadRequestType   = "type.googleapis.com/google.rpc.BadRequest"
	HelpType         = "type.googleapis.com/google.rpc.Help"
)

func init() {
	suite.Register(new(errorcodes))
}

type errorcodes struct {
	daprd *daprd.Daprd
}

func (e *errorcodes) Setup(t *testing.T) []framework.Option {
	e.daprd = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: lockstore
spec:
  type: lock.redis
  version: v1
  metadata:
  - name: redisHost
    value: localhost:6379
  - name: redisPassword
    value: ""
`))

	return []framework.Option{
		framework.WithProcesses(e.daprd),
	}
}

func (e *errorcodes) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)

	// Covers apierrors.ERR_LOCK_NOT_FOUND
	t.Run("lock doesn't exist", func(t *testing.T) {
		name := "lock-doesn't-exist"
		payload := `{"resourceId": "resource", "lockOwner": "owner", "expiryInSeconds": 10}`
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/lock/%s", e.daprd.HTTPPort(), name)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_LOCK_NOT_FOUND", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("lock %s is not found", name), errMsg)

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
		require.Equal(t, kitErrors.CodePrefixLock+kitErrors.CodeNotFound, detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	// Covers apierrors.ERR_LOCK_NOT_CONFIGURED
	t.Run("lock store not configured", func(t *testing.T) {
		daprdNoLockStore := daprd.New(t, daprd.WithAppID("daprd_no_lock_store"))
		daprdNoLockStore.Run(t, ctx)
		daprdNoLockStore.WaitUntilRunning(t, ctx)
		defer daprdNoLockStore.Cleanup(t)

		name := "lock"
		payload := `{"resourceId": "resource", "lockOwner": "owner", "expiryInSeconds": 10}`
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/lock/%s", daprdNoLockStore.HTTPPort(), name)

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
		require.Equal(t, "ERR_LOCK_NOT_CONFIGURED", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("lock %s is not configured", name), errMsg)

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
		require.Equal(t, kitErrors.CodePrefixLock+kitErrors.CodeNotConfigured, detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	// Covers apierrors.ERR_RESOURCE_ID_EMPTY
	t.Run("lock resource id empty", func(t *testing.T) {
		name := "lockstore"
		payload := `{"lockOwner":"owner", "expiryInSeconds": 10}`
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/lock/%s", e.daprd.HTTPPort(), name)

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
		require.Equal(t, "ERR_RESOURCE_ID_EMPTY", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "lock resource id is empty", errMsg)

		// Confirm that the 'details' field exists and has one element
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
		require.Equal(t, "DAPR_LOCK_RESOURCE_ID_EMPTY", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "lock", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})

	// Covers apierrors.ERR_LOCK_OWNER_EMPTY
	t.Run("lock owner empty", func(t *testing.T) {
		name := "lockstore"
		payload := `{"resourceId": "resource", "expiryInSeconds": 10}`
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/lock/%s", e.daprd.HTTPPort(), name)

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
		require.Equal(t, "ERR_OWNER_EMPTY", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "lock owner is empty", errMsg)

		// Confirm that the 'details' field exists and has one element
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
		require.Equal(t, "DAPR_LOCK_OWNER_EMPTY", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "lock", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})

	// Covers apierrors.ERR_EXPIRY_NOT_POSITIVE
	t.Run("lock expiry in seconds not positive", func(t *testing.T) {
		name := "lockstore"
		payload := `{"resourceId": "resource", "lockOwner": "owner", "expiryInSeconds": -1}`
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/lock/%s", e.daprd.HTTPPort(), name)

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
		require.Equal(t, "ERR_EXPIRY_NOT_POSITIVE", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "expiry in seconds is not positive", errMsg)

		// Confirm that the 'details' field exists and has one element
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
		require.Equal(t, "DAPR_LOCK_EXPIRY_NOT_POSITIVE", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "lock", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})

	// Covers apierrors.ERR_TRY_LOCK
	t.Run("try lock failed", func(t *testing.T) {
		name := "lockstore"
		resourceId := "resource||"
		payload := fmt.Sprintf(`{"resourceId": "%s", "lockOwner":"owner", "expiryInSeconds": 10}`, resourceId)
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/lock/%s", e.daprd.HTTPPort(), name)

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
		require.Equal(t, "ERR_TRY_LOCK", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "failed to try acquiring lock", errMsg)

		// Confirm that the 'details' field exists and has one element
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
		require.Equal(t, "DAPR_LOCK_TRY_LOCK_FAILED", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "lock", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})

	// Covers apierrors.ERR_Unlock
	t.Run("unlock failed", func(t *testing.T) {
		name := "lockstore"
		resourceId := "resource||"
		payload := fmt.Sprintf(`{"resourceId": "%s", "lockOwner":"owner"}`, resourceId)
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/unlock/%s", e.daprd.HTTPPort(), name)

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
		require.Equal(t, "ERR_UNLOCK", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "failed to release lock", errMsg)

		// Confirm that the 'details' field exists and has one element
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
		require.Equal(t, "DAPR_LOCK_UNLOCK_FAILED", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "lock", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})
}
