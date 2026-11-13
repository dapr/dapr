/*
Copyright 2026 The Dapr Authors
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

package selfhosted

import (
	"context"
	"io"
	"net/http"
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
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actorstatefalsy))
}

// actorstatefalsy ensures that a state store whose actorStateStore metadata
// value is false is hot reloaded as a plain state store while an actor state
// store is loaded, instead of being skipped as a second actor state store.
type actorstatefalsy struct {
	daprd  *daprd.Daprd
	resDir string
}

func (a *actorstatefalsy) Setup(t *testing.T) []framework.Option {
	place := placement.New(t)

	a.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"), []byte(actorStateStoreComp), 0o600))

	a.daprd = daprd.New(t,
		daprd.WithResourcesDir(a.resDir),
		daprd.WithPlacementAddresses(place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(place, a.daprd),
	}
}

func (a *actorstatefalsy) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "plainstore.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: plainstore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorStateStore
   value: "false"
`), 0o600))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		comps := a.daprd.GetMetaRegisteredComponents(c, ctx)
		names := make([]string, 0, len(comps))
		for _, comp := range comps {
			names = append(names, comp.GetName())
		}
		assert.Contains(c, names, "plainstore")
	}, time.Second*20, time.Millisecond*10)

	t.Run("http", func(t *testing.T) {
		httpClient := client.HTTP(t)
		url := "http://" + a.daprd.HTTPAddress() + "/v1.0/state/plainstore"

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
			strings.NewReader(`[{"key":"hkey","value":"hvalue"}]`))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusNoContent, resp.StatusCode)

		req, err = http.NewRequestWithContext(ctx, http.MethodGet, url+"/hkey", nil)
		require.NoError(t, err)
		resp, err = httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.JSONEq(t, `"hvalue"`, string(body))
	})

	t.Run("grpc", func(t *testing.T) {
		gclient := a.daprd.GRPCClient(t, ctx)

		_, err := gclient.SaveState(ctx, &rtv1.SaveStateRequest{
			StoreName: "plainstore",
			States:    []*commonv1.StateItem{{Key: "gkey", Value: []byte("gvalue")}},
		})
		require.NoError(t, err)

		resp, err := gclient.GetState(ctx, &rtv1.GetStateRequest{
			StoreName: "plainstore",
			Key:       "gkey",
		})
		require.NoError(t, err)
		assert.Equal(t, "gvalue", string(resp.GetData()))
	})
}
