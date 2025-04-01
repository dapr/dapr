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
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	compv1 "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/binding"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd   *daprd.Daprd
	binding *binding.Binding

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.closeInvoke = make(chan struct{})

	app := app.New(t,
		app.WithHandlerFunc("/healthz", func(w nethttp.ResponseWriter, r *nethttp.Request) {}),
		app.WithHandlerFunc("/mybinding", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			h.inInvoke.Store(true)
			<-h.closeInvoke
			w.Write([]byte("hello world"))
		}),
	)

	h.binding = binding.New(t)

	h.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithSocket(t, h.binding.Socket()),
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
`, h.binding.SocketName())),
	)

	return []framework.Option{
		framework.WithProcesses(app, h.binding),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.Run(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { h.daprd.Cleanup(t) })

	assert.Len(t, h.daprd.GetMetaRegisteredComponents(t, ctx), 1)

	ch := h.binding.SendMessage(new(compv1.ReadResponse))

	require.Eventually(t, h.inInvoke.Load, time.Second*10, time.Millisecond*10)

	go h.daprd.Cleanup(t)

	select {
	case req := <-ch:
		assert.Fail(t, "unexpected request returned", req)
	case <-time.After(time.Second * 3):
	}

	close(h.closeInvoke)

	select {
	case req := <-ch:
		assert.Equal(t, "hello world", string(req.GetResponseData()))
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	ch = h.binding.SendMessage(new(compv1.ReadResponse))
	select {
	case req := <-ch:
		assert.Empty(t, req.GetResponseData())
		assert.Equal(t, &compv1.AckResponseError{Message: "input binding is closed"}, req.GetResponseError())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}
}
