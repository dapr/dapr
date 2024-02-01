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

package selfhosted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(crypto))
}

type crypto struct {
	daprd *daprd.Daprd

	resDir     string
	cryptoDir1 string
	cryptoDir2 string
}

func (c *crypto) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	c.resDir = t.TempDir()
	c.cryptoDir1, c.cryptoDir2 = t.TempDir(), t.TempDir()

	c.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(c.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(c.daprd),
	}
}

func (c *crypto) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	client := c.daprd.GRPCClient(t, ctx)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Empty(t, resp.GetRegisteredComponents())
		c.encryptDecryptFail(t, ctx, client, "crypto1")
		c.encryptDecryptFail(t, ctx, client, "crypto2")
	})

	t.Run("creating crypto component should make it available", func(t *testing.T) {
		pk := make([]byte, 32)
		for i := range pk {
			pk[i] = byte(i)
		}
		require.NoError(t, os.WriteFile(filepath.Join(c.cryptoDir1, "crypto1"), pk, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(c.resDir, "1.yaml"),
			[]byte(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: crypto1
spec:
  type: crypto.dapr.localstorage
  version: v1
  metadata:
    - name: path
      value: '%s'
`, c.cryptoDir1)), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 1)
		}, time.Second*5, time.Millisecond*100)

		c.encryptDecrypt(t, ctx, client, "crypto1")
		c.encryptDecryptFail(t, ctx, client, "crypto2")
		c.encryptDecryptFail(t, ctx, client, "crypto3")
	})

	t.Run("creating another crypto component should make it available", func(t *testing.T) {
		pk := make([]byte, 32)
		for i := range pk {
			pk[i] = byte(i + 32)
		}
		require.NoError(t, os.WriteFile(filepath.Join(c.cryptoDir2, "crypto2"), pk, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(c.resDir, "2.yaml"),
			[]byte(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: crypto2
spec:
  type: crypto.dapr.localstorage
  version: v1
  metadata:
    - name: path
      value: '%s'
`, c.cryptoDir2)), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*5, time.Millisecond*100)

		c.encryptDecrypt(t, ctx, client, "crypto1")
		c.encryptDecrypt(t, ctx, client, "crypto2")
		c.encryptDecryptFail(t, ctx, client, "crypto3")
	})

	t.Run("creating third crypto component should make it available", func(t *testing.T) {
		pk := make([]byte, 32)
		for i := range pk {
			pk[i] = byte(i + 32)
		}
		require.NoError(t, os.WriteFile(filepath.Join(c.cryptoDir2, "crypto3"), pk, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(c.resDir, "2.yaml"),
			[]byte(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: crypto3
spec:
  type: crypto.dapr.localstorage
  version: v1
  metadata:
    - name: path
      value: '%[1]s'
---
kind: Component
metadata:
  name: crypto2
spec:
  type: crypto.dapr.localstorage
  version: v1
  metadata:
    - name: path
      value: '%[1]s'
`, c.cryptoDir2)), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 3)
		}, time.Second*5, time.Millisecond*100)

		c.encryptDecrypt(t, ctx, client, "crypto1")
		c.encryptDecrypt(t, ctx, client, "crypto2")
		c.encryptDecrypt(t, ctx, client, "crypto3")
	})

	t.Run("deleting crypto component (through type update) should make it no longer available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(c.resDir, "2.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: crypto2
spec:
  type: state.in-memory
  version: v1
---
kind: Component
metadata:
  name: crypto3
spec:
  type: crypto.dapr.localstorage
  version: v1
  metadata:
    - name: path
      value: '%s'
`, c.cryptoDir2)), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{Name: "crypto1", Type: "crypto.dapr.localstorage", Version: "v1"},
				{Name: "crypto3", Type: "crypto.dapr.localstorage", Version: "v1"},
				{
					Name: "crypto2", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp.GetRegisteredComponents())
		}, time.Second*5, time.Millisecond*100)

		c.encryptDecrypt(t, ctx, client, "crypto1")
		c.encryptDecryptFail(t, ctx, client, "crypto2")
		c.encryptDecrypt(t, ctx, client, "crypto3")
	})

	t.Run("deleting all crypto components should make them no longer available", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(c.resDir, "1.yaml")))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*5, time.Millisecond*100)
		require.NoError(t, os.WriteFile(filepath.Join(c.resDir, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: crypto1
spec:
  type: state.in-memory
  version: v1
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{
					Name: "crypto1", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp.GetRegisteredComponents())
		}, time.Second*5, time.Millisecond*100)

		c.encryptDecryptFail(t, ctx, client, "crypto1")
		c.encryptDecryptFail(t, ctx, client, "crypto2")
		c.encryptDecryptFail(t, ctx, client, "crypto3")
	})

	t.Run("recreating crypto component should make it available again", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(c.resDir, "1.yaml"), []byte(fmt.Sprintf(`
kind: Component
metadata:
  name: crypto2
spec:
  type: crypto.dapr.localstorage
  version: v1
  metadata:
    - name: path
      value: '%s'
`, c.cryptoDir2)), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*5, time.Millisecond*100)

		c.encryptDecryptFail(t, ctx, client, "crypto1")
		c.encryptDecrypt(t, ctx, client, "crypto2")
		c.encryptDecryptFail(t, ctx, client, "crypto3")
	})
}

func (c *crypto) encryptDecryptFail(t *testing.T, ctx context.Context, client rtv1.DaprClient, name string) {
	t.Helper()

	encclient, err := client.EncryptAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, encclient.Send(&rtv1.EncryptRequest{
		Options: &rtv1.EncryptRequestOptions{
			ComponentName:    name,
			KeyName:          name,
			KeyWrapAlgorithm: "AES",
		},
		Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
	}))
	resp, err := encclient.Recv()
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "crypto providers not configured"),
	)
	require.Nil(t, resp)

	decclient, err := client.DecryptAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, decclient.Send(&rtv1.DecryptRequest{
		Options: &rtv1.DecryptRequestOptions{
			ComponentName: name,
			KeyName:       name,
		},
		Payload: &commonv1.StreamPayload{Seq: 0, Data: []byte("hello")},
	}))
	respd, err := decclient.Recv()
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "crypto providers not configured"),
	)
	require.Nil(t, respd)
}

func (c *crypto) encryptDecrypt(t *testing.T, ctx context.Context, client rtv1.DaprClient, name string) {
	t.Helper()

	encclient, err := client.EncryptAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, encclient.Send(&rtv1.EncryptRequest{
		Options: &rtv1.EncryptRequestOptions{
			ComponentName:    name,
			KeyName:          name,
			KeyWrapAlgorithm: "AES",
		},
		Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
	}))
	require.NoError(t, encclient.CloseSend())
	var encdata []byte
	for {
		var resp *rtv1.EncryptResponse
		resp, err = encclient.Recv()

		if resp != nil {
			encdata = append(encdata, resp.GetPayload().GetData()...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}

	decclient, err := client.DecryptAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, decclient.Send(&rtv1.DecryptRequest{
		Options: &rtv1.DecryptRequestOptions{
			ComponentName: name,
			KeyName:       name,
		},
		Payload: &commonv1.StreamPayload{Seq: 0, Data: encdata},
	}))
	require.NoError(t, decclient.CloseSend())
	var resp []byte
	for {
		respd, err := decclient.Recv()
		if respd != nil {
			resp = append(resp, respd.GetPayload().GetData()...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	assert.Equal(t, "hello", string(resp))
}
