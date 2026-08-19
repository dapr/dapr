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
	suite.Register(new(unlockfirst))
}

// unlockfirst ensures that an unlock order received before any lock order,
// as happens when a sidecar joins a placement stream mid dissemination
// round, does not poison the sidecar's lock/unlock pairing for future
// rounds.
type unlockfirst struct {
	place *fakeplacement.Placement
	daprd *daprd.Daprd
}

func (u *unlockfirst) Setup(t *testing.T) []framework.Option {
	u.place = fakeplacement.New(t)
	srv := newActorApp(t)
	u.daprd = daprd.New(t,
		daprd.WithResourceFiles(actorStateStore),
		daprd.WithPlacementAddresses(u.place.Address(t)),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(u.place, srv, u.daprd),
	}
}

func (u *unlockfirst) Run(t *testing.T, ctx context.Context) {
	host := u.place.WaitForRegistration(t, ctx)

	// Stray unlock with no paired lock.
	u.place.SendOrder(t, ctx, "unlock", nil)

	// A normal dissemination round must still lock and unlock the table.
	u.place.SendOrder(t, ctx, "lock", nil)
	u.place.SendOrder(t, ctx, "update", fakeplacement.TablesWithHost(host, "1", "myactortype"))
	u.place.SendOrder(t, ctx, "unlock", nil)

	u.daprd.WaitUntilRunning(t, ctx)

	client := u.daprd.GRPCClient(t, ctx)
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
