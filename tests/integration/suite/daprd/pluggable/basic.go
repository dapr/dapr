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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *daprd.Daprd
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	// Darwin enforces a maximum 104 byte socket name limit, so we need to be a
	// bit fancy on how we generate the name.
	tmp, err := nettest.LocalPath()
	require.NoError(t, err)

	socketDir := filepath.Join(tmp, util.RandomString(t, 4))
	require.NoError(t, os.MkdirAll(socketDir, 0o700))
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(socketDir))
	})

	store := statestore.New(t,
		statestore.WithSocketDirectory(socketDir),
		statestore.WithStateStore(inmemory.New(t)),
	)

	b.daprd = daprd.New(t,
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
`, store.SocketName())),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_COMPONENTS_SOCKETS_FOLDER", socketDir,
		)),
	)

	return []framework.Option{
		framework.WithProcesses(store, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := util.DaprGRPCClient(t, ctx, b.daprd.GRPCPort())

	now := time.Now()
	_, err := client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "mystore",
		States: []*commonv1.StateItem{
			{
				Key: "key1", Value: []byte("value1"),
			},
			{
				Key: "key2", Value: []byte("value2"),
				Options: &commonv1.StateOptions{
					Concurrency: commonv1.StateOptions_CONCURRENCY_FIRST_WRITE,
				},
			},
			{
				Key: "key3", Value: []byte("value3"),
				Metadata: map[string]string{"ttlInSeconds": "1"},
			},
			{
				Key: "key4", Value: []byte("value4"),
				Metadata: map[string]string{"ttlInSeconds": "1"},
				Options: &commonv1.StateOptions{
					Concurrency: commonv1.StateOptions_CONCURRENCY_FIRST_WRITE,
				},
			},
		},
	})
	require.NoError(t, err)

	resp, err := client.GetState(ctx, &rtv1.GetStateRequest{
		StoreName: "mystore", Key: "key2",
	})
	require.NoError(t, err)
	etag2 := resp.GetEtag()

	resp, err = client.GetState(ctx, &rtv1.GetStateRequest{
		StoreName: "mystore", Key: "key4",
	})
	require.NoError(t, err)
	etag4 := resp.GetEtag()

	{
		resp, err := client.GetBulkState(ctx, &rtv1.GetBulkStateRequest{
			StoreName: "mystore",
			Keys:      []string{"key1", "key2", "key3", "key4"},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetItems(), 4)
		assert.Equal(t, "key1", resp.GetItems()[0].GetKey())
		assert.Equal(t, "value1", string(resp.GetItems()[0].GetData()))
		assert.Empty(t, resp.GetItems()[0].GetMetadata())

		assert.Equal(t, "key2", resp.GetItems()[1].GetKey())
		assert.Equal(t, "value2", string(resp.GetItems()[1].GetData()))
		assert.Equal(t, etag2, resp.GetItems()[1].GetEtag())

		assert.Equal(t, "key3", resp.GetItems()[2].GetKey())
		assert.Equal(t, "value3", string(resp.GetItems()[2].GetData()))
		if assert.Contains(t, resp.GetItems()[2].GetMetadata(), "ttlExpireTime") {
			expireTime, eerr := time.Parse(time.RFC3339, resp.GetItems()[2].GetMetadata()["ttlExpireTime"])
			require.NoError(t, eerr)
			assert.WithinDuration(t, now.Add(time.Second), expireTime, time.Second)
		}

		assert.Equal(t, "key4", resp.GetItems()[3].GetKey())
		assert.Equal(t, "value4", string(resp.GetItems()[3].GetData()))
		assert.Equal(t, etag4, resp.GetItems()[3].GetEtag())
		if assert.Contains(t, resp.GetItems()[3].GetMetadata(), "ttlExpireTime") {
			expireTime, eerr := time.Parse(time.RFC3339, resp.GetItems()[3].GetMetadata()["ttlExpireTime"])
			require.NoError(t, eerr)
			assert.WithinDuration(t, now.Add(time.Second), expireTime, time.Second)
		}

		_, err = client.DeleteState(ctx, &rtv1.DeleteStateRequest{
			StoreName: "mystore",
			Key:       "key1",
		})
		require.NoError(t, err)
		_, err = client.DeleteBulkState(ctx, &rtv1.DeleteBulkStateRequest{
			StoreName: "mystore",
			States: []*commonv1.StateItem{
				{
					Key: "key2",
				},
			},
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, key := range []string{"key3", "key4"} {
			resp, err := client.GetState(ctx, &rtv1.GetStateRequest{
				StoreName: "mystore",
				Key:       key,
			})
			require.NoError(t, err)
			assert.Empty(c, resp.GetData())
		}
	}, time.Second*2, time.Millisecond*100)
}
