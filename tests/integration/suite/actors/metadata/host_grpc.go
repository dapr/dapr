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
	suite.Register(new(hostGRPC))
}

// hostGRPC mirrors host but exercises actor-type registration over the
// streaming SubscribeActorEventsAlpha1 protocol: the app dials daprd,
// opens the stream, and sends the initial registration message. Daprd's
// API handler calls RegisterHosted in response, adding the hosted actor
// type to the runtime.
//
// The blockConfig channel gates the initial stream message so the test
// can observe ActiveActors before vs. after the registration arrives.
// RuntimeStatus itself transitions to RUNNING as soon as the app is
// healthy (matching the HTTP semantic), so the gate is asserted via
// ActiveActors presence.
type hostGRPC struct {
	daprd       *daprd.Daprd
	place       *placement.Placement
	blockConfig chan struct{}
}

func (m *hostGRPC) Setup(t *testing.T) []framework.Option {
	m.blockConfig = make(chan struct{})

	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return m.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			// Block until the test signals the registration can proceed.
			// WithActorRegistration is invoked synchronously on the stream
			// dial path in the harness, so this gates the initial hello.
			<-m.blockConfig
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities: []string{"myactortype"},
			}
		}),
	)

	m.place = placement.New(t)
	m.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(m.place, m.daprd, srv),
	}
}

func (m *hostGRPC) Run(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	// Before the stream's initial registration is sent, the app advertises
	// no actor types so ActiveActors is empty. RuntimeStatus is already
	// RUNNING because the empty synchronous RegisterHosted in
	// appHealthChanged releases the placement gate — matching the HTTP
	// semantic where /dapr/config returning empty still transitions out
	// of INITIALIZING.
	res := m.daprd.GetMetadata(t, ctx)
	assert.Empty(t, res.ActorRuntime.ActiveActors)

	// Unblock the gRPC registration message.
	close(m.blockConfig)

	// After the stream registers, daprd reports the advertised actor type.
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		res := m.daprd.GetMetadata(t, ctx)
		assert.Equal(t, "RUNNING", res.ActorRuntime.RuntimeStatus)
		assert.True(t, res.ActorRuntime.HostReady)
		assert.Equal(t, "placement: connected", res.ActorRuntime.Placement)
		assert.ElementsMatch(t, []*daprd.MetadataActorRuntimeActiveActor{
			{Type: "myactortype"},
		}, res.ActorRuntime.ActiveActors)
	}, 10*time.Second, 10*time.Millisecond)
}
