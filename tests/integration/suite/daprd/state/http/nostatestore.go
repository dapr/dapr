/*
Copyright 2025 The Dapr Authors
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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nostatestore))
}

type nostatestore struct {
	daprd *daprd.Daprd
}

func (n *nostatestore) Setup(t *testing.T) []framework.Option {
	n.daprd = daprd.New(t, daprd.WithErrorCodeMetrics(t))

	return []framework.Option{
		framework.WithProcesses(n.daprd),
	}
}

func (n *nostatestore) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	storeName := "mystore"
	endpoint := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", n.daprd.HTTPPort(), storeName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(""))
	require.NoError(t, err)

	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	var data map[string]any
	require.NoError(t, json.Unmarshal([]byte(string(body)), &data))

	// Confirm that the 'errorCode' field exists and contains the correct error code
	errCode, exists := data["errorCode"]
	require.True(t, exists)
	require.Equal(t, "ERR_STATE_STORE_NOT_CONFIGURED", errCode)

	metric := fmt.Sprintf(
		"dapr_error_code_total|app_id:%s|category:state|error_code:ERR_STATE_STORE_NOT_CONFIGURED",
		n.daprd.AppID(),
	)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, n.daprd.Metrics(c, ctx).All()[metric], 1.0)
	}, time.Second*10, time.Millisecond*10)

	// Confirm that the 'message' field exists and contains the correct error message
	errMsg, exists := data["message"]
	require.True(t, exists)
	require.Equal(t, fmt.Sprintf("state store %s is not configured", storeName), errMsg)

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
	require.Equal(t, "DAPR_STATE_NOT_CONFIGURED", detailsObject["reason"])
	require.Equal(t, ErrInfoType, detailsObject["@type"])
}
