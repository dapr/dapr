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

package failurepolicy

import (
	"context"
	"net/http"
	"slices"
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
	suite.Register(new(notfound))
}

type notfound struct {
	place *notfound
	sched *notfound

	actors    *actors.Actors
	triggered slice.Slice[string]
}

func (n *notfound) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	n.triggered = slice.String()

	n.actors = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusNotFound)
				n.triggered.Append(r.URL.Path)
			}
		}),
	}, extra...)...)

	return []process.Interface{n.actors}
}

func (n *notfound) Setup(t *testing.T) []framework.Option {
	n.place, n.sched = new(notfound), new(notfound)
	procs := n.place.setup(t)
	procs = append(procs, n.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (n *notfound) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { n.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { n.sched.run(t, ctx) })
}

func (n *notfound) run(t *testing.T, ctx context.Context) {
	n.actors.WaitUntilRunning(t, ctx)

	_, err := n.actors.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "1234",
		Name:      "test",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c,
			slices.Repeat([]string{"/actors/foo/1234/method/remind/test"}, 4),
			n.triggered.Slice(),
		)
	}, time.Second*20, time.Millisecond*10)
}
