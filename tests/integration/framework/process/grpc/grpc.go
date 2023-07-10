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

package grpc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// Option is a function that configures the process.
type Option func(*options)

// GRPC is a GRPC server that can be used in integration tests.
type GRPC struct {
	server   *grpc.Server
	listener net.Listener
	srvErrCh chan error
	stopCh   chan struct{}
}

func New(t *testing.T, fopts ...Option) *GRPC {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	server := grpc.NewServer()
	for _, rfs := range opts.registerFns {
		rfs(server)
	}

	// Start the listener in New so we can squat on the port immediately, and
	// keep it for the entire test case.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	return &GRPC{
		listener: listener,
		server:   server,
		srvErrCh: make(chan error),
		stopCh:   make(chan struct{}),
	}
}

func (g *GRPC) Port() int {
	return g.listener.Addr().(*net.TCPAddr).Port
}

func (g *GRPC) Run(t *testing.T, ctx context.Context) {
	go func() {
		err := g.server.Serve(g.listener)
		if !errors.Is(err, http.ErrServerClosed) {
			g.srvErrCh <- err
		} else {
			g.srvErrCh <- nil
		}
	}()

	go func() {
		<-g.stopCh
		g.server.GracefulStop()
	}()
}

func (g *GRPC) Cleanup(t *testing.T) {
	close(g.stopCh)
	require.NoError(t, <-g.srvErrCh)
}
