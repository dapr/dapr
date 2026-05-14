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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hostGRPCStreamRejectedOnHTTP))
}

// hostGRPCStreamRejectedOnHTTP asserts the guard in
// pkg/api/grpc/actorcallbacks.go: when daprd's app channel is HTTP,
// opening the gRPC SubscribeActorEventsAlpha1 stream must fail with
// FailedPrecondition. This prevents the "RegisterHosted called twice"
// scenario (once via HTTP /dapr/config, once via the stream) — the HTTP
// path is the canonical source of actor types and the stream is rejected
// before any second RegisterHosted runs.
type hostGRPCStreamRejectedOnHTTP struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (m *hostGRPCStreamRejectedOnHTTP) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	m.place = placement.New(t)
	m.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(m.place, srv, m.daprd),
	}
}

func (m *hostGRPCStreamRejectedOnHTTP) Run(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	// First confirm the HTTP-registered actor type made it through, so we
	// know the daprd is healthy and serving actors via the HTTP path.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		res := m.daprd.GetMetadata(c, ctx)
		assert.True(c, res.ActorRuntime.HostReady)
		assert.ElementsMatch(c, []*daprd.MetadataActorRuntimeActiveActor{
			{Type: "myactortype"},
		}, res.ActorRuntime.ActiveActors)
	}, 10*time.Second, 50*time.Millisecond, "HTTP-registered actor type never showed up")

	conn, err := grpc.NewClient(m.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := rtv1.NewDaprClient(conn)

	stream, err := client.SubscribeActorEventsAlpha1(ctx)
	require.NoError(t, err, "SubscribeActorEventsAlpha1 RPC initiation must succeed (error surfaces on the first Recv)")
	require.NoError(t, stream.Send(&rtv1.SubscribeActorEventsRequestAlpha1{
		RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities: []string{"actorViaStream"},
			},
		},
	}))

	// The server-side guard rejects the call before reading the initial
	// message; the client surfaces it on Recv with FailedPrecondition.
	_, err = stream.Recv()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected a gRPC status error, got %T: %v", err, err)
	assert.Equal(t, codes.FailedPrecondition, st.Code(),
		"daprd must reject SubscribeActorEventsAlpha1 when the app channel is HTTP")

	// Sanity: the HTTP-registered actor type is still the only one daprd
	// knows about — the rejected stream did not call RegisterHosted.
	res := m.daprd.GetMetadata(t, ctx)
	assert.ElementsMatch(t, []*daprd.MetadataActorRuntimeActiveActor{
		{Type: "myactortype"},
	}, res.ActorRuntime.ActiveActors)
}
