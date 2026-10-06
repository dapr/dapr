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

package inflight

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
)

// TestResetSessionReleasesAbortedRoundScope covers a round aborted by
// stream loss between LOCK and UNLOCK: the reset lifts the block and the
// next session's first Open resolves the parked lookup to no address.
func TestResetSessionReleasesAbortedRoundScope(t *testing.T) {
	t.Parallel()

	i := New(Options{Hostname: "10.0.0.1", Port: "50002"})
	i.Open(t.Context())

	// A round locks the type, then the stream dies before its UNLOCK.
	i.LockTypes([]string{"gonetype"})

	respCh := make(chan *loops.LookupResponse, 1)
	i.AcquireLookup(&loops.LookupRequest{
		Request:  &api.LookupActorRequest{ActorType: "gonetype", ActorID: "a"},
		Context:  t.Context(),
		Response: respCh,
	})
	require.Empty(t, respCh, "a lookup for a locked type must queue")

	i.Close(nil)
	i.ResetSession()

	// The reconnected session's first round opens the queue: the type never
	// returned, so the parked lookup resolves to no address.
	i.Open(t.Context())

	select {
	case resp := <-respCh:
		require.NotNil(t, resp)
		require.Error(t, resp.Error)
	case <-time.After(time.Second * 5):
		require.Fail(t, "the parked lookup must resolve after the session reset")
	}
}
