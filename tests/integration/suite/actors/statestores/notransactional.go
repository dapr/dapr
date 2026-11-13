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
	nethttp "net/http"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/status"

	contribstate "github.com/dapr/components-contrib/state"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(notransactional))
}

// notransactional ensures that a state store bound to an actor type must
// support the ETag and transactional features for every state operation,
// reads included, the same as the default actor state store. A bound store
// without transactions fails every state operation of its actor type with an
// error naming that store, rather than one about the actorStateStore
// metadata.
type notransactional struct {
	app     *actors.Actors
	store   *statestore.StateStore
	logline *logline.LogLine

	boundStore string
	boundType  string
}

func (n *notransactional) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	n.boundStore = "wallet-balance"
	n.boundType = "BalanceActor"

	// daprd warns at startup that the bound store cannot be used, with the
	// error its state operations return.
	n.logline = logline.New(t, logline.WithStdoutLineContains(
		`State operations of actor type `+n.boundType+` will fail: actors: state store \"`+n.boundStore+`\" bound to actor type \"`+n.boundType+`\" must support both the ETag and transactional features`,
	))

	sock := socket.New(t)
	n.store = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(inmemory.New(t, inmemory.WithFeatures(contribstate.FeatureETag))),
	)

	n.app = actors.New(t,
		actors.WithActorTypes(n.boundType),
		actors.WithActorTypeHandler(n.boundType, func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithResources(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: `+n.boundStore+`
spec:
  type: state.`+n.store.SocketName()+`
  version: v1
`),
		actors.WithDaprdOptions(
			daprd.WithSocket(t, sock),
			daprd.WithLogLineStdout(n.logline),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: `+n.boundStore+`
      actorTypes: ["`+n.boundType+`"]
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(n.logline, n.store, n.app),
	}
}

func (n *notransactional) Run(t *testing.T, ctx context.Context) {
	n.app.WaitUntilRunning(t, ctx)
	n.logline.EventuallyFoundAll(t)

	assertMessage := func(t *testing.T, msg string) {
		t.Helper()
		assert.Contains(t, msg, n.boundStore)
		assert.Contains(t, msg, n.boundType)
		assert.NotContains(t, msg, "actorStateStore")
	}

	t.Run("http", func(t *testing.T) {
		code, body := n.app.ActorHTTP(t, ctx, nethttp.MethodPost, n.boundType, "h1", "/method/foo", "")
		require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)

		code, body = n.app.ActorHTTP(t, ctx, nethttp.MethodGet, n.boundType, "h1", "/state/k1", "")
		assert.Equal(t, nethttp.StatusInternalServerError, code)
		assertMessage(t, body)

		code, body = n.app.SaveActorStateHTTP(t, ctx, n.boundType, "h1", "k1", "v1")
		assert.Equal(t, nethttp.StatusInternalServerError, code)
		assertMessage(t, body)
	})

	t.Run("grpc", func(t *testing.T) {
		_, err := n.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: n.boundType, ActorId: "g1", Method: "foo",
		})
		require.NoError(t, err)

		_, err = n.app.GetActorStateGRPC(t, ctx, n.boundType, "g1", "k1")
		require.Error(t, err)
		assertMessage(t, status.Convert(err).Message())

		err = n.app.SaveActorStateGRPC(t, ctx, n.boundType, "g1", "k1", "v1")
		require.Error(t, err)
		assertMessage(t, status.Convert(err).Message())
	})
}
