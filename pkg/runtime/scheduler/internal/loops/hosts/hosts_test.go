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

package hosts

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops"
	secfake "github.com/dapr/dapr/pkg/security/fake"
	"github.com/dapr/kit/events/loop"
)

type fakeConnector struct {
	connects []*loops.Connect
}

func (f *fakeConnector) Run(ctx context.Context) error { return nil }
func (f *fakeConnector) Close(t loops.EventConn)       {}
func (f *fakeConnector) Enqueue(t loops.EventConn) {
	if c, ok := t.(*loops.Connect); ok {
		f.connects = append(f.connects, c)
	}
}

// Test_ReloadClients_IdempotentForUnchangedAddresses pins that a re-emitted,
// unchanged host list (WatchHosts re-sends it whenever its own stream
// reconnects) does not rebuild the scheduler clients: a rebuild tears down
// every WatchJobs stream, aborting and redelivering their inflight triggers,
// which turns a single WatchHosts flap at startup into duplicate job
// deliveries.
func Test_ReloadClients_IdempotentForUnchangedAddresses(t *testing.T) {
	t.Parallel()

	conn := &fakeConnector{}
	h := &hosts{
		streamN:   1,
		security:  secfake.New(),
		connector: conn,
	}

	require.NoError(t, h.handleReloadClients(t.Context(), &loops.ReloadClients{
		Addresses: []string{"127.0.0.1:1", "127.0.0.1:2"},
	}))
	require.Len(t, conn.connects, 1)

	// Same set re-emitted (any order): no rebuild.
	require.NoError(t, h.handleReloadClients(t.Context(), &loops.ReloadClients{
		Addresses: []string{"127.0.0.1:2", "127.0.0.1:1"},
	}))
	require.Len(t, conn.connects, 1)

	// A genuinely different set rebuilds.
	require.NoError(t, h.handleReloadClients(t.Context(), &loops.ReloadClients{
		Addresses: []string{"127.0.0.1:3"},
	}))
	require.Len(t, conn.connects, 2)
}

var _ loop.Interface[loops.EventConn] = (*fakeConnector)(nil)
