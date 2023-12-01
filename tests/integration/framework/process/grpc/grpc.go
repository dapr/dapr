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
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// Option is a function that configures the process.
type Option func(*options)

// GRPC is a GRPC server that can be used in integration tests.
type GRPC struct {
	registerFns []func(*grpc.Server)
	serverOpts  []func(*testing.T, context.Context) grpc.ServerOption
	listener    func() (net.Listener, error)
	srvErrCh    chan error
	stopCh      chan struct{}
}

func New(t *testing.T, fopts ...Option) *GRPC {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	ln := opts.listener
	if ln == nil {
		// Start the listener in New so we can squat on the port immediately, and
		// keep it for the entire test case.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		ln = func() (net.Listener, error) { return listener, nil }
	}

	return &GRPC{
		listener:    ln,
		registerFns: opts.registerFns,
		serverOpts:  opts.serverOpts,
		srvErrCh:    make(chan error),
		stopCh:      make(chan struct{}),
	}
}

func (g *GRPC) Port(t *testing.T) int {
	ln, err := g.listener()
	require.NoError(t, err)
	return ln.Addr().(*net.TCPAddr).Port
}

func (g *GRPC) Address(t *testing.T) string {
	return "localhost:" + strconv.Itoa(g.Port(t))
}

func (g *GRPC) Run(t *testing.T, ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)

	opts := make([]grpc.ServerOption, len(g.serverOpts))
	for i, opt := range g.serverOpts {
		opts[i] = opt(t, ctx)
	}
	server := grpc.NewServer(opts...)
	for _, rfs := range g.registerFns {
		rfs(server)
	}

	go func() {
		ln, err := g.listener()
		if err != nil {
			g.srvErrCh <- err
			return
		}
		g.srvErrCh <- server.Serve(ln)
	}()

	go func() {
		<-g.stopCh
		cancel()
		server.GracefulStop()
	}()
}

func (g *GRPC) Cleanup(t *testing.T) {
	close(g.stopCh)
	err := <-g.srvErrCh
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, grpc.ErrServerStopped) {
		err = nil
	}
	require.NoError(t, err)
}
