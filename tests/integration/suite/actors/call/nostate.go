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

package call

import (
	"context"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nostate))
}

type nostate struct {
	place *nostate
	sched *nostate

	actors *actors.Actors
	daprd  *daprd.Daprd
}

func (n *nostate) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	n.actors = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	}, extra...)...)

	if n.actors.Placement() != nil {
		n.daprd = daprd.New(t,
			daprd.WithPlacementAddresses(n.actors.Placement().Address()),
		)
	} else {
		n.daprd = daprd.New(t,
			daprd.WithScheduler(n.actors.Scheduler()),
		)
	}

	return []process.Interface{n.actors, n.daprd}
}

func (n *nostate) Setup(t *testing.T) []framework.Option {
	n.place, n.sched = new(nostate), new(nostate)
	procs := n.place.setup(t)
	procs = append(procs, n.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (n *nostate) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { n.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { n.sched.run(t, ctx) })
}

func (n *nostate) run(t *testing.T, ctx context.Context) {
	n.actors.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	client := n.daprd.GRPCClient(t, ctx)

	_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "1",
		Method:    "foo",
	})
	require.NoError(t, err)
}
