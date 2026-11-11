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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

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
	suite.Register(new(missing))
}

// missing ensures that when the state store bound to an actor type is not
// loaded, daprd still starts and logs a warning naming the binding, and the
// actor type's state operations fail with ERR_ACTOR_STATE_STORE_NOT_FOUND
// without falling back to the default actor state store.
type missing struct {
	app       *actors.Actors
	defaultDB *sqlite.SQLite
	logline   *logline.LogLine

	boundStore string
	boundType  string
}

func (m *missing) Setup(t *testing.T) []framework.Option {
	m.boundStore = "wallet-transactions"
	m.boundType = "TransactionActor"

	m.defaultDB = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	m.logline = logline.New(t, logline.WithStdoutLineContains(
		"State operations of actor type "+m.boundType+" will fail: actor type "+m.boundType+" is bound to state store "+m.boundStore+", which is not loaded",
	))

	m.app = actors.New(t,
		actors.WithDB(m.defaultDB),
		actors.WithActorTypes(m.boundType),
		actors.WithActorTypeHandler(m.boundType, func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithDaprdOptions(
			daprd.WithLogLineStdout(m.logline),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: `+m.boundStore+`
      actorTypes: ["`+m.boundType+`"]
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(m.logline, m.app),
	}
}

func (m *missing) Run(t *testing.T, ctx context.Context) {
	m.app.WaitUntilRunning(t, ctx)

	// The missing store is a startup warning, not fatal: daprd is running.
	m.logline.EventuallyFoundAll(t)

	assertMessage := func(t *testing.T, msg string) {
		t.Helper()
		assert.Contains(t, msg, m.boundType)
		assert.Contains(t, msg, m.boundStore)
		assert.NotContains(t, msg, "actorStateStore")
	}

	t.Run("http", func(t *testing.T) {
		httpClient := client.HTTP(t)
		baseURL := "http://" + m.app.Daprd().HTTPAddress() + "/v1.0/actors/" + m.boundType + "/h1"

		do := func(method, path, body string) (int, string) {
			t.Helper()
			req, err := nethttp.NewRequestWithContext(ctx, method, baseURL+path, strings.NewReader(body))
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			return resp.StatusCode, string(b)
		}

		code, body := do(nethttp.MethodPost, "/method/foo", "")
		require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)

		code, body = do(nethttp.MethodPost, "/state",
			`[{"operation":"upsert","request":{"key":"k1","value":"v1"}}]`)
		assert.Equal(t, nethttp.StatusInternalServerError, code)
		assert.Contains(t, body, `"errorCode":"ERR_ACTOR_STATE_STORE_NOT_FOUND"`)
		assertMessage(t, body)

		code, body = do(nethttp.MethodGet, "/state/k1", "")
		assert.Equal(t, nethttp.StatusInternalServerError, code)
		assert.Contains(t, body, `"errorCode":"ERR_ACTOR_STATE_STORE_NOT_FOUND"`)
		assertMessage(t, body)
	})

	t.Run("grpc", func(t *testing.T) {
		gclient := m.app.GRPCClient(t, ctx)

		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: m.boundType, ActorId: "g1", Method: "foo",
		})
		require.NoError(t, err)

		_, err = gclient.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
			ActorType: m.boundType,
			ActorId:   "g1",
			Operations: []*rtv1.TransactionalActorStateOperation{{
				OperationType: "upsert",
				Key:           "k1",
				Value:         &anypb.Any{Value: []byte("v1")},
			}},
		})
		s, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, s.Code())
		assertMessage(t, s.Message())

		_, err = gclient.GetActorState(ctx, &rtv1.GetActorStateRequest{
			ActorType: m.boundType, ActorId: "g1", Key: "k1",
		})
		s, ok = status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, s.Code())
		assertMessage(t, s.Message())
	})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, m.app.Daprd().Metrics(c, ctx).MatchMetricAndSum(c, 4,
			"dapr_error_code_total", "category:actor", "error_code:ERR_ACTOR_STATE_STORE_NOT_FOUND"))
	}, 5*time.Second, 100*time.Millisecond)

	// Nothing fell back to the default actor state store.
	assert.Empty(t, m.defaultDB.StateKeys(t, ctx))
}
