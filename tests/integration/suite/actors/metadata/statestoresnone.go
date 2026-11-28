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
	"encoding/json"
	"io"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(statestoresnone))
}

// statestoresnone ensures that the metadata API response of an app without
// state store bindings does not change: the HTTP response has no stateStores
// field in actorRuntime, and the gRPC response has no state stores.
type statestoresnone struct {
	app *actors.Actors
}

func (s *statestoresnone) Setup(t *testing.T) []framework.Option {
	s.app = actors.New(t,
		actors.WithActorTypes("BalanceActor"),
		actors.WithActorTypeHandler("BalanceActor", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(s.app),
	}
}

func (s *statestoresnone) Run(t *testing.T, ctx context.Context) {
	s.app.WaitUntilRunning(t, ctx)

	dapr := s.app.Daprd()

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, "http://"+dapr.HTTPAddress()+"/v1.0/metadata", nil)
	require.NoError(t, err)
	resp, err := fclient.HTTP(t).Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, nethttp.StatusOK, resp.StatusCode)

	// The raw JSON has the actor runtime, without a stateStores key.
	var meta struct {
		ActorRuntime map[string]json.RawMessage `json:"actorRuntime"`
	}
	require.NoError(t, json.Unmarshal(body, &meta))
	assert.Contains(t, meta.ActorRuntime, "runtimeStatus")
	assert.NotContains(t, meta.ActorRuntime, "stateStores")
	assert.NotContains(t, string(body), "stateStores")

	gresp, err := dapr.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Empty(t, gresp.GetActorRuntime().GetStateStores())
}
