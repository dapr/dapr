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

package placement

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	fakeplacement "github.com/dapr/dapr/tests/integration/framework/process/grpc/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(latefailsafe))
}

// latefailsafe ensures that a dissemination round whose unlock order arrives
// after the sidecar's 15 second unlock failsafe has already released the
// round does not permanently wedge the actor table lock: subsequent rounds
// must still unlock the table and actor calls must recover.
type latefailsafe struct {
	place *fakeplacement.Placement
	daprd *daprd.Daprd
}

func (l *latefailsafe) Setup(t *testing.T) []framework.Option {
	l.place = fakeplacement.New(t)
	srv := newActorApp(t)
	l.daprd = daprd.New(t,
		daprd.WithResourceFiles(actorStateStore),
		daprd.WithPlacementAddresses(l.place.Address(t)),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(l.place, srv, l.daprd),
	}
}

func (l *latefailsafe) Run(t *testing.T, ctx context.Context) {
	host := l.place.WaitForRegistration(t, ctx)

	// A round whose unlock is later than the sidecar's 15 second failsafe.
	l.place.SendOrder(t, ctx, "lock", nil)

	select {
	case <-time.After(time.Second * 16):
	case <-ctx.Done():
		require.Fail(t, "context canceled while waiting for the unlock failsafe")
	}

	// The late unlock for the round the failsafe already released.
	l.place.SendOrder(t, ctx, "unlock", nil)

	// The next round must still lock and unlock the table.
	l.place.SendOrder(t, ctx, "lock", nil)
	l.place.SendOrder(t, ctx, "update", fakeplacement.TablesWithHost(host, "2", "myactortype"))
	l.place.SendOrder(t, ctx, "unlock", nil)

	l.daprd.WaitUntilRunning(t, ctx)

	client := l.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		ictx, cancel := context.WithTimeout(ctx, time.Second*2)
		defer cancel()
		_, err := client.InvokeActor(ictx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "1",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*100)
}
