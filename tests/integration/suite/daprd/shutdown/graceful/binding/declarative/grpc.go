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

package declarative

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	compv1 "github.com/dapr/dapr/pkg/proto/components/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/binding"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd   *daprd.Daprd
	binding *binding.Binding

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.closeInvoke = make(chan struct{})

	app := app.New(t,
		app.WithOnBindingEventFn(func(context.Context, *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			g.inInvoke.Store(true)
			<-g.closeInvoke
			return &rtv1.BindingEventResponse{
				Data: []byte("hello world"),
			}, nil
		}),
	)

	g.binding = binding.New(t)

	g.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithSocket(t, g.binding.Socket()),
		daprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'mybinding'
spec:
  type: bindings.%s
  version: v1
  metadata:
  - name: direction
    value: "input"
`, g.binding.SocketName())),
	)

	return []framework.Option{
		framework.WithProcesses(app, g.binding),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.Run(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { g.daprd.Cleanup(t) })

	assert.Len(t, g.daprd.GetMetaRegisteredComponents(t, ctx), 1)

	ch := g.binding.SendMessage(new(compv1.ReadResponse))

	require.Eventually(t, g.inInvoke.Load, time.Second*10, time.Millisecond*10)

	go g.daprd.Cleanup(t)

	select {
	case req := <-ch:
		assert.Fail(t, "unexpected request returned", req)
	case <-time.After(time.Second * 3):
	}

	close(g.closeInvoke)

	select {
	case req := <-ch:
		assert.Equal(t, "hello world", string(req.GetResponseData()))
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	ch = g.binding.SendMessage(new(compv1.ReadResponse))
	select {
	case req := <-ch:
		assert.Empty(t, req.GetResponseData())
		assert.Equal(t, &compv1.AckResponseError{Message: "input binding is closed"}, req.GetResponseError())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}
}
