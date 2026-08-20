/*
Copyright 2024 The Dapr Authors
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

package deactivation

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(move))
}

type move struct {
	place *move
	sched *move

	app    *actors.Actors
	called slice.Slice[string]
}

func (m *move) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	m.called = slice.String()

	m.app = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				m.called.Append(r.URL.Path)
			}
		}),
	}, extra...)...)

	return []process.Interface{m.app}
}

func (m *move) Setup(t *testing.T) []framework.Option {
	m.place, m.sched = new(move), new(move)
	procs := m.place.setup(t)
	procs = append(procs, m.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (m *move) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { m.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { m.sched.run(t, ctx) })
}

func (m *move) run(t *testing.T, ctx context.Context) {
	m.app.WaitUntilRunning(t, ctx)

	for i := range 100 {
		_, err := m.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(i),
			Method:    "foo",
		})
		require.NoError(t, err)
	}

	var newAppCalled atomic.Value
	newApp := actors.New(t,
		actors.WithPeerActor(m.app),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ http.ResponseWriter, r *http.Request) {
			newAppCalled.Store(r.URL.Path)
		}),
	)

	t.Cleanup(func() { newApp.Cleanup(t) })
	newApp.Run(t, ctx)
	newApp.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, m.called.Len())
	}, time.Second*10, time.Millisecond*10)

	act := path.Base(m.called.Slice()[0])
	_, err := m.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   act,
		Method:    "foo",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		called, _ := newAppCalled.Load().(string)
		assert.Equal(c, "/actors/abc/"+act+"/method/foo", called)
	}, time.Second*10, time.Millisecond*10)
	newApp.Cleanup(t)
}
