/*
Copyright 2024 The Dapr Authors
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
	"fmt"
	"io"
	nethttp "net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/components/pluggable"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(customerrors))
}

type customerrors struct {
	daprd *daprd.Daprd
}

func (c *customerrors) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	socket := socket.New(t)

	statestore := statestore.New(t,
		statestore.WithSocket(socket),
		statestore.WithStateStore(inmemory.New(t)),
		statestore.WithGetError(pluggable.MustStatusErrorFromKitError(
			codes.DataLoss, 506, "get-error", "get-tag", &errdetails.ErrorInfo{
				Reason: "get-reason",
				Domain: "get-domain",
			}),
		),
		statestore.WithBulkGetError(pluggable.MustStatusErrorFromKitError(
			codes.Aborted, 507, "bulkget-error", "bulkget-tag", &errdetails.ErrorInfo{
				Reason: "bulkget-reason",
				Domain: "bulkget-domain",
			}),
		),
		statestore.WithQueryError(pluggable.MustStatusErrorFromKitError(
			codes.AlreadyExists, 509, "query-error", "query-tag", &errdetails.ErrorInfo{
				Reason: "query-reason",
				Domain: "query-domain",
			}),
		),
		statestore.WithBulkSetError(pluggable.MustStatusErrorFromKitError(
			codes.Canceled, 510, "bulkset-error", "bulkset-tag", &errdetails.ErrorInfo{
				Reason: "bulkset-reason",
				Domain: "bulkset-domain",
			}),
		),
		statestore.WithDeleteError(pluggable.MustStatusErrorFromKitError(
			codes.OutOfRange, 511, "delete-error", "delete-tag", &errdetails.ErrorInfo{
				Reason: "delete-reason",
				Domain: "delete-domain",
			}),
		),
		statestore.WithSetError(pluggable.MustStatusErrorFromKitError(
			codes.ResourceExhausted, 512, "set-error", "set-tag", &errdetails.ErrorInfo{
				Reason: "set-reason",
				Domain: "set-domain",
			}),
		),
	)

	c.daprd = daprd.New(t,
		daprd.WithSocket(t, socket),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
`, statestore.SocketName()),
		),
		daprd.WithErrorCodeMetrics(t),
	)

	return []framework.Option{
		framework.WithProcesses(statestore, c.daprd),
	}
}

func (c *customerrors) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)
	client := client.HTTP(t)

	stateURL := fmt.Sprintf("http://%s/v1.0/state/mystore", c.daprd.HTTPAddress())

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, stateURL+"/key1", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, 506, resp.StatusCode)
	assert.Equal(t, `{"errorCode":"ERR_STATE_GET","message":"fail to get key1 from state store mystore: api error: code = DataLoss desc = get-error"}`, string(b))

	queryURL := fmt.Sprintf("http://%s/v1.0-alpha1/state/mystore/query", c.daprd.HTTPAddress())
	query := strings.NewReader(`{"filter":{"EQ":{"state":"CA"}},"sort":[{"key":"person.id","order":"DESC"}]}`)
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, queryURL, query)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, 509, resp.StatusCode)
	assert.Equal(t, `{"errorCode":"query-tag","message":"query-error","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","domain":"query-domain","metadata":null,"reason":"query-reason"}]}`, string(b))

	body := strings.NewReader(`[{"key":"key1","value":"value1"},{"key":"key2","value":"value2"}]`)
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, stateURL, body)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, 510, resp.StatusCode)
	assert.Equal(t, `{"errorCode":"ERR_STATE_SAVE","message":"failed saving state in state store mystore: api error: code = Canceled desc = bulkset-error"}`, string(b))

	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodDelete, stateURL+"/key1", body)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, 511, resp.StatusCode)
	assert.Equal(t, `{"errorCode":"ERR_STATE_DELETE","message":"failed deleting state with key key1: api error: code = OutOfRange desc = delete-error"}`, string(b))

	body = strings.NewReader(`[{"key":"key1","value":"value1"}]`)
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, stateURL, body)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, 512, resp.StatusCode)
	assert.Equal(t, `{"errorCode":"ERR_STATE_SAVE","message":"failed saving state in state store mystore: api error: code = ResourceExhausted desc = set-error"}`, string(b))

	assert.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.True(collect, c.daprd.Metrics(collect, ctx).MatchMetricAndSum(t, 2, "dapr_error_code_total", "category:state", "error_code:ERR_STATE_SAVE"))
		assert.True(collect, c.daprd.Metrics(collect, ctx).MatchMetricAndSum(t, 1, "dapr_error_code_total", "category:state", "error_code:ERR_STATE_GET"))
		assert.True(collect, c.daprd.Metrics(collect, ctx).MatchMetricAndSum(t, 1, "dapr_error_code_total", "category:state", "error_code:ERR_STATE_DELETE"))
	}, 5*time.Second, 100*time.Millisecond)
}
