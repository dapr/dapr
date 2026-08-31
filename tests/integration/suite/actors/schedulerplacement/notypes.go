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

package schedulerplacement

import (
	"context"
	nethttp "net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(notypes))
}

// notypes asserts a daprd with an actor state store but no actor types
// becomes ready under scheduler placement and can invoke actors across many
// types, like a load-generator sidecar.
type notypes struct {
	host   *actors.Actors
	caller *daprd.Daprd
}

func (n *notypes) Setup(t *testing.T) []framework.Option {
	types := make([]string, 100)
	handlers := make([]actors.Option, 0, 102)
	for i := range types {
		types[i] = "actor_" + strconv.Itoa(i)
		handlers = append(handlers, actors.WithActorTypeHandler(types[i], func(nethttp.ResponseWriter, *nethttp.Request) {}))
	}
	n.host = actors.New(t, append(handlers,
		actors.WithSchedulerPlacement(),
		actors.WithActorTypes(types...),
	)...)

	n.caller = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithScheduler(n.host.Scheduler()),
	)

	return []framework.Option{
		framework.WithProcesses(n.host, n.caller),
	}
}

func (n *notypes) Run(t *testing.T, ctx context.Context) {
	n.host.WaitUntilRunning(t, ctx)
	n.caller.WaitUntilRunning(t, ctx)

	gclient := n.caller.GRPCClient(t, ctx)
	for i := range 100 {
		atype := "actor_" + strconv.Itoa(i)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: atype, ActorId: "a", Method: "foo",
			})
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*10)
	}
}
