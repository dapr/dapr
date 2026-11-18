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

package statestores

import (
	"context"
	"io"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nodefault))
}

// nodefault ensures that a bound state store does not replace the default
// actor state store: with only a bound store loaded, actor hosting stays
// disabled, so the bound actor type is not hosted, calls to it fail, and
// nothing is written to its store.
type nodefault struct {
	app     *actors.Actors
	txDB    *sqlite.SQLite
	logline *logline.LogLine

	boundType string
}

func (n *nodefault) Setup(t *testing.T) []framework.Option {
	n.boundType = "PaymentActor"

	// The only state store is the bound one, so there is no default actor
	// state store.
	n.txDB = sqlite.New(t,
		sqlite.WithName("wallet-transactions"),
		sqlite.WithActorStateStore(false),
		sqlite.WithCreateStateTables(),
	)

	n.logline = logline.New(t, logline.WithStdoutLineContains(
		"Actor state store not configured - actor hosting disabled",
	))

	n.app = actors.New(t,
		actors.WithDB(n.txDB),
		actors.WithActorTypes(n.boundType),
		actors.WithActorTypeHandler(n.boundType, func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithDaprdOptions(
			daprd.WithLogLineStdout(n.logline),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: wallet-transactions
      actorTypes: ["`+n.boundType+`"]
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(n.logline, n.app),
	}
}

func (n *nodefault) Run(t *testing.T, ctx context.Context) {
	n.app.Daprd().WaitUntilRunning(t, ctx)
	n.logline.EventuallyFoundAll(t)

	// The bound type is registered by the app but not advertised to
	// placement, so no host is found for its actors.
	notHosted := "did not find address for actor '" + n.boundType + "/"

	t.Run("http", func(t *testing.T) {
		url := "http://" + n.app.Daprd().HTTPAddress() + "/v1.0/actors/" + n.boundType + "/h1/method/foo"
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, nethttp.StatusInternalServerError, resp.StatusCode)
		assert.Contains(t, string(body), notHosted)
	})

	t.Run("grpc", func(t *testing.T) {
		_, err := n.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: n.boundType, ActorId: "g1", Method: "foo",
		})
		require.Error(t, err)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
		assert.Contains(t, err.Error(), notHosted)
	})

	assert.Empty(t, n.txDB.StateKeys(t, ctx))
}
