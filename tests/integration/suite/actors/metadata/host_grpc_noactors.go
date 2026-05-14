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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hostGRPCNoActors))
}

// hostGRPCNoActors is the non-actor-host regression guard: a gRPC app
// that opens the SubscribeActorEventsAlpha1 stream but advertises an
// empty Entities list. Daprd must register the stream, recognise there
// are no hosted types, and still transition the actor runtime out of
// INITIALIZING to RUNNING / HostReady — matching the HTTP variant where
// /dapr/config returns an empty config.
type hostGRPCNoActors struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (m *hostGRPCNoActors) Setup(t *testing.T) []framework.Option {
	// The app still opens the SubscribeActorEventsAlpha1 stream but
	// registers with no entities, signalling it hosts no actors. This is
	// how a gRPC actor-client app (non-host) behaves under the streaming
	// protocol.
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return m.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{}
		}),
	)

	m.place = placement.New(t)
	m.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(m.place, m.daprd, srv),
	}
}

func (m *hostGRPCNoActors) Run(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilTCPReady(t, ctx)

	// Daprd must reach RUNNING with no hosted actors and no crash — an
	// empty Entities registration is treated exactly like an app that
	// doesn't host any actor types.
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		res := m.daprd.GetMetadata(t, ctx)
		assert.Equal(t, "RUNNING", res.ActorRuntime.RuntimeStatus)
		assert.True(t, res.ActorRuntime.HostReady)
		assert.Empty(t, res.ActorRuntime.ActiveActors)
	}, 10*time.Second, 10*time.Millisecond)
}
