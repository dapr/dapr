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

package runtime

import (
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/api/grpc"
	"github.com/dapr/dapr/pkg/modes"
)

func TestGRPCServers_closeDuringStart(t *testing.T) {
	cases := map[string]struct {
		start func(rt *DaprRuntime, port int) error
		close func(rt *DaprRuntime) error
		setup func(rt *DaprRuntime, port int)
	}{
		"api server": {
			setup: func(rt *DaprRuntime, port int) {
				rt.runtimeConfig.apiListenAddresses = []string{DefaultChannelAddress}
				rt.runtimeConfig.apiGRPCPort = port
			},
			start: func(rt *DaprRuntime, port int) error {
				return rt.startGRPCAPIServer(t.Context(), nil, port)
			},
			close: func(rt *DaprRuntime) error { return closeServer(rt.closingGRPCServer(&rt.grpcAPIServer)) },
		},
		"internal server": {
			setup: func(rt *DaprRuntime, port int) {
				rt.runtimeConfig.internalGRPCListenAddress = DefaultChannelAddress
				rt.runtimeConfig.internalGRPCPort = port
			},
			start: func(rt *DaprRuntime, _ int) error {
				return rt.startGRPCInternalServer(t.Context(), nil)
			},
			close: func(rt *DaprRuntime) error { return closeServer(rt.closingGRPCServer(&rt.grpcInternalServer)) },
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			for i := range 30 {
				rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
				require.NoError(t, err)

				port, err := freeport.GetFreePort()
				require.NoError(t, err)
				tc.setup(rt, port)

				var wg sync.WaitGroup
				start := make(chan struct{})
				wg.Go(func() {
					<-start
					_ = tc.start(rt, port)
				})
				wg.Go(func() {
					<-start
					assert.NoError(t, tc.close(rt))
				})
				close(start)
				wg.Wait()

				ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", DefaultChannelAddress, port))
				require.NoError(t, err, "iteration %d: a gRPC server was left running after close", i)
				require.NoError(t, ln.Close())
			}
		})
	}
}

func TestGRPCServers_startAfterCloseRefused(t *testing.T) {
	rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
	require.NoError(t, err)

	port, err := freeport.GetFreePort()
	require.NoError(t, err)
	rt.runtimeConfig.apiListenAddresses = []string{DefaultChannelAddress}
	rt.runtimeConfig.apiGRPCPort = port
	rt.runtimeConfig.internalGRPCListenAddress = DefaultChannelAddress
	rt.runtimeConfig.internalGRPCPort = port

	require.Nil(t, rt.closingGRPCServer(&rt.grpcAPIServer))
	require.Nil(t, rt.closingGRPCServer(&rt.grpcInternalServer))

	require.Error(t, rt.startGRPCAPIServer(t.Context(), nil, port))
	require.Error(t, rt.startGRPCInternalServer(t.Context(), nil))

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", DefaultChannelAddress, port))
	require.NoError(t, err)
	require.NoError(t, ln.Close())
}

func closeServer(server grpc.Server) error {
	if server == nil {
		return nil
	}
	return server.Close()
}
