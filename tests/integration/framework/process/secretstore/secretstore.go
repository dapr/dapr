/*
Copyright 2024 The Dapr Authors
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

package secretstore

import (
	"context"
	"net"
	"testing"

	"github.com/dapr/components-contrib/secretstores"
	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Option is a function that configures the process.
type Option func(*options)

// SecretStore is a pluggable pubsub component for Dapr.
type SecretStore struct {
	listener   net.Listener
	socketName string
	component  *component
	srvErrCh   chan error
	stopCh     chan struct{}
}

func New(t *testing.T, fopts ...Option) *SecretStore {
	t.Helper()
	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.NotNil(t, opts.socket)
	require.NotNil(t, opts.secretStore)
	// Start the listener in New, so we can sit on the path immediately, and
	// keep it for the entire test case.
	socketFile := opts.socket.File(t)
	listener, err := net.Listen("unix", socketFile.Filename())
	require.NoError(t, err)

	return &SecretStore{
		listener:   listener,
		component:  newComponent(t, opts),
		socketName: socketFile.Name(),
		srvErrCh:   make(chan error),
		stopCh:     make(chan struct{}),
	}
}

func (s *SecretStore) SocketName() string {
	return s.socketName
}

func (s *SecretStore) Run(t *testing.T, ctx context.Context) {
	s.component.impl.Init(ctx, secretstores.Metadata{})

	server := grpc.NewServer()
	compv1pb.RegisterSecretStoreServer(server, s.component)
	reflection.Register(server)

	go func() {
		s.srvErrCh <- server.Serve(s.listener)
	}()

	go func() {
		<-s.stopCh
		server.GracefulStop()
	}()
}

func (s *SecretStore) Cleanup(t *testing.T) {
	close(s.stopCh)
	require.NoError(t, <-s.srvErrCh)
	require.NoError(t, s.component.Close())
}
