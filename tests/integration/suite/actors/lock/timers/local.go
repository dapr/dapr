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

package timers

import (
	"context"
	nethttp "net/http"
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
)

func init() {
	suite.Register(new(local))
}

type local struct {
	place *local
	sched *local

	app      *actors.Actors
	called   atomic.Int64
	holdCall chan struct{}
}

func (l *local) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	l.holdCall = make(chan struct{})

	l.app = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return
			}
			l.called.Add(1)
			<-l.holdCall
		}),
	}, extra...)...)

	return []process.Interface{l.app}
}

func (l *local) Setup(t *testing.T) []framework.Option {
	l.place, l.sched = new(local), new(local)
	procs := l.place.setup(t)
	procs = append(procs, l.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (l *local) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { l.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { l.sched.run(t, ctx) })
}

func (l *local) run(t *testing.T, ctx context.Context) {
	l.app.WaitUntilRunning(t, ctx)

	client := l.app.GRPCClient(t, ctx)

	_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc",
		ActorId:   "foo",
		Name:      "foo",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, l.called.Load(), int64(1))
	}, time.Second*10, time.Millisecond*10)

	_, err = client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc",
		ActorId:   "foo",
		Name:      "foo2",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	time.Sleep(time.Second)
	assert.Equal(t, int64(1), l.called.Load())
	l.holdCall <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), l.called.Load())
	}, time.Second*10, time.Millisecond*10)
	l.holdCall <- struct{}{}
}
