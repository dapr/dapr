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

package broker

import (
	"context"
	"fmt"
	"testing"

	compv1 "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub"
	inmemory "github.com/dapr/dapr/tests/integration/framework/process/pubsub/in-memory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
)

type Broker struct {
	pmrReqCh  chan *compv1.PullMessagesRequest
	pmrRespCh chan *compv1.PullMessagesResponse

	inmem  *pubsub.PubSub
	socket *socket.Socket
}

// Option configures broker creation.
type Option func(*brokerOptions)

type brokerOptions struct {
	pausable bool
}

// WithPausable opts the pluggable server in to the Pause/Resume RPCs.
// Default is non-pausable (returns codes.Unimplemented), matching the
// majority of pubsub components.
func WithPausable() Option {
	return func(o *brokerOptions) {
		o.pausable = true
	}
}

func New(t *testing.T, opts ...Option) *Broker {
	bo := new(brokerOptions)
	for _, fn := range opts {
		fn(bo)
	}

	pmrReqCh := make(chan *compv1.PullMessagesRequest, 1)
	pmrRespCh := make(chan *compv1.PullMessagesResponse, 1)

	socket := socket.New(t)
	psOpts := []pubsub.Option{
		pubsub.WithSocket(socket),
		pubsub.WithPullMessagesChannel(pmrReqCh, pmrRespCh),
		pubsub.WithPubSub(inmemory.NewWrappedInMemory(t)),
	}
	if bo.pausable {
		psOpts = append(psOpts, pubsub.WithPausable())
	}
	inmem := pubsub.New(t, psOpts...)

	return &Broker{
		pmrReqCh:  pmrReqCh,
		pmrRespCh: pmrRespCh,
		inmem:     inmem,
		socket:    socket,
	}
}

func (b *Broker) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	b.inmem.Run(t, ctx)
}

func (b *Broker) Cleanup(t *testing.T) {
	t.Helper()
	b.inmem.Cleanup(t)
}

func (b *Broker) DaprdOptions(t *testing.T, componentName string, opts ...daprd.Option) []daprd.Option {
	return append(opts,
		daprd.WithSocket(t, b.socket),
		daprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: %s
spec:
  type: pubsub.%s
  version: v1
`,
			componentName,
			b.inmem.SocketName())),
	)
}

func (b *Broker) Socket() *socket.Socket {
	return b.socket
}

func (b *Broker) PubSub() *pubsub.PubSub {
	return b.inmem
}

func (b *Broker) PublishHelloWorld(topic string) <-chan *compv1.PullMessagesRequest {
	b.pmrRespCh <- &compv1.PullMessagesResponse{
		Data:      []byte(`{"data":{"foo": "helloworld"},"datacontenttype":"application/json","id":"b959cd5a-29e5-42ca-89e2-c66f4402f273","pubsubname":"mypub","source":"foo","specversion":"1.0","time":"2024-03-27T23:47:53Z","topic":"a","traceid":"00-00000000000000000000000000000000-0000000000000000-00","traceparent":"00-00000000000000000000000000000000-0000000000000000-00","tracestate":"","type":"com.dapr.event.sent"}`),
		Id:        "foo",
		TopicName: topic,
	}

	return b.pmrReqCh
}

// PauseCalled reports how many times the runtime invoked Pause on the
// pluggable server. Used by tests asserting the pause-and-drain shutdown
// path was exercised.
func (b *Broker) PauseCalled() int64 {
	return b.inmem.PauseCalled()
}

// IsPaused reports whether the pluggable server is currently in the
// paused state.
func (b *Broker) IsPaused() bool {
	return b.inmem.IsPaused()
}

// PauseStarted returns a channel that is closed the first time Pause is
// called. Tests use this to wait for the runtime's shutdown path to
// invoke Pause without polling.
func (b *Broker) PauseStarted() <-chan struct{} {
	return b.inmem.PauseStarted()
}
