/*
Copyright 2025 The Dapr Authors
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

package actors

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(withactors))
}

type withactors struct {
	actors *actors.Actors
}

func (w *withactors) Setup(t *testing.T) []framework.Option {
	w.actors = actors.New(t,
		actors.WithActorTypes("abc", "xyz"),
		actors.WithActorTypeHandler("abc", func(http.ResponseWriter, *http.Request) {}),
		actors.WithActorTypeHandler("xyz", func(http.ResponseWriter, *http.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(w.actors),
	}
}

func (w *withactors) Run(t *testing.T, ctx context.Context) {
	w.actors.WaitUntilRunning(t, ctx)

	exp := &rtv1.ActorRuntime{
		RuntimeStatus: rtv1.ActorRuntime_RUNNING,
		HostReady:     true,
		Placement:     "placement: connected",
		ActiveActors: []*rtv1.ActiveActorsCount{
			{Type: "abc", Count: 0},
			{Type: "xyz", Count: 0},
		},
	}

	client := w.actors.GRPCClient(t, ctx)

	resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)

	assert.Equal(t, exp.GetRuntimeStatus(), resp.GetActorRuntime().GetRuntimeStatus())
	assert.Equal(t, exp.GetHostReady(), resp.GetActorRuntime().GetHostReady())
	assert.Equal(t, exp.GetPlacement(), resp.GetActorRuntime().GetPlacement())
	assert.ElementsMatch(t, exp.GetActiveActors(), resp.GetActorRuntime().GetActiveActors())

	respHTTP := w.actors.Daprd().GetMetaActorRuntime(t, ctx)
	assert.Equal(t, "RUNNING", respHTTP.RuntimeStatus)
	assert.True(t, respHTTP.HostReady)
	assert.Equal(t, "placement: connected", respHTTP.Placement)
	assert.ElementsMatch(t, []*daprd.MetadataActorRuntimeActiveActor{
		{Type: "abc", Count: 0},
		{Type: "xyz", Count: 0},
	}, respHTTP.ActiveActors)

	_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "1",
		Method:    "set",
	})
	require.NoError(t, err)
	_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "2",
		Method:    "set",
	})
	require.NoError(t, err)
	_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "xyz",
		ActorId:   "1",
		Method:    "set",
	})
	require.NoError(t, err)

	exp.ActiveActors = []*rtv1.ActiveActorsCount{
		{Type: "abc", Count: 2},
		{Type: "xyz", Count: 1},
	}
	resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)

	assert.Equal(t, exp.GetRuntimeStatus(), resp.GetActorRuntime().GetRuntimeStatus())
	assert.Equal(t, exp.GetHostReady(), resp.GetActorRuntime().GetHostReady())
	assert.Equal(t, exp.GetPlacement(), resp.GetActorRuntime().GetPlacement())
	assert.ElementsMatch(t, exp.GetActiveActors(), resp.GetActorRuntime().GetActiveActors())

	respHTTP = w.actors.Daprd().GetMetaActorRuntime(t, ctx)
	assert.Equal(t, "RUNNING", respHTTP.RuntimeStatus)
	assert.True(t, respHTTP.HostReady)
	assert.Equal(t, "placement: connected", respHTTP.Placement)
	assert.ElementsMatch(t, []*daprd.MetadataActorRuntimeActiveActor{
		{Type: "abc", Count: 2},
		{Type: "xyz", Count: 1},
	}, respHTTP.ActiveActors)
}
