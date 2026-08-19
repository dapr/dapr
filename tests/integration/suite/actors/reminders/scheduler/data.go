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

package scheduler

import (
	"context"
	"io"
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
)

func init() {
	suite.Register(new(data))
}

type data struct {
	place *data
	sched *data

	actors *actors.Actors
	got    chan string
}

func (d *data) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	d.got = make(chan string, 1)
	d.actors = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(_ http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodDelete {
				return
			}
			got, err := io.ReadAll(req.Body)
			assert.NoError(t, err)
			d.got <- string(got)
		}),
	}, extra...)...)

	return []process.Interface{d.actors}
}

func (d *data) Setup(t *testing.T) []framework.Option {
	d.place, d.sched = new(data), new(data)
	procs := d.place.setup(t)
	procs = append(procs, d.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (d *data) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { d.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { d.sched.run(t, ctx) })
}

func (d *data) run(t *testing.T, ctx context.Context) {
	d.actors.WaitUntilRunning(t, ctx)

	_, err := d.actors.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "1234",
		Name:      "helloworld",
		DueTime:   "0s",
		Period:    "1000s",
		Ttl:       "2000s",
		Data:      []byte("mydata"),
	})
	require.NoError(t, err)

	select {
	case got := <-d.got:
		assert.JSONEq(t, `{"data":"bXlkYXRh","dueTime":"","period":""}`, got)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for reminder")
	}
}
