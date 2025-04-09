/*
Copyright 2025 The Dapr Authors
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

package input

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/binding"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(pluggable))
}

type pluggable struct {
	daprd   *daprd.Daprd
	binding *binding.Binding

	bindingCalled atomic.Int64
}

func (p *pluggable) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithOnBindingEventFn(func(context.Context, *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			p.bindingCalled.Add(1)
			return &rtv1.BindingEventResponse{
				Data: []byte("world hello"),
			}, nil
		}),
	)

	p.binding = binding.New(t)

	p.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithSocket(t, p.binding.Socket()),
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
`, p.binding.SocketName())),
	)

	return []framework.Option{
		framework.WithProcesses(app, p.binding, p.daprd),
	}
}

func (p *pluggable) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)

	require.Len(t, p.daprd.GetMetaRegisteredComponents(t, ctx), 1)

	resp := <-p.binding.SendMessage(&compv1pb.ReadResponse{
		Data: []byte("hello world"),
	})
	assert.Equal(t, "world hello", string(resp.GetResponseData()))
	assert.Nil(t, resp.GetResponseError())
	assert.Equal(t, int64(1), p.bindingCalled.Load())
}
