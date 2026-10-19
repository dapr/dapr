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

package ports

import (
	"context"
	"net"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(internalgrpcshutdown))
}

// internalgrpcshutdown verifies that the shutdown API releases the internal
// gRPC port when the runtime closes its server, not when the process exits.
// A message in flight to the app keeps daprd shutting down meanwhile.
type internalgrpcshutdown struct {
	daprd    *procdaprd.Daprd
	broker   *broker.Broker
	inInvoke atomic.Bool
	release  chan struct{}
}

func (i *internalgrpcshutdown) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	i.release = make(chan struct{})

	app := app.New(t,
		app.WithHandlerFunc("/healthz", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		app.WithHandlerFunc("/a", func(nethttp.ResponseWriter, *nethttp.Request) {
			i.inInvoke.Store(true)
			<-i.release
		}),
	)

	i.broker = broker.New(t)

	i.daprd = procdaprd.New(t,
		i.broker.DaprdOptions(t, "mypub",
			procdaprd.WithAppPort(app.Port()),
			procdaprd.WithResourceFiles(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: mysub
spec:
  pubsubname: mypub
  topic: a
  routes:
    default: /a
`),
		)...,
	)

	return []framework.Option{
		framework.WithProcesses(app, i.broker),
	}
}

func (i *internalgrpcshutdown) Run(t *testing.T, ctx context.Context) {
	i.daprd.Run(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	ch := i.broker.PublishHelloWorld("a")
	require.Eventually(t, i.inInvoke.Load, time.Second*10, time.Millisecond*10)

	i.daprd.HTTPPost(t, ctx, "/v1.0/shutdown", nil, nethttp.StatusNoContent)

	addr := i.daprd.InternalGRPCAddress()
	assert.Eventuallyf(t, func() bool {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return false
		}
		return ln.Close() == nil
	}, time.Second*3, time.Millisecond*10, "internal gRPC port %s was not released while daprd shut down", addr)

	// daprd was still running: it acks the message once the app answers.
	close(i.release)
	select {
	case req := <-ch:
		assert.Nil(t, req.GetAckError())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "daprd did not ack the in-flight message")
	}

	i.daprd.WaitUntilExit(t, 15*time.Second)
}
