/*
Copyright 2024 The Dapr Authors
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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/file"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/secretstore"
	"github.com/dapr/dapr/tests/integration/framework/process/secretstore/localfile"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	kitErrors "github.com/dapr/kit/errors"
	"github.com/stretchr/testify/require"
)

const (
	ErrInfoType      = "type.googleapis.com/google.rpc.ErrorInfo"
	ResourceInfoType = "type.googleapis.com/google.rpc.ResourceInfo"
)

func init() {
	suite.Register(new(errorcodes))
}

type errorcodes struct {
	daprd *procdaprd.Daprd
}

func (e *errorcodes) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	socket := socket.New(t)

	storeWithGetSecret := secretstore.New(t,
		secretstore.WithSocket(socket),
		secretstore.WithSecretStore(localfile.New(t,
			localfile.WithGetSecretFn(func(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
				return secretstores.GetSecretResponse{}, errors.New("get secret error")
			}),
		)),
	)

	storeWithBulkGetSecret := secretstore.New(t,
		secretstore.WithSocket(socket),
		secretstore.WithSecretStore(localfile.New(t,
			localfile.WithBulkGetSecretFn(func(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
				return secretstores.BulkGetSecretResponse{}, errors.New("bulk get secret error")
			}),
		)),
	)

	fileName := file.Paths(t, 1)[0]
	values := map[string]string{
		"mySecret":   "myKey",
		"notAllowed": "myKey",
	}
	bytes, err := json.Marshal(values)
	require.NoError(t, err)
	err = os.WriteFile(fileName, bytes, 0o600)
	require.NoError(t, err)
	configFile := file.Paths(t, 1)[0]
	err = os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: appconfig
spec:
  secrets:
    scopes:
      - storeName: mystore
        defaultAccess: allow # this is the default value, line can be omitted
        deniedSecrets: ["notAllowed"]
`), 0o600)
	require.NoError(t, err)

	e.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore-get-secret-error
spec:
 type: secretstores.%s
 version: v1  
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore-bulk-get-secret-error
spec:
 type: secretstores.%s
 version: v1
`, strings.ReplaceAll(fileName, "'", "''"), storeWithGetSecret.SocketName(), storeWithBulkGetSecret.SocketName())),
		procdaprd.WithSocket(t, socket),
		procdaprd.WithConfigs(configFile),
	)

	return []framework.Option{
		framework.WithProcesses(storeWithGetSecret, storeWithBulkGetSecret, e.daprd),
	}
}

func (e *errorcodes) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Covers apierrors.SecretStore("").NotConfigured()
	t.Run("secret store not configured", func(t *testing.T) {
		// Start a new daprd without secret store
		daprdNoSecretStore := procdaprd.New(t, procdaprd.WithAppID("daprd_no_secret_store"))
		daprdNoSecretStore.Run(t, ctx)
		daprdNoSecretStore.WaitUntilRunning(t, ctx)
		defer daprdNoSecretStore.Cleanup(t)

		storeName := "mystore"
		key := "mysecret"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/%s", daprdNoSecretStore.HTTPPort(), storeName, key)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, strings.NewReader(""))
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
		require.Equal(t, "ERR_SECRET_STORES_NOT_CONFIGURED", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "secret store is not configured", errMsg)

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
		require.Equal(t, kitErrors.CodePrefixSecretStore+kitErrors.CodeNotConfigured, detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	// Covers apierrors.SecretStore("").NotFound()
	t.Run("secret store doesn't exist", func(t *testing.T) {
		storeName := "mystore-doesnt-exist"
		key := "mysecret"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/%s", e.daprd.HTTPPort(), storeName, key)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, strings.NewReader(""))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_SECRET_STORE_NOT_FOUND", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("secret store %s not found", storeName), errMsg)

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
		require.Equal(t, kitErrors.CodePrefixSecretStore+kitErrors.CodeNotFound, detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	// Covers apierrors.SecretStore("").PermissionDenied()
	t.Run("secret store permission denied", func(t *testing.T) {
		storeName := "mystore"
		key := "notAllowed"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/%s", e.daprd.HTTPPort(), storeName, key)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, strings.NewReader(""))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusForbidden, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_PERMISSION_DENIED", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("access denied by policy to get %q from %q", key, storeName), errMsg)

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
		require.Equal(t, kitErrors.CodePrefixSecretStore+"PERMISSION_DENIED", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, string(metadata.SecretStoreType), resInfo["resource_type"])
		require.Equal(t, storeName, resInfo["resource_name"])
	})

	// Covers apierrors.SecretStore("").GetSecret()
	t.Run("secret store get secret failed", func(t *testing.T) {
		storeName := "mystore-get-secret-error"
		key := "mysecret"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/%s", e.daprd.HTTPPort(), storeName, key)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, strings.NewReader(""))
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
		require.Equal(t, "ERR_SECRET_GET", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("failed getting secret with key %s from secret store %s: %s", key, storeName, "rpc error: code = Unknown desc = get secret error"), errMsg)

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
		require.Equal(t, kitErrors.CodePrefixSecretStore+"GET_SECRET", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, string(metadata.SecretStoreType), resInfo["resource_type"])
		require.Equal(t, storeName, resInfo["resource_name"])
	})

	// Covers apierrors.SecretStore("").BulkSecretGet()
	t.Run("secret store get bulk secret failed", func(t *testing.T) {
		storeName := "mystore-bulk-get-secret-error"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/bulk", e.daprd.HTTPPort(), storeName)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, strings.NewReader(""))
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
		require.Equal(t, "ERR_SECRET_GET", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("failed getting secrets from secret store %s: %v", storeName, "rpc error: code = Unknown desc = bulk get secret error"), errMsg)

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
		require.Equal(t, kitErrors.CodePrefixSecretStore+"GET_BULK_SECRET", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, string(metadata.SecretStoreType), resInfo["resource_type"])
		require.Equal(t, storeName, resInfo["resource_name"])
	})
}
