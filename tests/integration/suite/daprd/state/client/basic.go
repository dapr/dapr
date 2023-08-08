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

package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	statestore "github.com/dapr/dapr/pkg/api/clients/statestore"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *procdaprd.Daprd
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)
	daprdHTTPURL := fmt.Sprintf("http://localhost:%d/", b.daprd.HTTPPort())
	statestoreName := "mystore"
	client, err := statestore.NewClient(daprdHTTPURL)
	if err != nil {
		panic(err)
	}

	t.Run("bad json", func(t *testing.T) {
		for _, body := range []string{
			"",
			"{}",
			`foobar`,
			"[{}]",
			`[{"key": "ke||y1", "value": "value1"}]`,
			`[{"key": "key1", "value": "value1"},]`,
			`[{"key": "key1", "value": "value1"},{"key": "key2", "value": "value1"},]`,
			`[{"key": "key1", "value": "value1", "etag": 123}]`,
			`[{"ey": "key0", "value": "value1"}]`,
		} {
			t.Run(body, func(t *testing.T) {
				resp, err := client.PostStoreStateWithBody(ctx, statestoreName, "application/json", strings.NewReader(body))
				require.NoError(t, err)
				assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.Contains(t, string(body), "ERR_MALFORMED_REQUEST")
			})
		}
	})

	t.Run("good json", func(t *testing.T) {
		for _, body := range []string{
			"[]",
			`[{"key": "key1", "vae": "value1"}]`,
			`[{"key": "key1", "value": "value1"}]`,
			`[{"key": "key1", "value": "value1"},{"key": "key2", "value": "value1"}]`,
			`[{"key": "key1", "value": "value1"},{"key": "key2", "value": "value1"},  {"key": "key1", "value": "value1"},{"key": "key2", "value": "value1"}]`,
		} {
			t.Run(body, func(t *testing.T) {
				resp, err := client.PostStoreStateWithBody(ctx, statestoreName, "application/json", strings.NewReader(body))
				require.NoError(t, err)
				assert.Equal(t, http.StatusNoContent, resp.StatusCode)
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.Empty(t, string(body))
			})
		}
	})
}
