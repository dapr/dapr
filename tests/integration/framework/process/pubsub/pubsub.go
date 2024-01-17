/*
Copyright 2023 The Dapr Authors
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

package pubsub

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/dapr/components-contrib/pubsub"
	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

// Option is a function that configures the process.
type Option func(*options)

// PubSub is a pluggable pubsub component for Dapr.
type PubSub struct {
	listener   net.Listener
	socketName string
	component  *component
	srvErrCh   chan error
	stopCh     chan struct{}
}

func New(t *testing.T, fopts ...Option) *PubSub {
	t.Helper()

	var opts options
	for _, fopts := range fopts {
		fopts(&opts)
	}

	require.NotEmpty(t, opts.socketDir)

	socketFile := util.RandomString(t, 8)
	require.NotNil(t, opts.pubsub)

	// Start the listener in New, so we can sit on the path immediately, and
	// keep it for the entire test case.
	path := filepath.Join(opts.socketDir, socketFile+".sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)

	return &PubSub{
		listener:   listener,
		component:  newComponent(t, opts),
		socketName: socketFile,
		srvErrCh:   make(chan error),
		stopCh:     make(chan struct{}),
	}
}

func (p *PubSub) SocketName() string {
	return p.socketName
}

func (p *PubSub) Run(t *testing.T, ctx context.Context) {
	p.component.impl.Init(ctx, pubsub.Metadata{})

	server := grpc.NewServer()
	compv1pb.RegisterPubSubServer(server, p.component)
	reflection.Register(server)

	go func() {
		p.srvErrCh <- server.Serve(p.listener)
	}()

	go func() {
		<-p.stopCh
		server.GracefulStop()
	}()
}

func (p *PubSub) Cleanup(t *testing.T) {
	close(p.stopCh)
	require.NoError(t, <-p.srvErrCh)
	require.NoError(t, p.component.impl.(io.Closer).Close())
}
