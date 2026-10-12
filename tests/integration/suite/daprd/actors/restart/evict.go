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
	suite.Register(new(evict))
}

// evict kills daprd while the app keeps running.
type evict struct {
	app *actors.Actors

	lock   sync.Mutex
	held   map[string]bool
	events []string
}

func (e *evict) Setup(t *testing.T) []framework.Option {
	e.held = make(map[string]bool)
	e.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithEvictions(),
		actors.WithHandler("/actors/abc/{id}", e.handle),
		actors.WithHandler("/actors/abc/{id}/method/{method}", e.handle),
	)

	return []framework.Option{
		framework.WithProcesses(e.app),
	}
}

func (e *evict) Run(t *testing.T, ctx context.Context) {
	e.app.WaitUntilRunning(t, ctx)

	e.invoke(t, ctx)
	assert.Equal(t, []string{"DELETE 404", "PUT held=false"}, e.take())
	e.invoke(t, ctx)
	assert.Equal(t, []string{"PUT held=true"}, e.take())

	e.app.Daprd().Restart(t, ctx)
	e.app.Daprd().WaitUntilRunning(t, ctx)
	e.app.Daprd().WaitUntilActorTypeHosted(t, ctx, "abc")
	assert.Empty(t, e.take())

	e.invoke(t, ctx)
	assert.Equal(t, []string{"DELETE 200", "PUT held=false"}, e.take())
	e.invoke(t, ctx)
	assert.Equal(t, []string{"PUT held=true"}, e.take())
}

// handle answers 404 for an actor it doesn't hold, like the go-sdk.
func (e *evict) handle(w http.ResponseWriter, r *http.Request) {
	e.lock.Lock()
	defer e.lock.Unlock()

	id := r.PathValue("id")
	switch r.Method {
	case http.MethodDelete:
		if !e.held[id] {
			e.events = append(e.events, "DELETE 404")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(e.held, id)
		e.events = append(e.events, "DELETE 200")
	case http.MethodPut:
		e.events = append(e.events, "PUT held="+strconv.FormatBool(e.held[id]))
		e.held[id] = true
	}
}

func (e *evict) take() []string {
	e.lock.Lock()
	defer e.lock.Unlock()
	events := e.events
	e.events = nil
	return events
}

func (e *evict) invoke(t *testing.T, ctx context.Context) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := e.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "1",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*8, time.Millisecond*100)
}
