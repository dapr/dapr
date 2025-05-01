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

package binding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	suite.Register(new(closing))
}

type closing struct {
	daprd   *daprd.Daprd
	binding *binding.Binding

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
	dir         string
}

func (c *closing) Setup(t *testing.T) []framework.Option {
	c.dir = t.TempDir()

	c.closeInvoke = make(chan struct{})

	app := app.New(t,
		app.WithOnBindingEventFn(func(context.Context, *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			c.inInvoke.Store(true)
			<-c.closeInvoke
			return &rtv1.BindingEventResponse{
				Data: []byte("hello world"),
			}, nil
		}),
	)

	c.binding = binding.New(t)

	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "comp.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'mybinding'
spec:
  type: bindings.%s
  version: v1
  metadata:
  - name: direction
    value: "input"
`, c.binding.SocketName())), 0o600))

	c.daprd = daprd.New(t,
		daprd.WithSocket(t, c.binding.Socket()),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(c.dir),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configun
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`),
	)

	return []framework.Option{
		framework.WithProcesses(app, c.binding, c.daprd),
	}
}

func (c *closing) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, c.daprd.GetMetaRegisteredComponents(t, ctx), 1)

	ch := c.binding.SendMessage(new(compv1.ReadResponse))

	require.Eventually(t, c.inInvoke.Load, time.Second*10, time.Millisecond*10)

	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "comp.yaml"), nil, 0o600))

	select {
	case req := <-ch:
		assert.Fail(t, "unexpected request returned", req)
	case <-time.After(time.Second * 3):
	}

	close(c.closeInvoke)

	select {
	case req := <-ch:
		assert.Equal(t, "hello world", string(req.GetResponseData()))
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	ch = c.binding.SendMessage(new(compv1.ReadResponse))
	select {
	case req := <-ch:
		assert.Empty(t, req.GetResponseData())
		assert.Equal(t, &compv1.AckResponseError{Message: "input binding is closed"}, req.GetResponseError())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	// The Binding should eventually be completely removed.
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		ch = c.binding.SendMessage(new(compv1.ReadResponse))
		select {
		case req := <-ch:
			assert.Fail(col, "unexpected request returned", req)
		case <-time.After(time.Second):
		}
	}, time.Second*10, time.Millisecond*10)
}
