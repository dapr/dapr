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
package metadata

import (
	"context"
	nethttp "net/http"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribstate "github.com/dapr/components-contrib/state"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(statestoresunusable))
}

// statestoresunusable ensures that the metadata API reports, over HTTP and
// gRPC, a bound state store which is loaded but lacks transaction support as
// not usable, with the reason: the state operations of its actor type fail.
type statestoresunusable struct {
	app *actors.Actors
}

func (s *statestoresunusable) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	sock := socket.New(t)
	store := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(inmemory.New(t, inmemory.WithFeatures(contribstate.FeatureETag))),
	)

	s.app = actors.New(t,
		actors.WithActorTypes("BalanceActor"),
		actors.WithActorTypeHandler("BalanceActor", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithResources(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: wallet-balance
spec:
  type: state.`+store.SocketName()+`
  version: v1
`),
		actors.WithDaprdOptions(
			daprd.WithSocket(t, sock),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: statestores
spec:
  actors:
    stateStores:
    - name: wallet-balance
      actorTypes: ["BalanceActor"]
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(store, s.app),
	}
}

func (s *statestoresunusable) Run(t *testing.T, ctx context.Context) {
	s.app.WaitUntilRunning(t, ctx)

	const reason = "the state store does not support both the ETag and transactional features required for actor state"

	// The store is loaded: it is listed with the registered components.
	dapr := s.app.Daprd()
	var loaded bool
	for _, comp := range dapr.GetMetaRegisteredComponents(t, ctx) {
		loaded = loaded || comp.GetName() == "wallet-balance"
	}
	require.True(t, loaded)

	assert.Equal(t, []*daprd.MetadataActorRuntimeStateStore{
		{Name: "wallet-balance", ActorTypes: []string{"BalanceActor"}, Usable: false, Error: reason},
	}, dapr.GetMetaActorRuntime(t, ctx).StateStores)

	resp, err := dapr.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	stores := resp.GetActorRuntime().GetStateStores()
	require.Len(t, stores, 1)
	assert.Equal(t, "wallet-balance", stores[0].GetName())
	assert.Equal(t, []string{"BalanceActor"}, stores[0].GetActorTypes())
	assert.False(t, stores[0].GetUsable())
	assert.Equal(t, reason, stores[0].GetError())

	// The state operations of the actor type fail, as reported.
	code, body := s.app.ActorHTTP(t, ctx, nethttp.MethodPost, "BalanceActor", "a1", "/method/foo", "")
	require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
	code, body = s.app.SaveActorStateHTTP(t, ctx, "BalanceActor", "a1", "k", "v")
	assert.Equal(t, nethttp.StatusInternalServerError, code)
	assert.Contains(t, body, "must support both the ETag and transactional features")
}
