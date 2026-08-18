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

package clients

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	securityfake "github.com/dapr/dapr/pkg/security/fake"
)

// Reload must never block on borrowers of the previous generation: an
// in-flight RPC against a scheduler that died mid-shutdown can hang
// indefinitely (the transport still ACKs keepalives), and blocking Reload
// while holding the write lock wedges every scheduler operation in the
// runtime permanently.
func Test_ReloadDoesNotBlockOnBorrowers(t *testing.T) {
	t.Parallel()

	c := New(Options{Security: securityfake.New()})
	ctx := t.Context()

	// gRPC dials are lazy, so unreachable addresses still produce clients.
	require.NoError(t, c.Reload(ctx, []string{"127.0.0.1:1"}))

	// Borrow a client and do NOT release it (simulates a hung RPC).
	_, done, err := c.Next(ctx)
	require.NoError(t, err)

	reloaded := make(chan error, 1)
	go func() {
		reloaded <- c.Reload(ctx, []string{"127.0.0.1:2", "127.0.0.1:3"})
	}()

	select {
	case err = <-reloaded:
		require.NoError(t, err)
	case <-time.After(time.Second * 5):
		t.Fatal("Reload blocked on an unreleased borrower from the previous generation")
	}

	assert.Equal(t, []string{"127.0.0.1:2", "127.0.0.1:3"}, c.Addresses())

	// Next must serve from the new generation while the old borrow is live.
	_, done2, err := c.Next(ctx)
	require.NoError(t, err)
	done2()

	// Releasing the stale borrow after the swap must not panic.
	done()
}

func Test_NextRoundRobin(t *testing.T) {
	t.Parallel()

	c := New(Options{Security: securityfake.New()})
	ctx := t.Context()

	require.NoError(t, c.Reload(ctx, []string{"127.0.0.1:1", "127.0.0.1:2"}))

	cl1, done1, err := c.Next(ctx)
	require.NoError(t, err)
	cl2, done2, err := c.Next(ctx)
	require.NoError(t, err)
	assert.NotSame(t, cl1, cl2)
	done1()
	done2()
}
