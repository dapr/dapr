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

package idle

import (
	"context"
	"net/http"
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
	suite.Register(new(ppanic))
}

type ppanic struct {
	app1 *actors.Actors
	app2 *actors.Actors

	calls atomic.Int64
}

func (p *ppanic) Setup(t *testing.T) []framework.Option {
	p.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorIdleTimeout(time.Nanosecond),
		actors.WithActorTypeHandler("abc", func(_ http.ResponseWriter, r *http.Request) {
			p.calls.Add(1)
		}),
	)
	p.app2 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorIdleTimeout(time.Nanosecond),
		actors.WithActorTypeHandler("abc", func(_ http.ResponseWriter, r *http.Request) {
			p.calls.Add(1)
		}),
		actors.WithPeerActor(p.app1),
	)

	return []framework.Option{
		framework.WithProcesses(p.app1, p.app2),
	}
}

func (p *ppanic) Run(t *testing.T, ctx context.Context) {
	p.app1.WaitUntilRunning(t, ctx)
	p.app2.WaitUntilRunning(t, ctx)

	const n = 1000
	done := make(chan error, n)
	for i := range n {
		go func(i int) {
			_, err := p.app1.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
				ActorType: "abc",
				ActorId:   strconv.Itoa(i),
				Name:      "xxx",
				DueTime:   "0s",
			})
			done <- err
		}(i)
	}

	for range n {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(time.Second * 10):
			require.Fail(t, "timed out waiting for reminders to be registered")
			return
		case <-ctx.Done():
			require.Fail(t, "timed out waiting for reminders to be registered")
			return
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, p.calls.Load(), int64(n*2), "expected all reminders to be fired")
	}, time.Second*60, time.Millisecond*10)
}
