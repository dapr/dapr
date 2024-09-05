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

package grpc

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/components/pluggable"
	"github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
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
		statestore.WithBulkDeleteError(pluggable.MustStatusErrorFromKitError(
			codes.AlreadyExists, 508, "bulkdelete-error", "bulkdelete-tag", &errdetails.ErrorInfo{
				Reason: "bulkdelete-reason",
				Domain: "bulkdelete-domain",
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
	)

	return []framework.Option{
		framework.WithProcesses(statestore, c.daprd),
	}
}

func (c *customerrors) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	client := c.daprd.GRPCClient(t, ctx)

	_, err := client.GetState(ctx, &rtv1.GetStateRequest{
		StoreName: "mystore", Key: "key",
	})
	require.Error(t, err)
	assert.Equal(t, "rpc error: code = DataLoss desc = get-error", err.Error())
	serr, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.DataLoss.String(), serr.Code().String(), "unexpected code")

	_, err = client.GetBulkState(ctx, &rtv1.GetBulkStateRequest{
		StoreName: "mystore", Keys: []string{"key"},
	})
	require.Error(t, err)
	assert.Equal(t, "rpc error: code = Aborted desc = bulkget-error", err.Error())
	serr, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted.String(), serr.Code().String(), "unexpected code")

	_, err = client.DeleteBulkState(ctx, &rtv1.DeleteBulkStateRequest{
		StoreName: "mystore", States: []*common.StateItem{
			{Key: "key1"}, {Key: "key2"},
		},
	})
	require.Error(t, err)
	assert.Equal(t, "rpc error: code = AlreadyExists desc = bulkdelete-error", err.Error())
	serr, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists.String(), serr.Code().String(), "unexpected code")

	_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "mystore",
		States: []*common.StateItem{
			{Key: "key1", Value: []byte("value1")},
			{Key: "key2", Value: []byte("value2")},
		},
	})
	require.Error(t, err)
	assert.Equal(t, "rpc error: code = Canceled desc = bulkset-error", err.Error())
	serr, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Canceled.String(), serr.Code().String(), "unexpected code")

	_, err = client.QueryStateAlpha1(ctx, &rtv1.QueryStateRequest{
		StoreName: "mystore",
		Query:     `{"filter":{"EQ":{"state":"CA"}},"sort":[{"key":"person.id","order":"DESC"}]}`,
	})
	require.Error(t, err)
	assert.Equal(t, "rpc error: code = AlreadyExists desc = query-error", err.Error())
	serr, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists.String(), serr.Code().String(), "unexpected code")

	_, err = client.DeleteState(ctx, &rtv1.DeleteStateRequest{
		StoreName: "mystore",
		Key:       "key",
	})
	require.Error(t, err)
	assert.Equal(t, "rpc error: code = OutOfRange desc = delete-error", err.Error())
	serr, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.OutOfRange.String(), serr.Code().String(), "unexpected code")

	_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "mystore",
		States: []*common.StateItem{
			{Key: "key", Value: []byte("value")},
		},
	})
	require.Error(t, err)
	assert.Equal(t, "rpc error: code = ResourceExhausted desc = set-error", err.Error())
	serr, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted.String(), serr.Code().String(), "unexpected code")
}
