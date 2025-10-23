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

package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appapitoken))
}

type appapitoken struct {
	daprd *daprd.Daprd
	ch    chan metadata.MD
}

func (a *appapitoken) Setup(t *testing.T) []framework.Option {
	a.ch = make(chan metadata.MD, 1)

	app := app.New(t,
		app.WithOnBindingEventFn(func(ctx context.Context, in *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				md = metadata.MD{}
			}
			a.ch <- md
			return &rtv1.BindingEventResponse{}, nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppAPIToken(t, "test-binding-app-token"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mybinding
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
  - name: direction
    value: input
`))

	return []framework.Option{
		framework.WithProcesses(app, a.daprd),
	}
}

func (a *appapitoken) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	select {
	case md := <-a.ch:
		tokens := md.Get("dapr-api-token")
		assert.NotEmpty(t, tokens)
		assert.Equal(t, "test-binding-app-token", tokens[0])
	case <-time.After(time.Second * 10):
		assert.Fail(t, "Timed out waiting for binding event to be delivered to app")
	}
}
