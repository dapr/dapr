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

package deactivation

import (
	"context"
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(onexit))
}

type onexit struct {
	app       *actors.Actors
	calledABC atomic.Int64
	calledDEF atomic.Bool
	calledXYZ atomic.Bool
}

func (o *onexit) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows as is not compatible")
	}

	o.calledABC.Store(0)
	o.calledDEF.Store(false)
	o.calledXYZ.Store(false)

	t.Cleanup(func() {
		assert.Equal(t, int64(2), o.calledABC.Load())
		assert.True(t, o.calledDEF.Load())
		assert.False(t, o.calledXYZ.Load())
	})

	o.app = actors.New(t,
		actors.WithActorTypes("abc", "def", "xyz"),
		actors.WithActorTypeHandler("abc", func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				o.calledABC.Add(1)
				return
			}
		}),
		actors.WithActorTypeHandler("def", func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				o.calledDEF.Store(true)
				return
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(o.app),
	}
}

func (o *onexit) Run(t *testing.T, ctx context.Context) {
	o.app.WaitUntilRunning(t, ctx)

	_, err := o.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "123",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = o.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "456",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = o.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "def",
		ActorId:   "123",
		Method:    "foo",
	})
	require.NoError(t, err)
}
