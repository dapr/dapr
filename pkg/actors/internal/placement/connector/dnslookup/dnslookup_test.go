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
package dnslookup

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func lookupHost(addrs []string) lookupFunc {
	return func(ctx context.Context, host string) ([]string, error) {
		return addrs, nil
	}
}

func TestNewDNSConnector(t *testing.T) {
	conn, err := New(Options{
		Address:  "dapr-placement-server.dapr-tests.svc.cluster.local:50005",
		resolver: lookupHost([]string{"add1", "add2", "add3"}),
	})
	require.NoError(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "add1:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "add2:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "add3:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "add1:50005", conn.Address())
}

func TestAuthorityOptionStability(t *testing.T) {
	conn, err := New(Options{
		Address:     "dapr-placement-server.dapr-tests.svc.cluster.local:50005",
		GRPCOptions: []grpc.DialOption{grpc.WithInsecure()}, //nolint:staticcheck
		resolver:    lookupHost([]string{"add1", "add2", "add3"}),
	})
	require.NoError(t, err)

	c := conn.(*dnsLookUpConnector)

	// WithAuthority must be appended to the user-supplied options.
	require.Len(t, c.gOpts, 2)

	// Verify the option list doesn't grow on repeated Connect calls.
	before := len(c.gOpts)
	_, _ = conn.Connect(t.Context())
	_, _ = conn.Connect(t.Context())
	assert.Equal(t, before, len(c.gOpts))
}

// TestConnectSetsAuthorityHeader verifies that the gRPC :authority header
// received by the server matches the original DNS hostname, not the resolved
// IP address. This is the core behaviour that WithAuthority enables — it
// allows SNI-based TLS gateways to route the connection correctly.
func TestConnectSetsAuthorityHeader(t *testing.T) {
	const expectedHost = "dapr-placement-server.dapr-tests.svc.cluster.local"

	// authorityCreds wraps insecure credentials but captures the authority
	// string that gRPC passes during the client TLS/transport handshake.
	// When grpc.WithAuthority is set, this value is the overridden authority
	// (the original DNS name) rather than the dialed IP address.
	creds := &authorityCreds{
		TransportCredentials: insecure.NewCredentials(),
	}

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	t.Cleanup(srv.Stop)
	go func() { _ = srv.Serve(lis) }()

	conn, err := New(Options{
		Address: expectedHost + ":50005",
		GRPCOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(creds),
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return lis.DialContext(t.Context())
			}),
		},
		resolver: lookupHost([]string{"127.0.0.1"}),
	})
	require.NoError(t, err)

	grpcConn, err := conn.Connect(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { grpcConn.Close() })

	// Trigger a real RPC so the transport handshake (and authority capture)
	// actually happens. The method is unregistered so the server returns
	// Unimplemented, but that's fine — we only care about the authority.
	//nolint:staticcheck
	_ = grpcConn.Invoke(t.Context(), "/test.Service/Ping", nil, &struct{}{})

	got, ok := creds.authority.Load().(string)
	require.True(t, ok, "transport credentials did not capture the authority")
	assert.Equal(t, expectedHost, got,
		"expected :authority to be the original DNS hostname, not the resolved IP")
}

// authorityCreds wraps a credentials.TransportCredentials and records the
// authority value that gRPC supplies during the client handshake.
type authorityCreds struct {
	credentials.TransportCredentials
	authority atomic.Value
}

func (c *authorityCreds) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	c.authority.Store(authority)
	return c.TransportCredentials.ClientHandshake(ctx, authority, rawConn)
}

func (c *authorityCreds) Clone() credentials.TransportCredentials {
	return &authorityCreds{
		TransportCredentials: c.TransportCredentials.Clone(),
	}
}

func TestNewDNSConnectorErrors(t *testing.T) {
	_, err := New(Options{
		Address: "dns:///dapr-placement-server.dapr-tests.svc.cluster.local:50005",
	})
	assert.Error(t, err)
}
