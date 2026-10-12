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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(graceful))
}

// graceful checks the old daprd still deactivates on a graceful restart.
type graceful struct {
	app *actors.Actors

	lock   sync.Mutex
	held   map[string]bool
	events []string
}

func (g *graceful) Setup(t *testing.T) []framework.Option {
	g.held = make(map[string]bool)
	g.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithEvictions(),
		actors.WithHandler("/actors/abc/{id}", g.handle),
		actors.WithHandler("/actors/abc/{id}/method/{method}", g.handle),
	)

	return []framework.Option{
		framework.WithProcesses(g.app),
	}
}

func (g *graceful) Run(t *testing.T, ctx context.Context) {
	g.app.WaitUntilRunning(t, ctx)

	g.invoke(t, ctx)
	assert.Equal(t, []string{"DELETE 404", "PUT held=false"}, g.take())

	g.app.Daprd().RestartGraceful(t, ctx)
	assert.Equal(t, []string{"DELETE 200"}, g.take())
	g.app.Daprd().WaitUntilRunning(t, ctx)
	g.app.Daprd().WaitUntilActorTypeHosted(t, ctx, "abc")

	g.invoke(t, ctx)
	assert.Equal(t, []string{"DELETE 404", "PUT held=false"}, g.take())
}

func (g *graceful) handle(w http.ResponseWriter, r *http.Request) {
	g.lock.Lock()
	defer g.lock.Unlock()

	id := r.PathValue("id")
	switch r.Method {
	case http.MethodDelete:
		if !g.held[id] {
			g.events = append(g.events, "DELETE 404")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(g.held, id)
		g.events = append(g.events, "DELETE 200")
	case http.MethodPut:
		g.events = append(g.events, "PUT held="+strconv.FormatBool(g.held[id]))
		g.held[id] = true
	}
}

func (g *graceful) take() []string {
	g.lock.Lock()
	defer g.lock.Unlock()
	events := g.events
	g.events = nil
	return events
}

func (g *graceful) invoke(t *testing.T, ctx context.Context) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := g.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "1",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*8, time.Millisecond*100)
}
