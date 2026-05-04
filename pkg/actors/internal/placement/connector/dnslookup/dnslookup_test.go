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
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRoundRobinAcrossAddresses(t *testing.T) {
	conn, err := New(Options{
		Address: "placement.svc:50005",
		resolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, nil
		},
	})
	require.NoError(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.1:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.2:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.3:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.1:50005", conn.Address())
}

func TestResolvesOnEveryConnect(t *testing.T) {
	var lookupCount atomic.Int32
	conn, err := New(Options{
		Address: "placement.svc:50005",
		resolver: func(ctx context.Context, host string) ([]string, error) {
			lookupCount.Add(1)
			return []string{"10.0.0.1"}, nil
		},
	})
	require.NoError(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, int32(1), lookupCount.Load())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, int32(2), lookupCount.Load())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, int32(3), lookupCount.Load())
}

func TestPicksUpNewIPsImmediately(t *testing.T) {
	var callCount atomic.Int32
	conn, err := New(Options{
		Address: "placement.svc:50005",
		resolver: func(ctx context.Context, host string) ([]string, error) {
			n := callCount.Add(1)
			if n <= 2 {
				return []string{"10.0.0.1", "10.0.0.2"}, nil
			}
			return []string{"10.0.0.3", "10.0.0.4"}, nil
		},
	})
	require.NoError(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.1:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.2:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.3:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.4:50005", conn.Address())
}

func TestDNSFailureReturnsError(t *testing.T) {
	conn, err := New(Options{
		Address: "placement.svc:50005",
		resolver: func(ctx context.Context, host string) ([]string, error) {
			return nil, errors.New("dns: no such host")
		},
	})
	require.NoError(t, err)

	_, err = conn.Connect(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dns: no such host")
}

func TestDNSReturnsEmptyList(t *testing.T) {
	conn, err := New(Options{
		Address: "placement.svc:50005",
		resolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{}, nil
		},
	})
	require.NoError(t, err)

	_, err = conn.Connect(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no addresses found")
}

func TestDNSFailureThenRecovery(t *testing.T) {
	var callCount atomic.Int32
	conn, err := New(Options{
		Address: "placement.svc:50005",
		resolver: func(ctx context.Context, host string) ([]string, error) {
			n := callCount.Add(1)
			if n == 1 {
				return nil, errors.New("temporary DNS failure")
			}
			return []string{"10.0.0.1"}, nil
		},
	})
	require.NoError(t, err)

	_, err = conn.Connect(t.Context())
	require.Error(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.1:50005", conn.Address())
}

func TestRoundRobinWrapsWhenDNSResultsShrink(t *testing.T) {
	var callCount atomic.Int32
	conn, err := New(Options{
		Address: "placement.svc:50005",
		resolver: func(ctx context.Context, host string) ([]string, error) {
			n := callCount.Add(1)
			if n <= 3 {
				return []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, nil
			}
			return []string{"10.0.0.4"}, nil
		},
	})
	require.NoError(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.1:50005", conn.Address())
	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.2:50005", conn.Address())
	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.3:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "10.0.0.4:50005", conn.Address())
}

func TestAuthorityOptionStability(t *testing.T) {
	inputOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	conn, err := New(Options{
		Address:     "dapr-placement-server.dapr-tests.svc.cluster.local:50005",
		GRPCOptions: inputOpts,
		resolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{"add1", "add2", "add3"}, nil
		},
	})
	require.NoError(t, err)

	c := conn.(*dnsLookUpConnector)

	// WithAuthority must be appended to the user-supplied options.
	require.Len(t, c.gOpts, len(inputOpts)+1)

	// Verify the option list doesn't grow on repeated Connect calls.
	before := len(c.gOpts)
	gc1, _ := conn.Connect(t.Context())
	if gc1 != nil {
		t.Cleanup(func() { gc1.Close() })
	}
	gc2, _ := conn.Connect(t.Context())
	if gc2 != nil {
		t.Cleanup(func() { gc2.Close() })
	}
	assert.Equal(t, before, len(c.gOpts))
}

// TestConnectOverridesClientAuthority verifies that the authority string the
// client passes into the transport handshake (and therefore uses for SNI /
// the HTTP/2 :authority pseudo-header) matches the original input hostname,
// not the resolved IP address. This is the core behaviour that WithAuthority
// enables — it allows SNI-based TLS gateways to route the connection correctly.
//
// The IPv6 case also locks in that bracketed literals stay bracketed in the
// authority, since net.SplitHostPort strips them from the host field.
func TestConnectOverridesClientAuthority(t *testing.T) {
	tests := []struct {
		name              string
		address           string
		expectedAuthority string
	}{
		{
			name:              "DNS hostname",
			address:           "dapr-placement-server.dapr-tests.svc.cluster.local:50005",
			expectedAuthority: "dapr-placement-server.dapr-tests.svc.cluster.local",
		},
		{
			name:              "IPv6 literal is bracketed",
			address:           "[fd00::1]:50005",
			expectedAuthority: "[fd00::1]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// authorityCreds wraps insecure credentials but captures the
			// authority string that gRPC passes during the client transport
			// handshake. When grpc.WithAuthority is set, this is the overridden
			// authority (the original input host) rather than the dialed IP.
			creds := &authorityCreds{
				TransportCredentials: insecure.NewCredentials(),
				authority:            new(atomic.Value),
			}

			lis := bufconn.Listen(1024 * 1024)
			srv := grpc.NewServer()
			t.Cleanup(srv.Stop)
			go func() { _ = srv.Serve(lis) }()

			conn, err := New(Options{
				Address: tc.address,
				GRPCOptions: []grpc.DialOption{
					grpc.WithTransportCredentials(creds),
					grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
						return lis.DialContext(t.Context())
					}),
				},
				resolver: func(ctx context.Context, host string) ([]string, error) {
					return []string{"127.0.0.1"}, nil
				},
			})
			require.NoError(t, err)

			grpcConn, err := conn.Connect(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { grpcConn.Close() })

			// Trigger a real RPC so the transport handshake (and authority
			// capture) actually happens. The method is unregistered so the
			// server returns Unimplemented, but that's fine — we only care
			// about the authority.
			_ = grpcConn.Invoke(t.Context(), "/test.Service/Ping", &emptypb.Empty{}, &emptypb.Empty{})

			got, ok := creds.authority.Load().(string)
			require.True(t, ok, "transport credentials did not capture the authority")
			assert.Equal(t, tc.expectedAuthority, got)
		})
	}
}

// authorityCreds wraps a credentials.TransportCredentials and records the
// authority value that gRPC supplies during the client handshake.
//
// gRPC clones transport credentials per connection, so the capture state is
// held behind a pointer that Clone copies — otherwise the authority would be
// stored on a clone the test never sees.
type authorityCreds struct {
	credentials.TransportCredentials
	authority *atomic.Value
}

func (c *authorityCreds) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	c.authority.Store(authority)
	return c.TransportCredentials.ClientHandshake(ctx, authority, rawConn)
}

func (c *authorityCreds) Clone() credentials.TransportCredentials {
	return &authorityCreds{
		TransportCredentials: c.TransportCredentials.Clone(),
		authority:            c.authority,
	}
}

func TestNewDNSConnectorErrors(t *testing.T) {
	_, err := New(Options{
		Address: "dns:///dapr-placement-server.dapr-tests.svc.cluster.local:50005",
	})
	assert.Error(t, err)
}
