/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reminders

import (
	"context"
	nethttp "net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(remote))
}

type remote struct {
	app1     *actors.Actors
	app2     *actors.Actors
	called   atomic.Int64
	holdCall chan struct{}
}

func (r *remote) Setup(t *testing.T) []framework.Option {
	r.holdCall = make(chan struct{})

	r.called.Store(0)

	r.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, req *nethttp.Request) {
			if req.Method == nethttp.MethodDelete {
				return
			}
			if r.called.Add(1) == 1 {
				return
			}
			<-r.holdCall
		}),
	)

	r.app2 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithPeerActor(r.app1),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		}),
	)

	return []framework.Option{
		framework.WithProcesses(r.app1, r.app2),
	}
}

func (r *remote) Run(t *testing.T, ctx context.Context) {
	r.app1.WaitUntilRunning(t, ctx)
	r.app2.WaitUntilRunning(t, ctx)

	client := r.app2.GRPCClient(t, ctx)

	var i atomic.Int64
	for {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(i.Add(1))),
			Method:    "foo",
		})
		require.NoError(t, err)
		if r.called.Load() == 1 {
			break
		}
	}

	_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "abc",
		ActorId:   strconv.Itoa(int(i.Load())),
		Name:      "foo1",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), r.called.Load())
	}, time.Second*10, time.Millisecond*10)

	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "abc",
		ActorId:   strconv.Itoa(int(i.Load())),
		Name:      "foo2",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	time.Sleep(time.Second)
	assert.Equal(t, int64(2), r.called.Load())
	r.holdCall <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), r.called.Load())
	}, time.Second*10, time.Millisecond*10)
	close(r.holdCall)
}
