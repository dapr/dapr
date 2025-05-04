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
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/socket"
)

type Binding struct {
	listener   net.Listener
	socketName string
	component  *component
	socket     *socket.Socket
	srvErrCh   chan error
	stopCh     chan struct{}
}

func New(t *testing.T, fopts ...Option) *Binding {
	t.Helper()

	os.SkipWindows(t)

	opts := options{
		socket: socket.New(t),
	}
	for _, fopts := range fopts {
		fopts(&opts)
	}

	require.NotNil(t, opts.socket)

	comp := newComponent(t, opts)

	// Start the listener in New, so we can sit on the path immediately, and
	// keep it for the entire test case.
	socketFile := opts.socket.File(t)
	listener, err := net.Listen("unix", socketFile.Filename())
	require.NoError(t, err)

	return &Binding{
		listener:   listener,
		component:  comp,
		socketName: socketFile.Name(),
		socket:     opts.socket,
		srvErrCh:   make(chan error),
		stopCh:     make(chan struct{}),
	}
}

func (b *Binding) Run(t *testing.T, ctx context.Context) {
	b.component.Init(ctx, new(compv1pb.InputBindingInitRequest))

	server := grpc.NewServer()
	compv1pb.RegisterInputBindingServer(server, b.component)
	reflection.Register(server)

	go func() {
		b.srvErrCh <- server.Serve(b.listener)
	}()

	go func() {
		<-b.stopCh
		server.GracefulStop()
	}()
}

func (b *Binding) Cleanup(t *testing.T) {
	close(b.stopCh)
	require.NoError(t, <-b.srvErrCh)
}

func (b *Binding) SocketName() string {
	return b.socketName
}

func (b *Binding) Socket() *socket.Socket {
	return b.socket
}

func (b *Binding) SendMessage(r *compv1pb.ReadResponse) <-chan *compv1pb.ReadRequest {
	b.component.msgCh <- r
	return b.component.respCh
}
