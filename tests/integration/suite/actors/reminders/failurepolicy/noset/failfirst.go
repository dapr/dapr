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

package noset

import (
	"context"
	"net/http"
	"path"
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
	suite.Register(new(failfirst))
}

type failfirst struct {
	place *failfirst
	sched *failfirst

	actors    *actors.Actors
	triggered slice.Slice[string]
	respErr   atomic.Bool
}

func (f *failfirst) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	f.triggered = slice.String()
	f.respErr.Store(true)

	f.actors = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("helloworld"),
		actors.WithActorTypeHandler("helloworld", func(w http.ResponseWriter, req *http.Request) {
			defer f.triggered.Append(path.Base(req.URL.Path))
			if f.respErr.Load() {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}),
	}, extra...)...)

	return []process.Interface{f.actors}
}

func (f *failfirst) Setup(t *testing.T) []framework.Option {
	f.place, f.sched = new(failfirst), new(failfirst)
	procs := f.place.setup(t)
	procs = append(procs, f.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (f *failfirst) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { f.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { f.sched.run(t, ctx) })
}

func (f *failfirst) run(t *testing.T, ctx context.Context) {
	f.actors.WaitUntilRunning(t, ctx)

	_, err := f.actors.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "helloworld",
		ActorId:   "1234",
		Name:      "test",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, f.triggered.Len(), 1)
	}, time.Second*10, time.Millisecond*10)

	f.respErr.Store(false)
	count := f.triggered.Len()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, f.triggered.Len(), count+1)
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, f.triggered.Len(), count+1)
}
