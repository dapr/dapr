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

package restart

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reactivate))
}

// reactivate checks the activation id is per activation, not per daprd process.
type reactivate struct {
	app *actors.Actors

	rec activation.Recorder
}

func (a *reactivate) Setup(t *testing.T) []framework.Option {
	a.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(http.ResponseWriter, *http.Request) {
			a.rec.Record("DELETE", "")
		}),
		actors.WithHandler("/actors/abc/{id}/method/{method}", func(_ http.ResponseWriter, r *http.Request) {
			a.rec.Record("invoke", activation.FromHeader(r))
		}),
		actors.WithActorIdleTimeout(time.Second*2),
	)

	return []framework.Option{
		framework.WithProcesses(a.app),
	}
}

func (a *reactivate) Run(t *testing.T, ctx context.Context) {
	a.app.WaitUntilRunning(t, ctx)

	foo := &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: "1", Method: "foo"}
	client := a.app.GRPCClient(t, ctx)
	seen := make(map[string]bool)
	for range 2 {
		activation.Invoke(t, ctx, client, foo)
		activation.Invoke(t, ctx, client, foo)
		calls := a.rec.Take()
		require.GreaterOrEqual(t, len(calls), 2)
		require.NotEmpty(t, calls[0].ID)
		for _, e := range calls {
			assert.Equal(t, calls[0].ID, e.ID)
		}
		assert.False(t, seen[calls[0].ID])
		seen[calls[0].ID] = true

		var events []activation.Event
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			events = append(events, a.rec.Take()...)
			assert.Equal(c, []activation.Event{{Kind: "DELETE"}}, events)
		}, time.Second*8, time.Millisecond*10)
	}
}
