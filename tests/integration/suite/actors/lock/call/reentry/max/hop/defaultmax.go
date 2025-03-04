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

package hop

import (
	"context"
	nethttp "net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(defaultmax))
}

type defaultmax struct {
	app1     *actors.Actors
	app2     *actors.Actors
	called   slice.Slice[string]
	rid      atomic.Pointer[string]
	holdCall chan struct{}
}

func (d *defaultmax) Setup(t *testing.T) []framework.Option {
	d.called = slice.New[string]()
	d.holdCall = make(chan struct{})
	d.rid = atomic.Pointer[string]{}

	handler := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method == nethttp.MethodDelete {
			return
		}
		if d.rid.Load() == nil {
			d.rid.Store(ptr.Of(r.Header.Get("Dapr-Reentrancy-Id")))
		}
		d.called.Append(r.URL.Path)
		<-d.holdCall
	}

	d.app1 = actors.New(t,
		actors.WithActorTypes("abc", "efg"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithActorTypeHandler("efg", handler),
		actors.WithReentry(true),
	)

	d.app2 = actors.New(t,
		actors.WithActorTypes("abc", "efg"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithActorTypeHandler("efg", handler),
		actors.WithPeerActor(d.app1),
		actors.WithReentry(true),
	)

	return []framework.Option{
		framework.WithProcesses(d.app1, d.app2),
	}
}

func (d *defaultmax) Run(t *testing.T, ctx context.Context) {
	d.app1.WaitUntilRunning(t, ctx)
	d.app2.WaitUntilRunning(t, ctx)

	client := d.app1.GRPCClient(t, ctx)

	errCh := make(chan error)
	go func() {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "123",
			Method:    "foo",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			"/actors/abc/123/method/foo",
		}, d.called.Slice())
	}, time.Second*10, time.Millisecond*10)

	require.NotNil(t, d.rid.Load())
	id := *(d.rid.Load())

	for i := range 31 {
		go func() {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "abc",
				ActorId:   "123",
				Method:    "foo",
				Metadata:  map[string]string{"Dapr-Reentrancy-Id": id},
			})
			errCh <- err
		}()

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, (i*2)+2, d.called.Len())
		}, time.Second*10, time.Millisecond*10)

		atype := "abc"
		if i%2 == 0 {
			atype = "efg"
		}
		go func() {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: atype,
				ActorId:   strconv.Itoa(i),
				Method:    "foo",
				Metadata:  map[string]string{"Dapr-Reentrancy-Id": id},
			})
			errCh <- err
		}()

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, (i*2)+3, d.called.Len())
		}, time.Second*10, time.Millisecond*10)
	}

	_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "123",
		Method:    "foo",
		Metadata:  map[string]string{"Dapr-Reentrancy-Id": id},
	})
	require.Error(t, err)
	status, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted.String(), status.Code().String())

	for range 63 {
		d.holdCall <- struct{}{}
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout")
		}
	}
}
