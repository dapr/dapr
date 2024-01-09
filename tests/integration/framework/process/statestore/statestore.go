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

package statestore

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/dapr/components-contrib/state"
	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

// Option is a function that configures the process.
type Option func(*options)

// StateStore is a pluggable state store component for Dapr.
type StateStore struct {
	listener   net.Listener
	socketName string
	component  *component
	srvErrCh   chan error
	stopCh     chan struct{}
}

func New(t *testing.T, fopts ...Option) *StateStore {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.NotEmpty(t, opts.socketDir)

	socketFile := util.RandomString(t, 8)

	require.NotNil(t, opts.statestore)

	// Start the listener in New so we can squat on the path immediately, and
	// keep it for the entire test case.
	path := filepath.Join(opts.socketDir, socketFile+".sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)

	return &StateStore{
		listener:   listener,
		component:  newComponent(t, opts),
		socketName: socketFile,
		srvErrCh:   make(chan error),
		stopCh:     make(chan struct{}),
	}
}

func (s *StateStore) SocketName() string {
	return s.socketName
}

func (s *StateStore) Run(t *testing.T, ctx context.Context) {
	s.component.impl.Init(ctx, state.Metadata{})

	server := grpc.NewServer()
	compv1pb.RegisterStateStoreServer(server, s.component)
	compv1pb.RegisterTransactionalStateStoreServer(server, s.component)
	compv1pb.RegisterQueriableStateStoreServer(server, s.component)
	compv1pb.RegisterTransactionalStoreMultiMaxSizeServer(server, s.component)
	reflection.Register(server)

	go func() {
		s.srvErrCh <- server.Serve(s.listener)
	}()

	go func() {
		<-s.stopCh
		server.GracefulStop()
	}()
}

func (s *StateStore) Cleanup(t *testing.T) {
	close(s.stopCh)
	require.NoError(t, <-s.srvErrCh)
	require.NoError(t, s.component.impl.(io.Closer).Close())
}
