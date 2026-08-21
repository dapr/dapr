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

package idle

import (
	"context"
	"net/http"
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
	suite.Register(new(pertype))
}

type pertype struct {
	place *pertype
	sched *pertype

	app    *actors.Actors
	called slice.Slice[string]
}

func (p *pertype) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	p.called = slice.String()

	p.app = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("abc", "def", "xyz"),
		actors.WithActorIdleTimeout(1 * time.Second),
		actors.WithEntityConfig(
			actors.WithEntityConfigEntities("abc"),
			actors.WithEntityConfigActorIdleTimeout(4*time.Second),
		),
		actors.WithActorTypeHandler("abc", func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				p.called.Append(r.URL.Path)
				return
			}
		}),
		actors.WithActorTypeHandler("def", func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				p.called.Append(r.URL.Path)
				return
			}
		}),
	}, extra...)...)

	return []process.Interface{p.app}
}

func (p *pertype) Setup(t *testing.T) []framework.Option {
	p.place, p.sched = new(pertype), new(pertype)
	procs := p.place.setup(t)
	procs = append(procs, p.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (p *pertype) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { p.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { p.sched.run(t, ctx) })
}

func (p *pertype) run(t *testing.T, ctx context.Context) {
	p.app.WaitUntilRunning(t, ctx)

	_, err := p.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "123",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = p.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "def",
		ActorId:   "456",
		Method:    "foo",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"/actors/def/456"}, p.called.Slice())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second)
	assert.ElementsMatch(t, []string{"/actors/def/456"}, p.called.Slice())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{
			"/actors/abc/123",
			"/actors/def/456",
		}, p.called.Slice())
	}, time.Second*10, time.Millisecond*10)
}
