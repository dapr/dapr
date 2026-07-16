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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestNewDNSConnectorErrors(t *testing.T) {
	_, err := New(Options{
		Address: "dns:///dapr-placement-server.dapr-tests.svc.cluster.local:50005",
	})
	assert.Error(t, err)
}
