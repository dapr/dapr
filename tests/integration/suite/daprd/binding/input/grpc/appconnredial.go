/*
Copyright 2026 The Dapr Authors
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
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/listener"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appconnredial))
}

type appconnredial struct {
	daprd    *daprd.Daprd
	listener *listener.Stall
	logs     *log.Log
	eventCh  chan struct{}
}

func (a *appconnredial) Setup(t *testing.T) []framework.Option {
	a.eventCh = make(chan struct{}, 100)
	a.logs = log.New()
	a.listener = listener.New(ports.Reserve(t, 1).Listener(t))

	srv := app.New(t,
		app.WithGRPCOptions(procgrpc.WithListener(func() (net.Listener, error) {
			return a.listener, nil
		})),
		app.WithOnBindingEventFn(func(context.Context, *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			select {
			case a.eventCh <- struct{}{}:
			default:
			}
			return new(rtv1.BindingEventResponse), nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithExecOptions(exec.WithStdout(a.logs), exec.WithStderr(a.logs)),
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
		framework.WithProcesses(srv, a.daprd),
	}
}

func (a *appconnredial) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	waitForEvent := func(t require.TestingT, timeout time.Duration) {
		select {
		case <-a.eventCh:
		case <-time.After(timeout):
			require.Fail(t, "timed out waiting for a binding event")
		}
	}

	waitForEvent(t, time.Second*10)

	a.listener.SetStall(time.Second * 2)
	a.listener.CloseAccepted()

	for len(a.eventCh) > 0 {
		<-a.eventCh
	}

	waitForEvent(t, time.Second*20)

	assert.False(t, a.logs.Contains("error reading server preface"),
		"app connection was closed mid handshake")
}
