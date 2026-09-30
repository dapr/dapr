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

package etcd

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security/fake"
)

// TestClientKeepAliveFirstResponse verifies that a lease keepalive whose
// first response is late is not given up on after the client's 5s default.
// go-etcd-cron treats a closed keepalive channel as lost leadership, and a
// just-elected scheduler only has this first-response window while etcd
// quorum is still settling after a cluster-wide restart.
func TestClientKeepAliveFirstResponse(t *testing.T) {
	ports, err := freeport.GetFreePorts(2)
	require.NoError(t, err)

	embedded, err := New(t.Context(), Options{
		Name:                 "id1",
		Embed:                true,
		InitialCluster:       []string{"id1=http://127.0.0.1:" + strconv.Itoa(ports[0])},
		ClientPort:           uint64(ports[1]), //nolint:gosec
		ClientListenAddress:  "127.0.0.1",
		BackendBatchInterval: "50ms",
		Security:             fake.New(),
		DataDir:              t.TempDir(),
		Healthz:              healthz.New(),
		Mode:                 modes.StandaloneMode,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- embedded.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
		assert.NoError(t, embedded.(*etcd).Close())
	})

	embeddedClient, err := embedded.Client(ctx)
	require.NoError(t, err)

	external, err := New(t.Context(), Options{
		ClientEndpoints: []string{"127.0.0.1:" + strconv.Itoa(ports[1])},
		Healthz:         healthz.New(),
	})
	require.NoError(t, err)
	externalClient := external.(*etcd).client
	t.Cleanup(func() { assert.NoError(t, externalClient.Close()) })

	clients := map[string]*clientv3.Client{
		"embedded": embeddedClient,
		"external": externalClient,
	}

	leases := make(map[string]clientv3.LeaseID, len(clients))
	for name, client := range clients {
		lease, gerr := client.Grant(ctx, 20)
		require.NoError(t, gerr, name)
		leases[name] = lease.ID
	}

	embedded.(*etcd).etcd.Server.HardStop()

	keepalives := make(map[string]<-chan *clientv3.LeaseKeepAliveResponse, len(clients))
	for name, client := range clients {
		ch, kerr := client.KeepAlive(ctx, leases[name])
		require.NoError(t, kerr, name)
		keepalives[name] = ch
	}

	closed := func() bool {
		for _, ch := range keepalives {
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return true
					}
					continue
				default:
				}
				break
			}
		}
		return false
	}
	assert.Never(t, closed, 8*time.Second, 100*time.Millisecond)
}
