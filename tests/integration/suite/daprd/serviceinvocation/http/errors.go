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
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"

	"github.com/dapr/dapr/tests/integration/suite"
)

const (
	ErrInfoType      = "type.googleapis.com/google.rpc.ErrorInfo"
	ResourceInfoType = "type.googleapis.com/google.rpc.ResourceInfo"
	HelpLinkType     = "type.googleapis.com/google.rpc.Help"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	newHTTPServer := func() *prochttp.HTTP {
		handler := http.NewServeMux()
		return prochttp.New(t, prochttp.WithHandler(handler))
	}
	srv1 := newHTTPServer()
	srv2 := newHTTPServer()
	e.daprd1 = procdaprd.New(t, procdaprd.WithAppPort(srv1.Port()))
	e.daprd2 = procdaprd.New(t, procdaprd.WithAppPort(srv2.Port()))

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, e.daprd1, e.daprd2),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd1.WaitUntilRunning(t, ctx)
	e.daprd2.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)

	// Covers apierrors.NoAppId()
	t.Run("no app id", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/doesntexist", e.daprd1.HTTPPort(), "")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_DIRECT_INVOKE", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "failed getting app id either from the URL path or the header dapr-app-id", errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 3)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}
		var links map[string]interface{}

		for _, detail := range detailsArray {
			d, ok := detail.(map[string]interface{})
			require.True(t, ok)
			switch d["@type"] {
			case HelpLinkType:
				links = d
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
		require.NotEmptyf(t, links, "Help link not found in %+v", detailsArray...)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, "DAPR_SERVICE_INVOCATION_"+"APP_ID_EMPTY", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "invoke", resInfo["resource_type"])
		require.Equal(t, "service_invocation", resInfo["resource_name"])
	})
}
