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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	server     *grpc.Server
	srvErrCh   chan error
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

	component := newComponent(t, opts)

	server := grpc.NewServer()
	compv1pb.RegisterStateStoreServer(server, component)
	compv1pb.RegisterTransactionalStateStoreServer(server, component)
	compv1pb.RegisterQueriableStateStoreServer(server, component)
	compv1pb.RegisterTransactionalStoreMultiMaxSizeServer(server, component)
	reflection.Register(server)

	return &StateStore{
		listener:   listener,
		component:  component,
		socketName: socketFile,
		server:     server,
		srvErrCh:   make(chan error),
	}
}

func (s *StateStore) SocketName() string {
	return s.socketName
}

func (s *StateStore) Run(t *testing.T, ctx context.Context) {
	s.component.impl.Init(ctx, state.Metadata{})
	go func() {
		s.srvErrCh <- s.server.Serve(s.listener)
	}()

	conn, err := grpc.DialContext(ctx, "unix://"+s.listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)

	client := compv1pb.NewStateStoreClient(conn)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = client.Ping(ctx, new(compv1pb.PingRequest))
		//nolint:testifylint
		assert.NoError(c, err)
	}, 10*time.Second, 100*time.Millisecond)
	require.NoError(t, conn.Close())
}

func (s *StateStore) Cleanup(t *testing.T) {
	s.server.GracefulStop()
	require.NoError(t, <-s.srvErrCh)
	require.NoError(t, s.component.impl.(io.Closer).Close())
}
