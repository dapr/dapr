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
	suite.Register(new(reactivate))
}

// reactivate checks eviction happens once per activation, not once per daprd
// process. An actor gets DELETE 404 then PUT on first use, idles out with
// DELETE 200, and gets DELETE 404 then PUT again when it's next called.
type reactivate struct {
	app *actors.Actors

	lock   sync.Mutex
	held   map[string]bool
	events []string
}

func (a *reactivate) Setup(t *testing.T) []framework.Option {
	a.held = make(map[string]bool)
	a.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithEvictions(),
		actors.WithHandler("/actors/abc/{id}", a.handle),
		actors.WithHandler("/actors/abc/{id}/method/{method}", a.handle),
		actors.WithActorIdleTimeout(time.Second*2),
	)

	return []framework.Option{
		framework.WithProcesses(a.app),
	}
}

func (a *reactivate) Run(t *testing.T, ctx context.Context) {
	a.app.WaitUntilRunning(t, ctx)

	for range 2 {
		a.invoke(t, ctx)
		assert.Equal(t, []string{"DELETE 404", "PUT held=false"}, a.take())
		a.invoke(t, ctx)
		assert.Equal(t, []string{"PUT held=true"}, a.take())

		var events []string
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			events = append(events, a.take()...)
			assert.Equal(c, []string{"DELETE 200"}, events)
		}, time.Second*10, time.Millisecond*10)
	}
}

func (a *reactivate) handle(w http.ResponseWriter, r *http.Request) {
	a.lock.Lock()
	defer a.lock.Unlock()

	id := r.PathValue("id")
	switch r.Method {
	case http.MethodDelete:
		if !a.held[id] {
			a.events = append(a.events, "DELETE 404")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(a.held, id)
		a.events = append(a.events, "DELETE 200")
	case http.MethodPut:
		a.events = append(a.events, "PUT held="+strconv.FormatBool(a.held[id]))
		a.held[id] = true
	}
}

func (a *reactivate) take() []string {
	a.lock.Lock()
	defer a.lock.Unlock()
	events := a.events
	a.events = nil
	return events
}

func (a *reactivate) invoke(t *testing.T, ctx context.Context) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := a.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "1",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*8, time.Millisecond*100)
}
